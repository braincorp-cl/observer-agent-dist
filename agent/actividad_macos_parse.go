package agent

// Parsers de la primitiva de actividad macOS. Viven sin build tag para que sus
// pruebas corran en cualquier SO: la MacBook real es la única que da la salida.

import (
	"path/filepath"
	"regexp"
	"strings"
)

// `lsappinfo info -only executablepath` cambió de formato: hasta macOS 15 daba
// `"LSExecutablePath"="/ruta"`; en macOS 27 ignora `-only` y devuelve el bloque
// completo con `executable path="/ruta"`. Se aceptan ambas.
var reLsappinfoExe = regexp.MustCompile(`(?:"LSExecutablePath"|executable path)\s*=\s*"([^"\n]+)"`)

// procesoDesdeLsappinfo devuelve el ejecutable en minúsculas, o "" si no hay ruta.
func procesoDesdeLsappinfo(out string) string {
	m := reLsappinfoExe.FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	return strings.ToLower(filepath.Base(strings.TrimSpace(m[1])))
}

// bloqueadoDesdeIoreg lee `ioreg -n Root -d1`: la clave CGSSessionScreenIsLocked
// sólo aparece en la sesión con la pantalla bloqueada, y cuenta la que está en
// la consola (con cambio rápido de usuario puede haber varias). No requiere TCC.
func bloqueadoDesdeIoreg(out string) bool {
	i := strings.Index(out, `"IOConsoleUsers"`)
	if i < 0 {
		return false
	}
	linea := out[i:]
	if j := strings.IndexByte(linea, '\n'); j >= 0 {
		linea = linea[:j]
	}
	for _, sesion := range strings.Split(linea, "},{") {
		if strings.Contains(sesion, `"kCGSSessionOnConsoleKey"=Yes`) {
			return strings.Contains(sesion, `"CGSSessionScreenIsLocked"=Yes`)
		}
	}
	return false
}
