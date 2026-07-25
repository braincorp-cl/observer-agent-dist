//go:build !darwin || !cgo

package agent

import "fmt"

// Feature 025 · stub del modo `-m geoprobe`.
//
// El modo existe en el CLI para todas las plataformas (main.go no tiene build
// tags), pero solo hace algo en macOS con cgo, donde el daemon se re-ejecuta en
// el contexto del usuario para consultar CoreLocation. En el resto imprime el
// mismo "sin fix" que el padre ya sabe interpretar.
func (a *Agent) RunGeoProbe() {
	fmt.Println("NOFIX")
}
