//go:build darwin
// +build darwin

package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Feature 028 · puente hacia la sesión del usuario en macOS.
//
// `consoleUser` viene de la feature 025 (ubicación nativa por CoreLocation),
// donde ya había que salir de la sesión del sistema para que `locationd`
// atendiera la sonda. Vivía dentro de `geolocation_darwin.go`, que se compila
// con la restricción `darwin && cgo`.
//
// Se movió acá, a un archivo `darwin` sin condición de cgo, por una razón
// concreta: `lock`, `alert` y `alarm` no necesitan cgo, y si el helper siguiera
// atado a la compilación con cgo, un binario `CGO_ENABLED=0` (el cross-compile
// desde Linux) se quedaría sin las tres acciones además de sin CoreLocation.
// `darwin && cgo` es un subconjunto de `darwin`, así que el proveedor de
// geolocalización lo sigue viendo igual.

// consoleUser devuelve el uid y el nombre del usuario con la sesión gráfica
// abierta, leyendo el dueño de /dev/console.
//
// Si el dueño es root o una cuenta de servicio (prefijo `_`), no hay nadie
// realmente sentado frente al equipo: puede ser la pantalla de login. Devolver
// error en ese caso es lo correcto — un `alert` que nadie va a ver no debería
// reportarse como entregado.
func consoleUser() (string, string, error) {
	out, err := exec.Command("/usr/bin/stat", "-f", "%u %Su", "/dev/console").Output()
	if err != nil {
		return "", "", err
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) != 2 {
		return "", "", fmt.Errorf("salida inesperada de stat: %q", string(out))
	}
	uid, name := fields[0], fields[1]
	if uid == "0" || name == "root" || strings.HasPrefix(name, "_") {
		return "", "", fmt.Errorf("sin sesión de usuario (console=%s)", name)
	}
	return uid, name, nil
}

// newUserSessionCmd prepara un comando para correr dentro de la sesión del
// usuario de consola.
//
// `launchctl asuser <uid>` mete el proceso en el dominio de sesión de ese
// usuario — que es lo que exigen tanto CoreAudio (para que la alarma se oiga)
// como el subsistema de ventanas (para que el diálogo se dibuje). El `sudo -u`
// que va después baja los privilegios: `asuser` cambia de dominio, no de
// usuario, y sin el `sudo` el proceso seguiría siendo root dentro de la sesión
// del usuario.
func (a *Agent) newUserSessionCmd(ctx context.Context, exe string, args ...string) (*exec.Cmd, error) {
	uid, username, err := consoleUser()
	if err != nil {
		a.Logger.Debugln("newUserSessionCmd:", err)
		return nil, errNoUserSession
	}

	full := append([]string{"asuser", uid, "/usr/bin/sudo", "-u", username, exe}, args...)

	return exec.CommandContext(ctx, "/bin/launchctl", full...), nil
}

// killUserSessionCmd mata el proceso y sus hijos: el bucle de la alarma es un
// shell que lanza un `afplay` por repetición.
func killUserSessionCmd(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = KillProc(int32(cmd.Process.Pid))
}
