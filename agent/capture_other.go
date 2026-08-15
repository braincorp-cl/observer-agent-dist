//go:build !windows && !darwin
// +build !windows,!darwin

package agent

import "fmt"

// Feature 030 · Fase 1 · el modo `-m screencapture` en el resto de los sistemas.
//
// En Windows la captura la hace un proceso hijo lanzado en la sesión del usuario
// (capture_windows.go) y en macOS el ayudante Aqua (capture_run_darwin.go). En
// Linux el que corre en la sesión del usuario es la herramienta del sistema
// —scrot y compañía— y el agente nunca se re-ejecuta, así que este modo no tiene
// nada que hacer.
//
// Existe igual, y contesta con el mismo contrato de una línea, porque el
// despacho de modos de main.go es portable: sin esta función el agente no
// compilaría fuera de Windows.
func (a *Agent) RunScreenCapture(nombre string) {
	fmt.Println(prefijoCapturaFallo + captureUnsupported)
}
