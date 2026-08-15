//go:build !windows && !linux && !darwin
// +build !windows,!linux,!darwin

package agent

import "fmt"

// Feature 030 · Fase 2 · el modo `-m webcamcapture` tiene trabajo en los tres
// sistemas donde el propio agente saca la foto: Windows (Media Foundation,
// T014), Linux (V4L2 nativo, enmienda de W-030-F2-06) y macOS (AVFoundation por
// el ayudante Aqua, T016).
//
// Este archivo es lo que queda: que el despacho de modos de main.go —que es
// portable— siga compilando en cualquier otro sistema. Mismo motivo y mismo
// contrato de una línea que `capture_other.go`.
//
// 🪤 macOS estuvo acá hasta T016, y por eso la única forma de probar la cámara
// de un Mac era compilar un binario aparte. Es el mismo agujero que tenía Linux
// y se cerró por la misma razón: lo que se mide tiene que ser lo que corre.
func (a *Agent) RunWebcamCapture(nombre string) {
	fmt.Println(prefijoCapturaFallo + webcamUnsupported)
}
