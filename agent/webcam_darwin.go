//go:build darwin
// +build darwin

package agent

import (
	"context"
	"os"
	"strings"
	"time"
)

// Feature 030 · Fase 2 · foto de webcam en macOS (T016) — GATED.
//
// EL MURO ES EL MISMO QUE EN LA PANTALLA, Y ACÁ ES DEFINITIVO: el permiso TCC
// de Cámara **ningún MDM lo puede conceder**. El perfil PPPC de Apple sólo sabe
// NEGAR cámara, micrófono y pantalla; concederlos exige que una persona lo haga
// a mano en Ajustes del Sistema, frente al equipo. Se verificó además que
// ningún producto del rubro lo evade: la propia documentación de HiddenApp y la
// de Prey instruyen habilitarlo a mano.
//
// Consecuencia que el producto declara en vez de disimular: **en una flota Mac
// administrada no hay foto de webcam**, salvo que alguien la haya autorizado a
// mano antes del robo. Este archivo existe para decir eso con un código
// accionable —`permiso_denegado`— y no para simular que lo intenta.
//
// LA SEGUNDA MITAD DEL MURO, que es la que sorprende: macOS **no trae ninguna
// herramienta de línea de comandos para tomar una foto**. `screencapture` es
// del sistema y por eso la Fase 1 no necesitó instalar nada; su equivalente de
// cámara no existe. Así que aunque el permiso estuviera concedido, hace falta
// `imagesnap` o `ffmpeg`, que no vienen con el sistema. Si no están, se reporta
// `sin_herramienta` — que es distinto de "no hay permiso" y de "no hay cámara",
// y esa diferencia es exactamente lo que el operador necesita para saber si
// vale la pena ir a tocar ese equipo.
//
// 🔴 SIN TERRENO. No se ha probado en un Mac real, y el único disponible no se
// puede usar para esto (ver la nota de la Fase 2 en actions.md).

// herramientasWebcamDarwin, en orden de preferencia. `%s` es la ruta destino.
//
// `imagesnap` va primero porque está hecho para esto y su `-w` espera a que la
// exposición automática converja, que es el problema central de fotografiar con
// una webcam (ver `cuadrosDeCalentamiento`). Su unidad son SEGUNDOS de espera,
// no cuadros: 1 s cubre de sobra los ~20 cuadros que se descartan en las otras
// plataformas.
var herramientasWebcamDarwin = []struct {
	bin  string
	args []string
}{
	{"/usr/local/bin/imagesnap", []string{"-q", "-w", "1", "%s"}},
	{"/opt/homebrew/bin/imagesnap", []string{"-q", "-w", "1", "%s"}},
	{"/usr/local/bin/ffmpeg", []string{
		"-hide_banner", "-loglevel", "error",
		"-f", "avfoundation", "-framerate", "30", "-i", "0",
		"-frames:v", "20", "-update", "1", "-y", "%s",
	}},
	{"/opt/homebrew/bin/ffmpeg", []string{
		"-hide_banner", "-loglevel", "error",
		"-f", "avfoundation", "-framerate", "30", "-i", "0",
		"-frames:v", "20", "-update", "1", "-y", "%s",
	}},
}

func (a *Agent) capturarWebcam(nombre string) WebcamShot {
	if !a.permisoDeCamaraConcedido() {
		a.Logger.Infoln("capturarWebcam: macOS no tiene concedido el permiso de Cámara, no se captura")
		return WebcamShot{Reason: webcamDenied}
	}

	bin, args, ok := elegirHerramientaWebcamDarwin()
	if !ok {
		a.Logger.Infoln("capturarWebcam: macOS no trae herramienta de cámara y no hay ninguna instalada (se buscó imagesnap, ffmpeg)")
		return WebcamShot{Reason: webcamNoTool}
	}

	// El directorio del usuario, no el del agente: el proceso corre con la
	// sesión de consola y `/opt/observeragent` es de root. Mismo criterio que la
	// captura de pantalla.
	destino := destinoFotoWebcam(nombre)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	finales := make([]string, 0, len(args))
	for _, arg := range args {
		finales = append(finales, strings.ReplaceAll(arg, "%s", destino))
	}

	cmd, err := a.newUserSessionCmd(ctx, bin, finales...)
	if err != nil {
		// Sin sesión de consola no hay contexto TCC en el que la cámara pueda
		// estar concedida, así que el motivo honesto es el permiso y no "no hay
		// sesión": la cámara, a diferencia de la pantalla, no necesitaría una.
		a.Logger.Debugln("capturarWebcam: no hay sesión de usuario de consola:", err)
		return WebcamShot{Reason: webcamDenied}
	}

	salida, err := cmd.CombinedOutput()
	if err != nil {
		a.Logger.Debugln("capturarWebcam:", bin, "falló:", err, "salida:", strings.TrimSpace(string(salida)))
		_ = os.Remove(destino)
		return WebcamShot{Reason: webcamFailed}
	}

	// El `exit 0` no prueba el efecto — la lección de la 025 y de la Fase 1.
	info, err := os.Stat(destino)
	if err != nil || info.Size() == 0 {
		a.Logger.Debugln("capturarWebcam:", bin, "salió con 0 pero no dejó archivo utilizable")
		_ = os.Remove(destino)
		return WebcamShot{Reason: webcamFailed}
	}

	return WebcamShot{Path: destino}
}

// permisoDeCamaraConcedido consulta TCC ANTES de intentar la foto.
//
// Mismo truco que `permisoDeCapturaConcedido`: AppleScript cargando el
// framework, sin cgo ni helper compilado, porque parte de la matriz de release
// cross-compila macOS desde Linux con CGO_ENABLED=0.
//
// `authorizationStatusForMediaType:` devuelve 3 (`AVAuthorizationStatusAuthorized`)
// cuando está concedido; 0 sin determinar, 1 restringido, 2 denegado. Sólo el 3
// habilita la captura: pedirla en el estado 0 dispararía el diálogo del sistema
// **en la cara de quien tiene el equipo**, que en un caso de robo es justo lo
// que no se quiere.
//
// El texto del script vive en `webcam.go`, sin etiqueta de plataforma, para que
// la CI pueda vigilarlo: ver `scriptEstadoTCCCamara` y el terreno del 2026-08-13.
func (a *Agent) permisoDeCamaraConcedido() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd, err := a.newUserSessionCmd(ctx, "/usr/bin/osascript", "-e", scriptEstadoTCCCamara)
	if err != nil {
		a.Logger.Debugln("permisoDeCamaraConcedido: sin sesión de usuario para consultar TCC:", err)
		return false
	}

	salida, err := cmd.Output()
	if err != nil {
		// A DIFERENCIA de la captura de pantalla, acá el fallo de consulta
		// devuelve `false` y no `true`. En la pantalla, asumir que sí y dejar que
		// la verificación de negro atrape el error es barato. Acá no: intentar
		// abrir la cámara con el permiso sin determinar **muestra el diálogo del
		// sistema al usuario**, y en un equipo robado eso avisa al ladrón. Ante
		// la duda, no se abre la cámara.
		a.Logger.Debugln("permisoDeCamaraConcedido: no se pudo consultar TCC:", err)
		return false
	}

	return strings.TrimSpace(string(salida)) == estadoTCCCamaraConcedido
}

func elegirHerramientaWebcamDarwin() (string, []string, bool) {
	for _, h := range herramientasWebcamDarwin {
		if info, err := os.Stat(h.bin); err == nil && !info.IsDir() {
			return h.bin, h.args, true
		}
	}
	return "", nil, false
}
