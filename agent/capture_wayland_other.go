//go:build !linux
// +build !linux

package agent

// Feature 030 · Fase 3 · T021. El enrolamiento del portal de Wayland sólo
// existe en Linux; el resto de los sistemas necesitan el símbolo porque la
// llamada vive en svc.go, que es común a los tres.
//
// Mismo patrón que tccpermisos_other.go, y por la misma razón: la alternativa
// sería un `if runtime.GOOS ==` en el arranque del servicio, que compila la
// rama muerta en todas las plataformas y obliga a leer svc.go para saber qué
// corre dónde.

func (a *Agent) RondaPortalWayland(forzar bool) {}

func (a *Agent) LanzarRondaPortalWayland() {}
