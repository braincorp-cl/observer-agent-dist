//go:build darwin && !cgo

package agent

import (
	"errors"
	"time"
)

// Feature 030 · Fase 2 · T016b — stub macOS sin cgo.
//
// El diálogo de Cámara necesita un bloque de Objective-C y no hay forma de
// pedirlo sin cgo (ver tccpermisos_darwin.m). Un binario CGO_ENABLED=0 es sólo
// un camino de desarrollo —el release de macOS lleva cgo desde la feature 025,
// porque CoreLocation lo exige—, así que acá se dice que no se puede en vez de
// fingir un intento. El resto de la ronda (consultas y permiso de pantalla)
// funciona igual, porque va por `osascript`.
func tccPedirCamaraNativo(plazo time.Duration) (int, error) {
	return 0, errors.New("sin cgo: el dialogo de camara no se puede pedir")
}
