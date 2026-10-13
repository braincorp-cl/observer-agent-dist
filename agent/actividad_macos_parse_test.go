package agent

import "testing"

// Salidas reales de la MacBook de prueba (macOS 27.0.1, 12-oct-2026).
func TestProcesoDesdeLsappinfo(t *testing.T) {
	casos := map[string]string{
		// macOS 27: ignora -only y da el bloque completo.
		"[ NULL ]  ASN:0x0-0x218218: (in front) \n    bundleID=[ NULL ] \n    bundle path=[ NULL ] \n" +
			"    executable path=\"/usr/bin/osascript\"\n !cgsConnection !signalled type=[ NULL ]  Arch=!!none \n": "osascript",
		// Formato previo.
		`"LSExecutablePath"="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"`: "google chrome",
		"[ NULL ]": "",
		"":         "",
	}
	for in, want := range casos {
		if got := procesoDesdeLsappinfo(in); got != want {
			t.Errorf("procesoDesdeLsappinfo(%q) = %q; quiero %q", in, got, want)
		}
	}
}

func TestBloqueadoDesdeIoreg(t *testing.T) {
	libre := `      "IOConsoleUsers" = ({"kCGSSessionOnConsoleKey"=Yes,"kCGSSessionUserNameKey"="dsepulveda","kCGSSessionUserIDKey"=501})` + "\n"
	bloq := `      "IOConsoleUsers" = ({"kCGSSessionOnConsoleKey"=Yes,"CGSSessionScreenIsLocked"=Yes,"kCGSSessionUserIDKey"=501})` + "\n"
	// Otra sesión (fuera de consola) bloqueada no cuenta.
	otra := `      "IOConsoleUsers" = ({"kCGSSessionOnConsoleKey"=No,"CGSSessionScreenIsLocked"=Yes},{"kCGSSessionOnConsoleKey"=Yes,"kCGSSessionUserIDKey"=502})` + "\n"
	if bloqueadoDesdeIoreg(libre) || !bloqueadoDesdeIoreg(bloq) || bloqueadoDesdeIoreg(otra) || bloqueadoDesdeIoreg("") {
		t.Fatal("detección de bloqueo macOS incorrecta")
	}
}
