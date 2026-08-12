//go:build darwin
// +build darwin

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Feature 030 · Fase 1 · captura de pantalla en macOS (T013).
//
// EL MURO, dicho desde el día uno (decisión 2 del jefe, 2026-07-28): en macOS
// ≥ 10.15 la captura de pantalla exige el permiso TCC "Grabación de pantalla",
// y ese permiso **ningún MDM lo puede conceder**. El perfil PPPC de Apple sólo
// sabe NEGAR pantalla, cámara y micrófono; concederlos requiere que una persona
// lo haga a mano en Ajustes del Sistema, frente al equipo. Lo que se midió en
// la 025 con CoreLocation vale igual acá.
//
// Consecuencia práctica: en una flota Mac administrada, esta captura **no va a
// funcionar** salvo que alguien la haya autorizado a mano antes del robo. Y el
// modo en que no funciona es el peligroso — `screencapture` **no falla**:
// devuelve 0 y escribe un PNG con el fondo de escritorio y sin una sola
// ventana, o directamente negro. Un caso alimentado con eso parece tener
// evidencia y no la tiene.
//
// Por eso acá hay DOS verificaciones y no una:
//
//  1. `CGPreflightScreenCaptureAccess` vía el binario del sistema, ANTES de
//     capturar: si el permiso no está concedido se reporta `permiso_denegado` y
//     no se captura nada.
//  2. La verificación de imagen en negro que hace `CaptureScreen` para los tres
//     SO, DESPUÉS: por si la primera dijera que sí y el resultado igual saliera
//     vacío.
//
// 🔴 SIN TERRENO: no se ha probado en un Mac real (ver la nota de la Fase 1 en
// actions.md). Lo que está escrito acá es el comportamiento documentado de
// `screencapture` y del subsistema TCC, no una medición.

// capturarPantalla toma la captura dentro de la sesión del usuario de consola.
//
// `screencapture` es parte del sistema (/usr/sbin), así que no hay dependencia
// que instalar. Las banderas:
//
//	-x  sin sonido de obturador. En un equipo robado, el "clic" avisa al ladrón
//	    que alguien lo está mirando — y esta feature depende de no delatarse.
//	-C  sin el cursor del mouse dibujado: la evidencia es lo que hay en pantalla.
//	-t png  formato explícito, para no depender de la preferencia del usuario.
func (a *Agent) capturarPantalla(nombre string) ScreenShot {
	if !a.permisoDeCapturaConcedido() {
		a.Logger.Infoln("capturarPantalla: macOS no tiene concedido el permiso de Grabación de pantalla, no se captura")
		return ScreenShot{Reason: captureDenied}
	}

	// El directorio del usuario, no el del agente: el proceso corre con `sudo -u`
	// y `/opt/observeragent` es de root. `/tmp` está siempre y es escribible; el
	// archivo se cierra a 0600 apenas existe (ver CaptureScreen) y se borra en
	// cuanto se sube.
	destino := filepath.Join(os.TempDir(), filepath.Base(nombre))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd, err := a.newUserSessionCmd(ctx, "/usr/sbin/screencapture", "-x", "-C", "-t", "png", destino)
	if err != nil {
		a.Logger.Debugln("capturarPantalla: no hay sesión de usuario de consola:", err)
		return ScreenShot{Reason: captureNoSession}
	}

	salida, err := cmd.CombinedOutput()
	if err != nil {
		a.Logger.Debugln("capturarPantalla: screencapture falló:", err, "salida:", strings.TrimSpace(string(salida)))
		_ = os.Remove(destino)
		return ScreenShot{Reason: captureFailed}
	}

	// El `exit 0` no prueba el efecto: sin permiso, `screencapture` sale con 0 y
	// puede no dejar nada. Lo que prueba la captura es el archivo.
	info, err := os.Stat(destino)
	if err != nil || info.Size() == 0 {
		a.Logger.Debugln("capturarPantalla: screencapture salió con 0 pero no dejó archivo utilizable")
		_ = os.Remove(destino)
		return ScreenShot{Reason: captureFailed}
	}

	return ScreenShot{Path: destino}
}

// permisoDeCapturaConcedido pregunta a TCC si este agente puede capturar la
// pantalla, SIN pedirle nada al usuario.
//
// La consulta se hace con `osascript` sobre el framework CoreGraphics
// (`CGPreflightScreenCaptureAccess`) porque es la única API que responde sin
// efectos secundarios: su hermana `CGRequestScreenCaptureAccess` **abre un
// diálogo** pidiendo el permiso, y un diálogo en un equipo robado es
// exactamente lo que no puede pasar. Preflight sólo lee el estado.
//
// Ante cualquier problema para consultar se devuelve `true` y se deja que la
// captura lo intente: el resultado se verifica igual por el lado de la imagen
// (una captura sin permiso sale negra y `CaptureScreen` la descarta), así que
// equivocarse hacia el intento cuesta un archivo de más, y equivocarse hacia la
// negativa costaría TODA la evidencia de un caso real por un fallo de la
// consulta.
func (a *Agent) permisoDeCapturaConcedido() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// El script imprime "1" o "0". `use framework "CoreGraphics"` expone la
	// función a AppleScript sin necesitar cgo ni un helper compilado aparte —
	// importa porque el binario de macOS se cross-compila desde Linux con
	// CGO_ENABLED=0 en parte de la matriz de release.
	const script = `use framework "CoreGraphics"
use scripting additions
if (current application's CGPreflightScreenCaptureAccess()) then
	return "1"
else
	return "0"
end if`

	cmd, err := a.newUserSessionCmd(ctx, "/usr/bin/osascript", "-e", script)
	if err != nil {
		a.Logger.Debugln("permisoDeCapturaConcedido: sin sesión de usuario para consultar TCC:", err)
		return true
	}

	salida, err := cmd.Output()
	if err != nil {
		a.Logger.Debugln("permisoDeCapturaConcedido: no se pudo consultar TCC:", err)
		return true
	}

	return strings.TrimSpace(string(salida)) != "0"
}

// motivoDePantallaNegra: en macOS el caso "negra" casi siempre es el permiso
// TCC, y ese ya se detecta ANTES de capturar (`permisoDeCapturaConcedido`). Lo
// que quede después es negro sin explicación conocida, así que se reporta como
// tal en vez de adivinar.
func (a *Agent) motivoDePantallaNegra() string {
	return captureBlank
}

// usuarioDeLaSesionGrafica devuelve el usuario de consola (el de /dev/console),
// que es quien tiene la pantalla que se captura.
func (a *Agent) usuarioDeLaSesionGrafica() string {
	_, username, err := consoleUser()
	if err != nil {
		return ""
	}
	return username
}
