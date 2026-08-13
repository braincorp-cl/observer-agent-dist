//go:build linux
// +build linux

package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Feature 030 · Fase 2 · foto de webcam en Linux (T015).
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
// El archivo cae en el directorio del propio agente y no en el del usuario,
// justamente porque lo escribe root: es más cerrado, no menos.

// nodosVideo son las rutas donde puede vivir una cámara. Se prueban en orden.
//
// POR QUÉ SE ITERA Y NO SE ASUME `/dev/video0`: desde Linux 4.16 una sola cámara
// USB publica DOS nodos —el de captura y uno de METADATOS— y cuál queda primero
// depende del orden de enumeración. En muchos portátiles `/dev/video0` es la
// cámara y `/dev/video1` los metadatos; en otros es al revés. Pedirle una imagen
// al nodo de metadatos no da error de "no existe": da un fallo de formato, y
// codificarlo como "este equipo no tiene cámara" sería falso.
const nodosVideoMax = 8

// herramientasWebcam son los programas que sabemos manejar, en orden de
// preferencia.
//
// `fswebcam` va primero porque está hecho exactamente para esto: su `-S`
// descarta cuadros de calentamiento, que es el problema central de capturar una
// foto de una webcam (ver `cuadrosDeCalentamiento`). `ffmpeg` es el respaldo
// ubicuo y consigue lo mismo pidiendo N cuadros con `-update 1`, que reescribe
// el mismo archivo cuadro a cuadro y deja el ÚLTIMO.
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

	bin, args, ocupada, ok := elegirHerramientaWebcam()
	if !ok {
		a.Logger.Infoln("capturarWebcam: no hay ninguna herramienta de cámara instalada (se buscó fswebcam, ffmpeg)")
		return WebcamShot{Reason: webcamNoTool}
	}

	destino := filepath.Join(a.ProgramDir, nombre)

	// El último motivo que se vio, para no degradar a `fallo_captura` cuando
	// alguno de los nodos dijo algo más preciso ("la cámara está ocupada").
	motivo := webcamFailed

	for _, nodo := range nodos {
		// Con plazo: una cámara que no responde no puede dejar clavado el ciclo.
		// 30 s es holgado — con el calentamiento incluido, una foto tarda ~1 s.
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
		// lo que prueba la foto es que exista un archivo con contenido.
		info, err := os.Stat(destino)
		if err != nil || info.Size() == 0 {
			a.Logger.Debugln("capturarWebcam:", bin, "salió con 0 pero no dejó archivo utilizable en", nodo)
			_ = os.Remove(destino)
			continue
		}

		return WebcamShot{Path: destino}
	}

	return WebcamShot{Reason: motivo}
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
