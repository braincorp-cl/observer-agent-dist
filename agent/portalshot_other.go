//go:build !linux
// +build !linux

package agent

import "fmt"

// Feature 030 · Fase 3 · T021. El portal de Wayland sólo existe en Linux; el
// resto de los sistemas necesita el símbolo porque el despacho de modos de
// main.go es portable. Mismo patrón que capture_other.go.
func (a *Agent) RunPortalScreenshot(destino string) {
	fmt.Println(prefijoCapturaFallo + captureUnsupported)
}
