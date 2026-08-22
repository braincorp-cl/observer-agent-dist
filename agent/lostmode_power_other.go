//go:build !windows && !linux

package agent

// Feature 038 · no-hibernación en los SO que aún no la implementan.
//
// Windows (T007, `lostmode_power_windows.go`) y Linux (T011,
// `lostmode_power_linux.go`) ya tienen no-suspensión reversible. macOS
// (`caffeinate`/`pmset`) es T010 y sigue pendiente, así que acá son no-ops: el
// resto de la cascada —bloqueo diferido, override de webcam, geo intensiva y la
// alarma— sí opera igual en macOS.

func (a *Agent) aplicarNoHibernacion()  {}
func (a *Agent) revertirNoHibernacion() {}
