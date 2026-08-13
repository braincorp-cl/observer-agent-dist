//go:build linux
// +build linux

package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Feature 030 · Fase 2 · foto de webcam en Linux (T015, enmendado por
// W-030-F2-06).
//
// LA DIFERENCIA GRANDE CON LA CAPTURA DE PANTALLA: acá NO se usa el puente de
// sesión de la 028. La pantalla hay que leerla desde dentro de la sesión gráfica
// del usuario (X11 no deja de otra forma); la cámara es un dispositivo en
// `/dev`, el agente corre como root y root lo abre directo. Consecuencias:
//
//   - funciona en la pantalla de inicio de sesión, sin que nadie haya entrado;
//   - funciona en Wayland, que sólo afecta a la captura de pantalla;
//   - no hace falta que el usuario esté en el grupo `video`.
//
// El archivo NO cae en el directorio del usuario, justamente porque lo escribe
// root. Dónde cae exactamente es una decisión con su propia historia: ver
// `destinoFotoWebcam` en webcam.go.
//
// EL ORDEN DE INTENTOS LO FIJÓ EL TERRENO. Primero V4L2 nativo
// (`webcam_v4l2_linux.go`), que no necesita nada instalado; `fswebcam` y
// `ffmpeg` quedan de respaldo para las cámaras que no den ni MJPEG ni YUYV. Fue
// al revés hasta la 2.15.17, y eso dejaba la funcionalidad entera en
// `sin_herramienta` en los dos equipos Linux reales donde se midió: ninguno traía
// las herramientas y ninguno podía instalarlas —uno por un middlebox que rompe
// TLS, el otro por los repositorios bloqueados—. El encabezado de
// `webcam_v4l2_linux.go` cuenta por qué instalarlas desde el instalador tampoco
// era la salida.

// nodosVideoMax acota la búsqueda de cámaras en `/dev`.
//
// POR QUÉ SE ITERA Y NO SE ASUME `/dev/video0`: desde Linux 4.16 una sola cámara
// USB publica DOS nodos —el de captura y uno de METADATOS— y cuál queda primero
// depende del orden de enumeración. En muchos portátiles `/dev/video0` es la
// cámara y `/dev/video1` los metadatos; en otros es al revés. Medido en FAZOCAR
// el 2026-08-13: `/dev/video0` captura, `/dev/video1` es metadatos, y el nodo de
// metadatos se reconoce por sus capacidades (`esNodoDeCaptura`), no adivinando.
const nodosVideoMax = 8

// herramientasWebcam son los programas externos que sabemos manejar, en orden de
// preferencia. Desde la enmienda de W-030-F2-06 son RESPALDO: sólo se prueban si
// la captura nativa no dio imagen, y el agente ya no depende de que estén.
//
// `fswebcam` va primero porque está hecho exactamente para esto: su `-S` descarta
// cuadros de calentamiento, que es el problema central de capturar una foto de
// una webcam (ver `cuadrosDeCalentamiento`). `ffmpeg` es el respaldo ubicuo y
// consigue lo mismo pidiendo N cuadros con `-update 1`, que reescribe el mismo
// archivo cuadro a cuadro y deja el ÚLTIMO.
//
// `%d` se sustituye por el nodo de video y `%s` por la ruta destino. Ningún
// argumento viene del operador ni del servidor.
var herramientasWebcam = []struct {
	bin  string
	args []string
	// ocupada son fragmentos que la herramienta imprime cuando el dispositivo
	// existe pero lo tiene tomado otro programa. Distinguirlo importa: "alguien
	// está usando la cámara" es información del caso, "no hay cámara" es otra
	// cosa.
	ocupada []string
}{
	{
		bin:     "fswebcam",
		args:    []string{"-d", "%d", "-S", strconv.Itoa(cuadrosDeCalentamiento), "--no-banner", "-q", "%s"},
		ocupada: []string{"Device or resource busy", "VIDIOC_STREAMON"},
	},
	{
		bin: "ffmpeg",
		args: []string{
			"-hide_banner", "-loglevel", "error",
			"-f", "video4linux2", "-i", "%d",
			"-frames:v", strconv.Itoa(cuadrosDeCalentamiento),
			"-update", "1", "-y", "%s",
		},
		ocupada: []string{"Device or resource busy", "Resource busy"},
	},
}

func (a *Agent) capturarWebcam(nombre string) WebcamShot {
	nodos := nodosDeVideo()
	if len(nodos) == 0 {
		a.Logger.Debugln("capturarWebcam: no hay ningún /dev/video*")
		return WebcamShot{Reason: webcamNoDevice}
	}

	destino := destinoFotoWebcam(nombre)

	// Primer intento: V4L2 nativo, que no necesita nada instalado.
	shot, motivo, capturaVista := a.capturarWebcamNativo(nodos, destino)
	if shot.OK() {
		return shot
	}

	// Respaldo: una herramienta externa, si es que hay alguna. Puede rescatar la
	// foto de una cámara con un formato que la ruta nativa no sabe leer.
	if respaldo, motivoRespaldo, intentado := a.capturarWebcamConHerramienta(nodos, destino); intentado {
		if respaldo.OK() {
			return respaldo
		}
		if motivo == "" {
			motivo = motivoRespaldo
		}
	}

	if motivo != "" {
		return WebcamShot{Reason: motivo}
	}
	if !capturaVista {
		// Todos los nodos eran de metadatos o no respondieron a QUERYCAP: en este
		// equipo no hay ninguna cámara que dé imágenes, que es un dato del caso y
		// no una avería. Es lo que reportó `DESKTOP-Q11L59A` con razón.
		return WebcamShot{Reason: webcamNoDevice}
	}
	return WebcamShot{Reason: webcamFailed}
}

// capturarWebcamNativo recorre los nodos con la ruta V4L2. Devuelve la foto, el
// motivo más preciso que se haya visto, y si algún nodo era de captura de verdad.
func (a *Agent) capturarWebcamNativo(nodos []string, destino string) (WebcamShot, string, bool) {
	motivo := ""
	capturaVista := false

	for _, nodo := range nodos {
		datos, err := capturarV4L2(nodo)
		if err != nil {
			if errors.Is(err, errNodoNoEsDeCaptura) {
				a.Logger.Debugln("capturarWebcam:", nodo, "no es un nodo de captura (metadatos), se salta")
				continue
			}
			capturaVista = true
			if errors.Is(err, unix.EBUSY) {
				a.Logger.Infoln("capturarWebcam:", nodo, "está tomado por otro programa")
				motivo = webcamBusy
				continue
			}
			a.Logger.Debugln("capturarWebcam: V4L2 falló en", nodo, ":", err)
			continue
		}
		capturaVista = true

		// 0600 desde que el archivo existe, no después: mientras espera la subida
		// es la foto de la CARA de una persona.
		if err := os.WriteFile(destino, datos, 0600); err != nil {
			a.Logger.Debugln("capturarWebcam: no se pudo escribir la foto en", destino, ":", err)
			_ = os.Remove(destino)
			continue
		}
		a.Logger.Debugln("capturarWebcam: foto nativa de", nodo, "-", len(datos), "bytes")
		return WebcamShot{Path: destino}, "", true
	}

	return WebcamShot{}, motivo, capturaVista
}

// capturarWebcamConHerramienta es el camino de respaldo. `intentado` es falso
// cuando no hay ninguna herramienta instalada, que desde esta enmienda ya no es
// un fallo: es lo normal en un equipo Linux recién instalado.
func (a *Agent) capturarWebcamConHerramienta(nodos []string, destino string) (WebcamShot, string, bool) {
	bin, args, ocupada, ok := elegirHerramientaWebcam()
	if !ok {
		a.Logger.Debugln("capturarWebcam: no hay herramienta externa de cámara instalada (se buscó fswebcam, ffmpeg); la captura nativa ya se intentó")
		return WebcamShot{}, "", false
	}

	motivo := ""
	for _, nodo := range nodos {
		// Con plazo: una cámara que no responde no puede dejar clavado el ciclo.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		finales := make([]string, 0, len(args))
		for _, arg := range args {
			arg = strings.ReplaceAll(arg, "%d", nodo)
			finales = append(finales, strings.ReplaceAll(arg, "%s", destino))
		}

		salida, err := exec.CommandContext(ctx, bin, finales...).CombinedOutput()
		cancel()

		texto := strings.TrimSpace(string(salida))
		if err != nil {
			if contieneAlguno(texto, ocupada) {
				a.Logger.Infoln("capturarWebcam:", nodo, "está tomado por otro programa")
				motivo = webcamBusy
			} else {
				a.Logger.Debugln("capturarWebcam:", bin, "falló en", nodo, ":", err, "salida:", texto)
			}
			_ = os.Remove(destino)
			continue
		}

		// El `exit 0` NO prueba el efecto, igual que en la captura de pantalla:
		// lo que prueba la foto es que exista un archivo con contenido. Medido en
		// terreno el 2026-08-13: `fswebcam` dice `fopen: No such file or
		// directory` y sale con código 0 igual.
		info, err := os.Stat(destino)
		if err != nil || info.Size() == 0 {
			a.Logger.Debugln("capturarWebcam:", bin, "salió con 0 pero no dejó archivo utilizable en", nodo)
			_ = os.Remove(destino)
			continue
		}

		return WebcamShot{Path: destino}, "", true
	}

	return WebcamShot{}, motivo, true
}

// nodosDeVideo lista los `/dev/video*` existentes, ordenados por número.
//
// Se ordena numéricamente y no alfabéticamente: con más de diez nodos, el orden
// de texto pone `video10` antes que `video2` y la cámara real quedaría al final
// de la cola de intentos.
func nodosDeVideo() []string {
	nodos := make([]string, 0, 4)
	for i := 0; i < nodosVideoMax; i++ {
		ruta := "/dev/video" + strconv.Itoa(i)
		if info, err := os.Stat(ruta); err == nil && !info.IsDir() {
			nodos = append(nodos, ruta)
		}
	}
	sort.Slice(nodos, func(i, j int) bool { return numeroDeNodo(nodos[i]) < numeroDeNodo(nodos[j]) })
	return nodos
}

func numeroDeNodo(ruta string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(ruta), "video"))
	if err != nil {
		return 1 << 30
	}
	return n
}

// elegirHerramientaWebcam devuelve el primer capturador disponible, con su ruta
// absoluta ya resuelta (mismo criterio que `elegirHerramientaCaptura`: el PATH
// del servicio lo fija systemd y viene recortado).
func elegirHerramientaWebcam() (string, []string, []string, bool) {
	for _, h := range herramientasWebcam {
		if ruta, ok := buscarBinario(h.bin); ok {
			return ruta, h.args, h.ocupada, true
		}
	}
	return "", nil, nil, false
}

func contieneAlguno(texto string, fragmentos []string) bool {
	for _, f := range fragmentos {
		if strings.Contains(texto, f) {
			return true
		}
	}
	return false
}
