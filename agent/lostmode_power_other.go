//go:build !windows

package agent

// Feature 038 · no-hibernación en no-Windows.
//
// Fase A1 es sólo Windows (donde vive el caso del portátil que se duerme con la
// tapa cerrada). macOS (`caffeinate`/`pmset`) y Linux (`systemd-inhibit`) son
// Fase A2 (T010/T011). Hasta entonces estas son no-ops, igual que el stub de
// DisableSleepHibernate en install_unix.go: el resto de la cascada —bloqueo
// diferido, override de webcam, geo intensiva— sí opera en los tres SO.

func (a *Agent) aplicarNoHibernacion()  {}
func (a *Agent) revertirNoHibernacion() {}
