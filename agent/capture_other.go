//go:build !windows
// +build !windows

package agent

import "fmt"

// Feature 030 · Fase 1 · el modo `-m screencapture` sólo existe en Windows.
//
// Ahí la captura la hace un proceso hijo lanzado en la sesión del usuario
// (capture_windows.go). En Linux y macOS el que corre en la sesión del usuario
// es la herramienta del sistema —scrot, screencapture— y el agente nunca se
// re-ejecuta, así que este modo no tiene nada que hacer.
//
// Existe igual, y contesta con el mismo contrato de una línea, porque el
// despacho de modos de main.go es portable: sin esta función el agente no
// compilaría fuera de Windows.
func (a *Agent) RunScreenCapture(nombre string) {
	fmt.Println(prefijoCapturaFallo + captureUnsupported)
}
