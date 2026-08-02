//go:build darwin
// +build darwin

package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
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

// activeGraphicalSessionType es el contrapunto de la versión Linux, donde el dato
// distingue X11 de Wayland y con eso predice si «Tomar control» va a mostrar una
// pantalla negra. En macOS **no aplica**: no hay tipos de sesión intercambiables
// —siempre es Aqua— y el KVM del meshagent funciona ahí. Devolver "" hace que
// `osString` no anexe nada, que es el comportamiento correcto.
func activeGraphicalSessionType() string {
	return ""
}

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

	cmd := exec.CommandContext(ctx, "/bin/launchctl", full...)

	// Grupo de procesos propio, igual que en Linux. Sin esto todo el árbol hereda
	// el grupo del PROPIO AGENTE — medido en macOS 26.5.2: el bucle de la alarma
	// tenía PGID = pid del agente. O sea que no existía ningún grupo que matar sin
	// matarse a sí mismo, y la única herramienta era `KillProc`, que recorre el
	// árbol por el enlace padre-hijo y por eso se queda sin nada que recorrer en
	// cuanto el padre muere. Nadie en la cadena (`launchctl`, `sudo`, `sh`) crea
	// grupo propio, así que basta pedirlo acá para que lo hereden los tres.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	return cmd, nil
}

// killUserSessionCmd mata el grupo de procesos completo.
//
// El signo negativo es la clave: `kill(-pgid)` alcanza a `launchctl`, al `sudo`,
// al `/bin/sh` del bucle y al `afplay` que esté sonando en ese momento, y sigue
// funcionando aunque el líder del grupo ya sea un cadáver. Eso es justamente lo
// que `KillProc` no podía hacer.
func killUserSessionCmd(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	pid := cmd.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		// Si el grupo no se creó o ya no existe, se cae al pid suelto.
		_ = KillProc(int32(pid))
	}
}
