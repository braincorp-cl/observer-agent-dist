//go:build !windows && !linux

package agent

// No-dormir REACTIVA (por transición modo perdido / salida de geocerca) en los SO
// que aún no la implementan. Build tag !windows && !linux ⇒ hoy cubre macOS.
//
// Windows (T007, `lostmode_power_windows.go`) y Linux (T011,
// `lostmode_power_linux.go`) ya tienen no-suspensión reactiva reversible. En macOS
// es un **no-op honesto y documentado** (feature 041 · RF-10): la cobertura del
// paquete reactivo es Windows/Linux ahora, macOS diferido a T010 de la 038
// (`caffeinate`/`pmset` por transición). Hasta entonces estas funciones no hacen
// nada a propósito —no fingen aplicar/revertir— y el resto de la cascada (bloqueo
// diferido, override de webcam, geo intensiva y la alarma) sí opera igual en macOS.
//
// ⚠️ No confundir con el BASELINE keep-awake persistente (RN-08/RF-12), que en
// macOS SÍ actúa vía `pmset` (T029, `baseline_darwin.go`): el baseline es un
// prerrequisito de la medición y no depende de un caso abierto, mientras que esta
// no-dormir reactiva es efímera (mientras dura la transición). Que el reactivo sea
// no-op en macOS NO deja al Mac medible-solo-si-despierto: de eso se encarga el
// baseline, que reconcilia por poll con independencia de este archivo.

func (a *Agent) aplicarNoHibernacion()  {}
func (a *Agent) revertirNoHibernacion() {}
