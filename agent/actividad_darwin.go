//go:build darwin
// +build darwin

package agent

// Primitiva de actividad en macOS. Usa binarios del SISTEMA (siempre presentes, no
// instalables: no es el problema de egress de Linux):
//   - Proceso activo: `lsappinfo front` + `lsappinfo info -only executablepath`.
//   - Inactividad: `ioreg -c IOHIDSystem` → HIDIdleTime (nanosegundos).
// El TÍTULO de la ventana y el estado de bloqueo exigen permisos TCC (Accesibilidad)
// y se dejan para una iteración con perfil PPPC; en 049-A se omiten con una nota.

import (
	"os/exec"
	"path/filepath"
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
	// Formato: "LSExecutablePath"="/Applications/Foo.app/Contents/MacOS/Foo"
	s := string(out)
	i := strings.Index(s, "=")
	if i < 0 {
		return ""
	}
	ruta := strings.Trim(strings.TrimSpace(s[i+1:]), "\"")
	if ruta == "" {
		return ""
	}
	return strings.ToLower(filepath.Base(ruta))
}

func muestraActividadImpl(conTitulo bool) rmm.ActividadMuestra {
	m := rmm.ActividadMuestra{SO: "macos", Sesion: "aqua", IdleSeg: idleSegundosDarwin()}
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
