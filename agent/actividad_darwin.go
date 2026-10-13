//go:build darwin
// +build darwin

package agent

// Primitiva de actividad en macOS. Usa binarios del SISTEMA (siempre presentes, no
// instalables: no es el problema de egress de Linux):
//   - Proceso activo: `lsappinfo front` + `lsappinfo info -only executablepath`.
//   - Inactividad: `ioreg -c IOHIDSystem` → HIDIdleTime (nanosegundos).
//   - Bloqueo: `ioreg -n Root -d1` → CGSSessionScreenIsLocked de la sesión de consola.
// El TÍTULO de la ventana exige Accesibilidad (TCC) y se omite con una nota.

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	rmm "github.com/braincorp-cl/observer-agent-dist/shared"
)

var reIdleNs = regexp.MustCompile(`"HIDIdleTime"\s*=\s*(\d+)`)

func idleSegundosDarwin() int {
	out, err := exec.Command("ioreg", "-c", "IOHIDSystem").Output()
	if err != nil {
		return -1
	}
	m := reIdleNs.FindSubmatch(out)
	if m == nil {
		return -1
	}
	ns, err := strconv.ParseInt(string(m[1]), 10, 64)
	if err != nil {
		return -1
	}
	return int(ns / 1_000_000_000)
}

func procesoActivoDarwin() string {
	asn, err := exec.Command("lsappinfo", "front").Output()
	id := strings.TrimSpace(string(asn))
	if err != nil || id == "" || id == "[ NULL ]" {
		return ""
	}
	out, err := exec.Command("lsappinfo", "info", "-only", "executablepath", id).Output()
	if err != nil {
		return ""
	}
	return procesoDesdeLsappinfo(string(out))
}

// pantallaBloqueadaDarwin: sesión de consola con la pantalla bloqueada (sin TCC).
func pantallaBloqueadaDarwin() bool {
	out, err := exec.Command("ioreg", "-n", "Root", "-d1").Output()
	if err != nil {
		return false
	}
	return bloqueadoDesdeIoreg(string(out))
}

func muestraActividadImpl(conTitulo bool) rmm.ActividadMuestra {
	m := rmm.ActividadMuestra{SO: "macos", Sesion: "aqua", IdleSeg: idleSegundosDarwin(), Bloqueado: pantallaBloqueadaDarwin()}
	if p := procesoActivoDarwin(); p != "" {
		m.Proceso = p
	} else {
		m.Proceso = "(desconocido)"
	}
	if conTitulo {
		// El título requiere Accesibilidad (TCC); se omite en 049-A.
		m.Nota = "macos: título de ventana requiere permiso de Accesibilidad (TCC)"
	}
	return m
}
