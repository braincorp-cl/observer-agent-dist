//go:build !windows && !linux
// +build !windows,!linux

package agent

import "fmt"

// Feature 030 · Fase 2 · el modo `-m webcamcapture` tiene trabajo donde el
// propio agente saca la foto: Windows (Media Foundation, T014) y Linux (V4L2
// nativo, enmienda de W-030-F2-06).
//
// En macOS la saca una herramienta del sistema y el agente nunca se re-ejecuta,
// así que acá sólo hace falta que el despacho de modos de main.go —que es
// portable— siga compilando. Mismo motivo y mismo contrato de una línea que
// `capture_other.go`.
func (a *Agent) RunWebcamCapture(nombre string) {
	fmt.Println(prefijoCapturaFallo + webcamUnsupported)
}
