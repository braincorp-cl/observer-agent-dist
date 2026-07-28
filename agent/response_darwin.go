//go:build darwin
// +build darwin

package agent

import (
	"context"
	"os"
	"os/exec"
	"strings"
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

// alarmSounds son sonidos del propio sistema, el RESPALDO de la sirena generada
// (`alarmsound.go`). Están en cualquier macOS, así que no hace falta empaquetar audio
// en el binario del agente — pero son sonidos de notificación cortos, no una alarma:
// sirven para que el equipo suene, no para encontrarlo.
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
//
// ⚠️ Medido en macOS 26.5.2 (Apple Silicon, 2026-07-26): **CGSession YA NO
// EXISTE** en esa ruta. En un Mac moderno el `os.Stat` lo descarta y el único
// camino real es el respaldo — es decir, el método que sólo bloquea si el equipo
// pide contraseña al despertar. Por eso el éxito de esta acción NO puede
// deducirse del código de salida; ver screenLockConfirmed.
//
// `needsConfirm` marca a cuál de los dos NO se le cree el código de salida, con
// el mismo criterio que se usó en Linux: se exige confirmación sólo donde el
// exit 0 no dice nada del efecto.
//
//   - CGSession -suspend habla con el servidor de ventanas y falla si no puede
//     cambiar de sesión, así que su 0 sí es evidencia.
//   - pmset displaysleepnow sale 0 por el mero hecho de apagar la pantalla,
//     bloquee o no. Ese 0 es el que hay que verificar.
var lockCandidates = []struct {
	path         string
	args         []string
	needsConfirm bool
}{
	{"/System/Library/CoreServices/Menu Extras/User.menu/Contents/Resources/CGSession", []string{"-suspend"}, false},
	{"/usr/bin/pmset", []string{"displaysleepnow"}, true},
}

const (
	// lockConfirmInterval y lockConfirmTries acotan la espera por candidato: el
	// bloqueo no es instantáneo (medido: la clave aparece a los ~0,4 s), pero
	// tampoco puede eternizarse.
	lockConfirmInterval = 250 * time.Millisecond
	lockConfirmTries    = 20

	// lockTimeout es el techo de toda la operación. Está deliberadamente por
	// DEBAJO de los 15 s con los que el servidor espera esta acción
	// (`_endpoint_response` en agents/views.py): si nos pasáramos, el operador
	// vería `agent_unreachable` —un problema de red— cuando lo que ocurrió es que
	// no se pudo bloquear. Peor diagnóstico, mismo fallo.
	lockTimeout = 12 * time.Second
)

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
	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()

	for _, c := range lockCandidates {
		if _, err := os.Stat(c.path); err != nil {
			continue
		}

		cmd, err := a.newUserSessionCmd(ctx, c.path, c.args...)
		if err != nil {
			return err
		}

		if err := cmd.Run(); err != nil {
			a.Logger.Debugln("lockScreen: falló", c.path, "— probando el siguiente")
			continue
		}

		if !c.needsConfirm {
			a.Logger.Debugln("lockScreen: bloqueado con", c.path)
			return nil
		}

		// Acá el código de salida NO es evidencia: `pmset displaysleepnow` apaga
		// la pantalla y sale 0 aunque el equipo no pida contraseña al despertar,
		// con lo que el operador recibiría `ok` sobre una máquina que quedó
		// abierta. En una función antirrobo esa es la peor mentira posible.
		if a.screenLockConfirmed(ctx) {
			a.Logger.Debugln("lockScreen: bloqueado con", c.path, "(confirmado por el SO)")
			return nil
		}

		a.Logger.Debugln("lockScreen:", c.path,
			"salió 0 pero el SO no reporta la pantalla bloqueada — probando el siguiente")
	}

	return errLockUnavailable
}

// screenLockConfirmed espera a que el SO reporte la pantalla efectivamente
// bloqueada. Devuelve false si no lo confirma dentro del plazo.
//
// Se pregunta en bucle porque el bloqueo no es instantáneo: `pmset` vuelve
// enseguida y loginwindow tarda en levantar la cortina y marcar la sesión
// (medido en macOS 26.5.2: ~0,4 s).
func (a *Agent) screenLockConfirmed(ctx context.Context) bool {
	for i := 0; i < lockConfirmTries; i++ {
		if screenIsLocked(ctx) {
			return true
		}

		select {
		case <-ctx.Done():
			return false
		case <-time.After(lockConfirmInterval):
		}
	}

	return false
}

// screenIsLocked le pregunta al SO por el estado real de la pantalla.
//
// Deliberadamente NO se toma "ya no hay usuario en /dev/console" como prueba de
// bloqueo. Sería cómodo para cubrir el cambio rápido de usuario, pero **no se
// pudo medir** qué le pasa al dueño de /dev/console con la pantalla dormida: si
// resultara pasar a root, esa rama daría "bloqueado" siempre y anularía esta
// verificación entera. El cambio rápido de usuario ya está cubierto por otro
// lado — CGSession no necesita confirmación (ver lockCandidates).
func screenIsLocked(ctx context.Context) bool {
	out, err := exec.CommandContext(ctx, ioregPath,
		"-n", "Root", "-d1", "-r", "-k", "IOConsoleUsers").Output()

	return err == nil && ioregScreenIsLocked(out)
}

const ioregPath = "/usr/sbin/ioreg"

// ioregScreenIsLocked busca la marca de pantalla bloqueada en la salida de
// `ioreg -n Root -d1 -r -k IOConsoleUsers`.
//
// La clave **sólo existe cuando está bloqueado**: desbloqueado no aparece como
// `=No`, sencillamente no está. Por eso se busca su presencia con el valor `Yes`
// y no se intenta leer un booleano.
//
// Se normalizan los espacios porque ioreg no es consistente: dentro del
// diccionario escribe `"CGSSessionScreenIsLocked"=Yes` (sin espacios) y en el
// nivel de arriba `"IOConsoleLocked" = No` (con ellos).
//
// ℹ️ `IOConsoleLocked` también pasa a `Yes` al bloquear y sería más barato de
// leer, pero NO se usa: no se pudo medir si distingue "hay una cortina en
// pantalla" de "pide credencial", y confiar en una señal sin verificar es
// exactamente el error que este cambio corrige.
func ioregScreenIsLocked(out []byte) bool {
	compact := strings.ReplaceAll(string(out), " ", "")
	return strings.Contains(compact, `"CGSSessionScreenIsLocked"=Yes`)
}

func (a *Agent) alarmLoopCmd(ctx context.Context) (*exec.Cmd, error) {
	// La sirena generada va primero; los sonidos del sistema son el respaldo.
	// `afplay` reproduce WAV igual que AIFF.
	candidatos := alarmSounds
	if siren := a.ensureAlarmSiren(); siren != "" {
		candidatos = append([]string{siren}, alarmSounds...)
	}

	sound := firstExisting(candidatos)
	if sound == "" {
		return nil, errNoAudioPlayer
	}

	// afplay es sincrónico, así que el `while` reproduce una vez tras otra hasta
	// que se mata el proceso. La duración la controla PlayAlarm.
	script := "while :; do /usr/bin/afplay " + shellQuote(sound) + "; sleep 0.25; done"

	return a.newUserSessionCmd(ctx, "/bin/sh", "-c", script)
}
