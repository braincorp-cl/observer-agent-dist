//go:build !windows && !linux && !darwin

package agent

// No-dormir REACTIVA (por transición modo perdido / salida de geocerca) en los SO
// que aún no la implementan. Con Windows (T007, `lostmode_power_windows.go`),
// Linux (T011) y macOS (T010) ya cubiertos —Linux/macOS comparten el esqueleto
// unix (`lostmode_power_unix.go`) y sólo difieren en el lanzador (`systemd-inhibit`
// vs `caffeinate`)—, este build tag ya no cubre ningún SO de la flota real: es la
// red de seguridad para cualquier GOOS futuro (p. ej. freebsd), donde la
// no-dormir reactiva es un **no-op honesto** que no finge aplicar/revertir. El
// resto de la cascada (bloqueo diferido, override de webcam, geo intensiva y la
// alarma) opera igual.
//
// ⚠️ No confundir con el BASELINE keep-awake persistente (RN-08/RF-12), que es
// otra capa (config del sistema, no un proceso sostenido) y tiene su propia
// implementación por-SO.

func (a *Agent) aplicarNoHibernacion()  {}
func (a *Agent) revertirNoHibernacion() {}
