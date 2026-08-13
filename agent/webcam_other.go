//go:build !windows
// +build !windows

package agent

import "fmt"

// Feature 030 · Fase 2 · el modo `-m webcamcapture` sólo tiene trabajo en
// Windows, donde la foto la saca el propio agente por Media Foundation.
//
// En Linux y macOS la saca una herramienta del sistema y el agente nunca se
// re-ejecuta, así que acá sólo hace falta que el despacho de modos de main.go
// —que es portable— siga compilando. Mismo motivo y mismo contrato de una línea
// que `capture_other.go`.
func (a *Agent) RunWebcamCapture(nombre string) {
	fmt.Println(prefijoCapturaFallo + webcamUnsupported)
}
