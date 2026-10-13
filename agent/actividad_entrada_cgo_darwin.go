//go:build darwin && cgo

package agent

/*
#cgo LDFLAGS: -framework CoreGraphics
#include <CoreGraphics/CoreGraphics.h>

static unsigned int contadorEvento(int tipo) {
	return (unsigned int)CGEventSourceCounterForEventType(kCGEventSourceStateHIDSystemState, (CGEventType)tipo);
}
*/
import "C"

// Tipos de CGEventType: kCGEventKeyDown=10; mouse: LeftMouseDown=1,
// RightMouseDown=3, MouseMoved=5, ScrollWheel=22, OtherMouseDown=25.
// El estado HIDSystem cuenta la entrada de hardware desde que inició la sesión
// gráfica; no requiere permisos de TCC. Sólo cuenta: no expone qué tecla fue.
func contadoresEntradaDarwin() (uint64, uint64, bool) {
	teclas := uint64(C.contadorEvento(10))
	mouse := uint64(C.contadorEvento(1)) + uint64(C.contadorEvento(3)) +
		uint64(C.contadorEvento(5)) + uint64(C.contadorEvento(22)) + uint64(C.contadorEvento(25))
	return teclas, mouse, true
}
