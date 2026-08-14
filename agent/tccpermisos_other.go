//go:build !darwin
// +build !darwin

package agent

import "fmt"

// Feature 030 · Fase 2 · T016b/T016c — la gestión de permisos TCC es de macOS.
//
// Windows y Linux no tienen un TCC: en Windows el permiso de cámara del
// registro (ConsentStore) no gobierna a un servicio SYSTEM —medido, T014 lo
// sortea— y en Linux el acceso a /dev/video* es de grupo, no de consentimiento.
// Los stubs existen para que el arranque del servicio y la tabla de modos de
// main.go sean iguales en los tres sistemas.

func (a *Agent) RondaPermisosTCC(forzar bool) {}

func (a *Agent) LanzarRondaPermisosTCC() {}

func (a *Agent) marcarPedidoDePermisosTCC() {}

func (a *Agent) RunPedirPermisoTCC(servicio string) {
	fmt.Println("FAIL solo_macos")
}
