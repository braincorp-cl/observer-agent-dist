//go:build darwin
// +build darwin

package agent

import (
	"fmt"
	"time"
)

// Feature 030 · `-m screencapture` en macOS (T016).
//
// NO lo usa el ciclo del modo perdido, que llama a `CaptureScreen` directo.
// Existe para poder MEDIR la pantalla de un equipo real sin marcarlo como
// perdido — marcarlo subiría además su foto y su ubicación, y dejaría un caso
// abierto en la consola por una prueba.
//
// 🔑 Y en macOS vale el doble, por lo mismo que `-m webcamcapture`: el camino
// tiene una pieza que en las otras plataformas no existe —el ayudante Aqua, con
// su bundle, su registro en LaunchServices y su propio permiso de TCC— y este
// modo pasa por ella igual que producción. Un modo que llamara a
// `screencapture` directo daría verde acá con el permiso del servicio y rojo en
// un caso real.
//
// 🚫 No confundir con `-m capturaaqua`, que es el otro extremo del camino y lo
// invoca launchd, nunca una persona.
func (a *Agent) RunScreenCapture(nombre string) {
	if nombre == "" {
		nombre = screenshotFilename(time.Now())
	}

	shot := a.CaptureScreen()
	if !shot.OK() {
		fmt.Println(prefijoCapturaFallo + shot.Reason)
		return
	}
	fmt.Println(prefijoCapturaOK + shot.Path)
}
