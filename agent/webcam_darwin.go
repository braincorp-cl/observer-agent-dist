//go:build darwin
// +build darwin

package agent

// Feature 030 · Fase 2 · T016 — la foto de webcam en macOS.
//
// Acá no hay lógica de cámara: la saca el ayudante Aqua, y el porqué está en
// capturaaqua.go. Este archivo existe para que se vea, desde el mismo sitio
// donde vive la implementación de Windows y la de Linux, que macOS toma un
// camino distinto y cuál es.
//
// 🪤 LO QUE HABÍA ANTES Y POR QUÉ SE BORRÓ: una búsqueda de `imagesnap` o
// `ffmpeg` lanzada con `launchctl asuser`. Dos errores en uno. Las herramientas
// no vienen con el sistema —y en el Mac medido no estaban—, y aunque hubieran
// estado, `asuser` no da contexto Aqua: habrían devuelto cuadros vacíos sin
// error, que es peor que fallar.
func (a *Agent) capturarWebcam(nombre string) WebcamShot {
	ruta, motivo := a.capturarPorAyudante(capturaTipoWebcam, nombre)
	if motivo != "" {
		return WebcamShot{Reason: motivo}
	}
	return WebcamShot{Path: ruta}
}
