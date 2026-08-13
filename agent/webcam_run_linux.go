//go:build linux
// +build linux

package agent

import (
	"fmt"
	"time"
)

// Feature 030 · Fase 2 · `-m webcamcapture` en Linux (enmienda de W-030-F2-06).
//
// NO lo usa el ciclo del modo perdido, que saca la foto dentro del propio
// servicio. Existe por la misma razón que en Windows: para poder MEDIR la cámara
// de un equipo real sin marcarlo como perdido — marcarlo subiría además capturas
// de su pantalla y su ubicación, y dejaría un caso abierto en la consola por una
// prueba.
//
// Hasta esta enmienda Linux caía en `webcam_other.go` y este modo respondía
// `so_no_soportado`, así que la única forma de probar la cámara de un Linux era
// compilar un binario aparte. Ahora que la captura es nativa, el mismo binario
// que se instala sirve de instrumento, y lo que se mide es exactamente lo que
// corre en producción.
//
// Imprime una sola línea (OK <ruta> / FAIL <motivo>), igual que `-m screencapture`.
func (a *Agent) RunWebcamCapture(nombre string) {
	if nombre == "" {
		nombre = webcamFilename(time.Now())
	}

	shot := a.CaptureWebcam()
	if !shot.OK() {
		fmt.Println(prefijoCapturaFallo + shot.Reason)
		return
	}
	fmt.Println(prefijoCapturaOK + shot.Path)
}
