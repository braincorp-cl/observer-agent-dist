//go:build !linux && !darwin && !windows
// +build !linux,!darwin,!windows

package agent

// Feature 030 · Fase 2 · plataformas sin implementación de cámara.
//
// Existe para que el árbol compile fuera de los tres SO de la flota. Devuelve
// el motivo explícito en vez de un error genérico: "este sistema operativo no
// toma fotos" es un dato del caso, no una avería.
func (a *Agent) capturarWebcam(nombre string) WebcamShot {
	return WebcamShot{Reason: webcamUnsupported}
}
