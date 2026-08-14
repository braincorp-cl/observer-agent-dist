//go:build darwin && cgo

package agent

/*
#cgo LDFLAGS: -framework AVFoundation -framework Foundation
#include "tccpermisos_darwin.h"
*/
import "C"

import (
	"errors"
	"time"
)

// Feature 030 · Fase 2 · T016b — pedido nativo del permiso de Cámara.
//
// Ver tccpermisos_darwin.m para el porqué de que esto exista en Objective-C
// habiendo hecho todo lo demás con `osascript`: el bloque del completion
// handler.

var errTCCPlazoAgotado = errors.New("nadie contesto el dialogo dentro del plazo")

// tccPedirCamaraNativo dispara el diálogo y devuelve el estado resultante.
func tccPedirCamaraNativo(plazo time.Duration) (int, error) {
	estado := int(C.observer_tcc_pedir_camara(C.double(plazo.Seconds())))
	if estado == int(C.OBSERVER_TCC_TIMEOUT) {
		return 0, errTCCPlazoAgotado
	}
	return estado, nil
}
