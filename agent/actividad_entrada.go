package agent

// Feature 049-E · presencia humana: conteo de teclas y eventos de mouse.
//
// Sólo se CUENTA: ni el código de tecla ni el texto salen jamás de la función
// que recibe el evento. Sirve para distinguir a una persona frente al equipo de
// un equipo encendido, con sesión iniciada y sin uso. Cada SO da un contador
// ACUMULADO (que puede reiniciarse: reinicio, cierre de sesión, relanzamiento
// del ayudante) y el ciclo suma la diferencia entre muestras.
//   - macOS: contadores de CoreGraphics leídos en la propia muestra.
//   - Linux: el servicio (root) lee /dev/input (X11 y Wayland por igual).
//   - Windows: un ayudante en la sesión del usuario con hooks de bajo nivel que
//     descartan la entrada inyectada por software (control remoto, simuladores).

import (
	"strconv"
	"strings"
	"sync/atomic"

	rmm "github.com/braincorp-cl/observer-agent-dist/shared"
)

var (
	entradaTeclas atomic.Uint64
	entradaMouse  atomic.Uint64
	entradaListo  atomic.Bool
)

// entradaAcumulada da el contador vigente: el de la muestra si el SO lo mide
// ahí, si no el que mantiene el servicio.
func entradaAcumulada(m rmm.ActividadMuestra) (teclas, mouse uint64, ok bool) {
	if m.EntradaAcum {
		return m.TeclasAcum, m.MouseAcum, true
	}
	if !entradaListo.Load() {
		return 0, 0, false
	}
	return entradaTeclas.Load(), entradaMouse.Load(), true
}

// deltaContador: diferencia entre lecturas de un contador que puede reiniciarse.
func deltaContador(prev, cur uint64) int {
	if cur >= prev {
		return int(cur - prev)
	}
	return int(cur) // se reinició: lo leído es lo ocurrido desde el reinicio
}

// lectorEntrada guarda la lectura anterior para sumar sólo lo nuevo.
type lectorEntrada struct {
	teclas, mouse uint64
	ok            bool
}

func (l *lectorEntrada) delta(teclas, mouse uint64, ok bool) (int, int) {
	if !ok {
		l.ok = false
		return 0, 0
	}
	if !l.ok {
		l.teclas, l.mouse, l.ok = teclas, mouse, true
		return 0, 0
	}
	dt, dm := deltaContador(l.teclas, teclas), deltaContador(l.mouse, mouse)
	l.teclas, l.mouse = teclas, mouse
	return dt, dm
}

// parsearLineaEntrada lee "teclas mouse" del ayudante.
func parsearLineaEntrada(l string) (uint64, uint64, bool) {
	c := strings.Fields(l)
	if len(c) != 2 {
		return 0, 0, false
	}
	t, e1 := strconv.ParseUint(c[0], 10, 64)
	m, e2 := strconv.ParseUint(c[1], 10, 64)
	return t, m, e1 == nil && e2 == nil
}

// Eventos de Linux (input-event-codes.h). Aquí y no en el archivo de Linux
// para que la clasificación se pruebe en los tres SO.
const (
	evKey = 0x01
	evRel = 0x02
	evAbs = 0x03

	btnMisc  = 0x100 // códigos < BTN_MISC son teclas
	btnMouse = 0x110 // BTN_LEFT..BTN_TASK
	btnFinal = 0x118
	relX     = 0x00
	relWheel = 0x08
	absX     = 0x00
)

// clasificarEvento: 1 = tecla, 2 = mouse, 0 = no cuenta.
func clasificarEvento(tipo, codigo uint16, valor int32) int {
	switch tipo {
	case evKey:
		if valor != 1 { // sólo la pulsación, no la repetición ni la liberación
			return 0
		}
		if codigo < btnMisc {
			return 1
		}
		if codigo >= btnMouse && codigo < btnFinal {
			return 2
		}
	case evRel:
		if codigo == relX || codigo == relWheel {
			return 2
		}
	case evAbs:
		if codigo == absX {
			return 2
		}
	}
	return 0
}
