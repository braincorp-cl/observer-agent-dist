//go:build !linux && !windows && !darwin
// +build !linux,!windows,!darwin

package agent

// Stub para Windows y macOS: la primitiva nativa de actividad se implementa en
// 049-A (Windows: GetForegroundWindow + GetLastInputInfo; macOS: lsappinfo/NSWorkspace
// + ioreg HIDIdleTime). Mientras tanto devuelve una muestra marcada como no
// implementada para que el módulo compile en las tres plataformas.

import rmm "github.com/braincorp-cl/observer-agent-dist/shared"

func muestraActividadImpl(conTitulo bool) rmm.ActividadMuestra {
	_ = conTitulo
	return rmm.ActividadMuestra{SO: soActividad(), IdleSeg: -1, Nota: "primitiva de actividad no implementada en esta plataforma todavía"}
}
