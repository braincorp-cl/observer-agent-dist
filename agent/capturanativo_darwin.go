//go:build darwin && cgo

package agent

/*
#cgo LDFLAGS: -framework AVFoundation -framework CoreImage -framework CoreMedia -framework CoreVideo -framework CoreGraphics -framework Foundation
#include <stdlib.h>
#include "captura_darwin.h"
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
// Aqua (ver capturaaqua.go). Acá no hay lógica: hay una tabla, y existe como
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

// estadoTCCNativo consulta el permiso SIN efectos secundarios.
//
// Corre DENTRO del ayudante, y eso es lo que lo hace válido: el sujeto de TCC es
// el bundle, así que lo que responde acá es exactamente lo que le va a responder
// al camino por el que se saca la evidencia. Preguntado desde el servicio, la
// respuesta sería la del servicio, que ya no es quien captura.
func estadoTCCNativo(servicio string) int {
	if servicio == tccServicioPantalla {
		// La pantalla no tiene escala: `CGPreflightScreenCaptureAccess` es un
		// booleano. Se mapea a los dos únicos valores que la escalada distingue.
		if int(C.observer_pantalla_preflight()) == 1 {
			return tccEstadoConcedido
		}
		return tccEstadoNoDeterminado
	}
	return int(C.observer_camara_estado())
}

// pedirPermisoTCCNativo dispara el diálogo del sistema y devuelve el estado
// resultante.
//
// 🚫 Sólo lo llama la ronda de T016b/T016c, que tiene las guardias: nunca con un
// ciclo de modo perdido activo, nunca sin sesión de consola, y con tope de
// rondas por binario.
func pedirPermisoTCCNativo(servicio string, plazo time.Duration) int {
	if servicio == tccServicioPantalla {
		if int(C.observer_pantalla_pedir()) == 1 {
			return tccEstadoConcedido
		}
		return tccEstadoNoDeterminado
	}

	estado, err := tccPedirCamaraNativo(plazo)
	if err != nil {
		return tccEstadoNoDeterminado
	}
	return estado
}

// pantallaPreflightNativo es el atajo que usa la captura.
//
// Ante un fallo de consulta devuelve `true` y deja que la captura lo intente: el
// resultado se verifica igual por el lado de la imagen (una captura sin permiso
// sale negra y `CaptureScreen` la descarta), así que equivocarse hacia el
// intento cuesta un archivo de más, y equivocarse hacia la negativa costaría
// TODA la evidencia de un caso real por un fallo de la consulta.
func pantallaPreflightNativo() bool {
	return int(C.observer_pantalla_preflight()) == 1
}
