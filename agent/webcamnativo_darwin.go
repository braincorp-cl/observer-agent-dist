//go:build darwin && cgo

package agent

/*
#cgo LDFLAGS: -framework AVFoundation -framework CoreImage -framework CoreMedia -framework CoreVideo -framework CoreGraphics -framework Foundation
#include <stdlib.h>
#include "webcam_darwin.h"
*/
import "C"

import (
	"time"
	"unsafe"
)

// Feature 030 · Fase 2 · T016 — traducción de los códigos de AVFoundation a los
// motivos del producto.
//
// La implementación está en webcam_darwin.m y sólo corre dentro del ayudante
// Aqua (ver webcamaqua.go). Acá no hay lógica: hay una tabla, y existe como
// tabla para que el día que alguien agregue un código en el .m tenga que venir a
// decidir qué le muestra a quien está siguiendo un caso.

// capturarWebcamNativo devuelve ("", "") si la foto quedó escrita; si no, el
// motivo que va a la consola y el detalle crudo que va al log del servicio.
func capturarWebcamNativo(destino string, descarte int, plazo time.Duration) (string, string) {
	cDestino := C.CString(destino)
	defer C.free(unsafe.Pointer(cDestino))

	// El buffer es memoria de Go y se pasa a C: legal porque
	// `observer_webcam_capturar` escribe en él de forma síncrona y no se lo
	// queda. El texto lo pone AVFoundation y puede venir en el idioma del
	// equipo, así que es para leer, no para comparar.
	detalle := make([]byte, 256)
	rc := C.observer_webcam_capturar(
		cDestino,
		C.int(descarte),
		C.double(plazo.Seconds()),
		(*C.char)(unsafe.Pointer(&detalle[0])),
		C.size_t(len(detalle)),
	)

	texto := C.GoString((*C.char)(unsafe.Pointer(&detalle[0])))

	switch rc {
	case C.OBSERVER_CAM_OK:
		return "", ""
	case C.OBSERVER_CAM_SIN_CAMARA:
		return webcamNoDevice, texto
	case C.OBSERVER_CAM_DENEGADO:
		return webcamDenied, texto
	case C.OBSERVER_CAM_OCUPADA:
		return webcamBusy, texto
	case C.OBSERVER_CAM_SIN_CUADROS:
		// La sesión corrió y no entregó ni un cuadro. Es el motivo honesto: no
		// es que la cámara diera una imagen negra —eso lo dice
		// `camara_sin_imagen` después de mirar los píxeles—, es que no dio nada.
		// En terreno esta es la firma exacta de haber corrido fuera de Aqua.
		return webcamFailed, "la sesion de captura no entrego ningun cuadro"
	default:
		return webcamFailed, texto
	}
}
