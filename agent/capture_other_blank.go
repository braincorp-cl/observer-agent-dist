//go:build !windows && !linux && !darwin
// +build !windows,!linux,!darwin

package agent

// motivoDePantallaNegra en una plataforma sin capturador: nunca se llega acá
// (no hay imagen que verificar), pero la función tiene que existir para que el
// archivo portable compile.
func (a *Agent) motivoDePantallaNegra() string {
	return captureBlank
}

func (a *Agent) usuarioDeLaSesionGrafica() string { return "" }
