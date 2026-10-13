package agent

// Feature 049-C · capturas de pantalla del monitoreo de actividad.
//
// Reutiliza `CaptureScreen()` (la misma vía verificada del modo perdido: baja a
// la sesión del usuario y descarta las imágenes en negro). La imagen se reduce
// y se recomprime a JPEG antes de subirla —una captura PNG de 1080p pesa varios
// MB y acá se suben cada pocos minutos—, y se borra del equipo apenas sube: el
// equipo no acumula fotos de la pantalla de nadie.
//
// Sólo se captura con el usuario ACTIVO y la sesión desbloqueada: una captura
// del protector de pantalla no aporta y sí gasta.

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	rmm "github.com/braincorp-cl/observer-agent-dist/shared"
)

const (
	capturaActAnchoMax = 1366
	capturaActCalidad  = 60
)

var capturaActEnCurso atomic.Bool

// reducirImagen escala por muestreo simple (vecino más cercano) a un ancho
// máximo. Suficiente para leer una pantalla; evita depender de x/image.
func reducirImagen(img image.Image, anchoMax int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= anchoMax || w == 0 {
		return img
	}
	nw := anchoMax
	nh := h * nw / w
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + y*h/nh
		for x := 0; x < nw; x++ {
			dst.Set(x, y, img.At(b.Min.X+x*w/nw, sy))
		}
	}
	return dst
}

// aJPEGReducido convierte la captura a JPEG reducido. Si no es un PNG legible
// (algún SO ya escribe JPEG), la devuelve tal cual.
func aJPEGReducido(data []byte) ([]byte, string) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return data, "png"
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, reducirImagen(img, capturaActAnchoMax), &jpeg.Options{Quality: capturaActCalidad}); err != nil {
		return data, "png"
	}
	// Una pantalla casi plana comprime mejor en PNG que en JPEG: se sube la
	// más liviana de las dos.
	if buf.Len() >= len(data) {
		return data, "png"
	}
	return buf.Bytes(), "jpg"
}

// capturaActividad toma, reduce y sube una captura. Corre en su goroutine; si
// ya hay una en curso, no apila otra.
func (a *Agent) capturaActividad(m rmm.ActividadMuestra) {
	a.capturaActividadModo(m, false)
}

// capturaActividadModo devuelve el resultado para la captura bajo demanda
// (049-E): "ok", "en_curso", "sin_imagen", "capturas_off" o "error".
func (a *Agent) capturaActividadModo(m rmm.ActividadMuestra, manual bool) string {
	if !capturaActEnCurso.CompareAndSwap(false, true) {
		return "en_curso"
	}
	defer capturaActEnCurso.Store(false)

	shot := a.CaptureScreen()
	if !shot.OK() {
		a.Logger.Debugln("actividad: captura sin imagen:", shot.Reason)
		return "sin_imagen"
	}
	defer os.Remove(shot.Path)

	data, err := os.ReadFile(shot.Path)
	if err != nil || len(data) == 0 {
		return "sin_imagen"
	}
	img, ext := aJPEGReducido(data)
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("obs-act-%d.%s", time.Now().UnixNano(), ext))
	if err := os.WriteFile(tmp, img, 0600); err != nil {
		return "error"
	}
	defer os.Remove(tmp)

	campos := map[string]string{
		"usuario": m.Usuario,
		"proceso": m.Proceso,
		"titulo":  strings.TrimSpace(m.Titulo),
		"url":     m.URL,
	}
	if manual {
		campos["manual"] = "1"
	}

	ts := time.Now()
	campos["captured_at"] = ts.Format(time.RFC3339)
	url := fmt.Sprintf("/api/v3/%s/activity/screenshot/", a.AgentID)
	r, err := a.rClient.R().
		SetFormData(campos).
		SetFile("image", tmp).
		Post(url)
	if err != nil {
		a.Logger.Debugln("actividad: no se pudo subir la captura:", err)
		return "error"
	}
	if r.StatusCode() == http.StatusConflict {
		// El servidor ya no quiere capturas de este equipo (se apagaron mientras
		// estaba sin red): se apagan acá sin esperar al polling de config.
		_, red := a.getActividadExtras()
		a.SetActividadExtras(0, red)
		return "capturas_off"
	}
	if r.StatusCode() >= 300 {
		a.Logger.Debugln("actividad: el servidor rechazó la captura:", r.StatusCode())
		return "error"
	}
	actAccMu.Lock()
	if actAcc != nil {
		actAcc.SumarCaptura(ts, m.Usuario)
		a.guardarAcumulador(actAcc)
	}
	actAccMu.Unlock()
	return "ok"
}

// CapturaBajoDemanda (049-E, RPC `actividad_captura`): la consola pide una
// captura del momento para el monitoreo en vivo. Respeta lo mismo que la
// periódica: monitoreo y capturas encendidos, sesión con usuario y desbloqueada.
func (a *Agent) CapturaBajoDemanda() string {
	active, _, _, conTitulo := a.getActividad()
	capturasMin, _ := a.getActividadExtras()
	if !active {
		return "monitoreo_off"
	}
	if capturasMin <= 0 {
		return "capturas_off"
	}
	m, err := a.MuestraActividadComoUsuario(conTitulo)
	if err != nil {
		return "sin_sesion"
	}
	return motivoCapturaManual(m, func() string { return a.capturaActividadModo(m, true) })
}

// motivoCapturaManual decide sin tocar la pantalla si corresponde capturar.
func motivoCapturaManual(m rmm.ActividadMuestra, capturar func() string) string {
	if m.Error != "" || m.Usuario == "" {
		return "sin_sesion"
	}
	if m.Bloqueado {
		return "bloqueado"
	}
	return capturar()
}
