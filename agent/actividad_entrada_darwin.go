//go:build darwin

package agent

// 049-E · macOS: los contadores de teclas y mouse los entrega CoreGraphics en
// la propia muestra (ver actividad_entrada_cgo_darwin.go); el servicio no
// mantiene nada.

func (a *Agent) entradaIniciar() {}
func (a *Agent) entradaDetener() {}
