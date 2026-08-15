//go:build darwin
// +build darwin

package agent

import (
	"fmt"
	"time"
)

// Feature 030 · Fase 2 · `-m webcamcapture` en macOS (T016).
//
// NO lo usa el ciclo del modo perdido, que llama a `CaptureWebcam` directo.
// Existe por la misma razón que en Windows y en Linux: poder MEDIR la cámara de
// un equipo real sin marcarlo como perdido — marcarlo subiría además capturas de
// su pantalla y su ubicación, y dejaría un caso abierto en la consola por una
// prueba.
//
// 🔑 Y ACÁ VALE EL DOBLE, porque en macOS el camino tiene una pieza que en las
// otras plataformas no existe: el ayudante Aqua. Este modo pasa por él, igual
// que producción, así que lo que se mide con `rawcmd -m webcamcapture` es
// exactamente lo que va a hacer un ciclo real — incluido el despertar del
// LaunchAgent, que es justo la parte nueva y la que puede faltar en un equipo
// actualizado. Un modo que tocara la cámara directamente daría verde acá y rojo
// en producción.
//
// 🚫 No confundir con `-m capturaaqua`, que es el otro extremo del camino y lo
// invoca launchd, nunca una persona.
//
// Imprime una sola línea (OK <ruta> / FAIL <motivo>), igual que en los otros SO.
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
