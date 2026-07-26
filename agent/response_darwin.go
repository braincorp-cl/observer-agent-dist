//go:build darwin
// +build darwin

package agent

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// Feature 028 · implementación de lock / alert / alarm en macOS.

// alertAppleScript muestra el diálogo.
//
// El texto NO se interpola en el script. `osascript` recibe el script por `-e` y
// los datos por `argv`, después del `--`. Un mensaje que contenga comillas,
// saltos de línea o algo que parezca AppleScript se trata como texto y nunca
// como código. Interpolar acá sería una inyección de AppleScript ejecutándose en
// la sesión del usuario.
const alertAppleScript = `on run argv
	display dialog (item 2 of argv) with title (item 1 of argv) buttons {"OK"} default button 1 with icon caution
end run`

// alarmSounds son sonidos del propio sistema. Están en cualquier macOS, así que
// no hace falta empaquetar audio en el binario del agente.
var alarmSounds = []string{
	"/System/Library/Sounds/Sosumi.aiff",
	"/System/Library/Sounds/Basso.aiff",
	"/System/Library/Sounds/Ping.aiff",
}

// lockCandidates son las dos formas de bloquear, en orden de preferencia.
//
//   - CGSession -suspend es el cambio rápido de usuario: manda a la ventana de
//     inicio de sesión. Es el método documentado de siempre, pero es un binario
//     dentro de un bundle de menú extra y Apple lo ha movido entre versiones, así
//     que se verifica que exista antes de invocarlo.
//   - pmset displaysleepnow duerme la pantalla. Bloquea de verdad **sólo si** el
//     equipo tiene activado pedir contraseña al despertar. Es el respaldo, no el
//     método preferido, justamente por esa condición.
var lockCandidates = []struct {
	path string
	args []string
}{
	{"/System/Library/CoreServices/Menu Extras/User.menu/Contents/Resources/CGSession", []string{"-suspend"}},
	{"/usr/bin/pmset", []string{"displaysleepnow"}},
}

func (a *Agent) showAlert(title, message string) error {
	ctx, cancel := context.WithTimeout(context.Background(), alertTimeout)
	// Sin `defer cancel()`: el diálogo sobrevive al retorno de esta función a
	// propósito, y cancelar acá lo mataría antes de que el usuario lo viera. El
	// cancel se libera en la goroutine de más abajo, o al vencer alertTimeout.
	cmd, err := a.newUserSessionCmd(ctx, "/usr/bin/osascript",
		"-e", alertAppleScript, "--", title, message)
	if err != nil {
		cancel()
		return err
	}

	// No se espera a que el usuario cierre el diálogo: `Start` y listo. Si
	// esperáramos, la respuesta al servidor quedaría colgada hasta que alguien
	// hiciera clic, y el operador necesita saber que el mensaje se entregó, no
	// que se leyó. El contexto lo mata si queda abierto demasiado tiempo.
	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}

	// El Wait va en una goroutine y no se omite: sin él el diálogo quedaría como
	// proceso zombi hasta que muera el agente, uno por cada mensaje enviado.
	go func() {
		_ = cmd.Wait()
		cancel()
	}()

	return nil
}

func (a *Agent) lockScreen() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, c := range lockCandidates {
		if _, err := os.Stat(c.path); err != nil {
			continue
		}

		cmd, err := a.newUserSessionCmd(ctx, c.path, c.args...)
		if err != nil {
			return err
		}

		if err := cmd.Run(); err == nil {
			a.Logger.Debugln("lockScreen: bloqueado con", c.path)
			return nil
		}
		a.Logger.Debugln("lockScreen: falló", c.path, "— probando el siguiente")
	}

	return errLockUnavailable
}

func (a *Agent) alarmLoopCmd(ctx context.Context) (*exec.Cmd, error) {
	sound := firstExisting(alarmSounds)
	if sound == "" {
		return nil, errNoAudioPlayer
	}

	// afplay es sincrónico, así que el `while` reproduce una vez tras otra hasta
	// que se mata el proceso. La duración la controla PlayAlarm.
	script := "while :; do /usr/bin/afplay " + shellQuote(sound) + "; sleep 0.25; done"

	return a.newUserSessionCmd(ctx, "/bin/sh", "-c", script)
}
