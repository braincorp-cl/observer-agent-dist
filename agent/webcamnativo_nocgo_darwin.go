//go:build darwin && !cgo

package agent

import "time"

// Feature 030 · Fase 2 · T016 — stub macOS sin cgo.
//
// macOS no trae herramienta de línea de comandos para fotografiar, así que sin
// cgo no hay forma de tomar la foto: no es que falte una dependencia, es que no
// existe el programa. Un binario `CGO_ENABLED=0` es sólo un camino de
// desarrollo —el release de macOS lleva cgo desde la feature 025, porque
// CoreLocation lo exige— y acá se dice que no se puede en vez de fingir un
// intento y devolver un archivo vacío.
func capturarWebcamNativo(destino string, descarte int, plazo time.Duration) (string, string) {
	return webcamUnsupported, "binario compilado sin cgo"
}
