//go:build linux
// +build linux

package agent

import (
	"context"
	"os/exec"
	"time"
)

// Feature 028 · implementación de lock / alert / alarm en Linux.
//
// Linux no tiene "la" forma de hacer estas tres cosas: depende del escritorio, del
// display manager y del servidor de audio. Cada acción prueba una cadena de
// candidatos y devuelve error sólo si ninguno estaba. La alternativa —asumir
// zenity y PulseAudio— fallaría en silencio en la mitad de los equipos.

// alertDialogs son las formas de mostrar un diálogo, en orden de preferencia.
//
// El texto del operador NUNCA se interpola en una cadena: se pasa como argumento
// del proceso (`argv`), en la posición marcada por `%TITLE%` y `%BODY%`. No hay
// shell en el medio, así que no hay comillas que escapar ni inyección posible.
var alertDialogs = []struct {
	bin  string
	args []string // "%TITLE%" y "%BODY%" se sustituyen por los valores reales
}{
	// zenity (GTK/GNOME) y kdialog (KDE) son diálogos modales: se ven de verdad.
	{"zenity", []string{"--info", "--title", "%TITLE%", "--text", "%BODY%"}},
	{"kdialog", []string{"--title", "%TITLE%", "--msgbox", "%BODY%"}},
	// notify-send es una notificación, no un diálogo: menos intrusiva y más fácil
	// de ignorar. Va después a propósito, como red de seguridad.
	{"notify-send", []string{"--urgency=critical", "%TITLE%", "%BODY%"}},
	// xmessage es feo pero está en cualquier instalación con X11.
	{"xmessage", []string{"-center", "-title", "%TITLE%", "%BODY%"}},
}

// alarmPlayers empareja reproductor con formato de sonido.
//
// El emparejamiento importa: `aplay` es ALSA puro y sólo reproduce WAV, mientras
// paplay/pw-play (PulseAudio/PipeWire) manejan los .oga del tema freedesktop, que
// es lo que trae instalado un escritorio típico.
var alarmPlayers = []struct {
	bin    string
	sounds []string
}{
	{"paplay", freedesktopSounds},
	{"pw-play", freedesktopSounds},
	{"aplay", []string{
		"/usr/share/sounds/alsa/Front_Center.wav",
		"/usr/share/sounds/alsa/Noise.wav",
	}},
}

var freedesktopSounds = []string{
	"/usr/share/sounds/freedesktop/stereo/alarm-clock-elapsed.oga",
	"/usr/share/sounds/freedesktop/stereo/complete.oga",
	"/usr/share/sounds/freedesktop/stereo/bell.oga",
	"/usr/share/sounds/freedesktop/stereo/dialog-warning.oga",
}

func (a *Agent) showAlert(title, message string) error {
	sess, err := a.findUserSession()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), alertTimeout)

	for _, d := range alertDialogs {
		bin, err := exec.LookPath(d.bin)
		if err != nil {
			continue
		}

		args := make([]string, len(d.args))
		for i, arg := range d.args {
			switch arg {
			case "%TITLE%":
				args[i] = title
			case "%BODY%":
				args[i] = message
			default:
				args[i] = arg
			}
		}

		cmd := a.newSessionCmdFor(ctx, sess, bin, args...)

		// No se espera el cierre del diálogo (ver el equivalente en macOS): el
		// operador necesita saber que se entregó, no que se leyó. El contexto lo
		// mata si queda abierto para siempre; por eso el cancel se libera en una
		// goroutine y no con defer.
		if err := cmd.Start(); err == nil {
			a.Logger.Debugln("showAlert: mostrado con", d.bin)
			go func() {
				_ = cmd.Wait()
				cancel()
			}()
			return nil
		}

		a.Logger.Debugln("showAlert: falló", d.bin, "— probando el siguiente")
	}

	cancel()
	return errNoDialogTool
}

// lockCandidates son las herramientas del escritorio que bloquean la pantalla.
//
// A diferencia de `loginctl`, el código de salida de éstas SÍ dice algo: hablan
// directamente con el bloqueador y fallan si no está corriendo. Van primero las
// específicas de cada bloqueador —que son las que fallan de verdad cuando no
// corresponden— y `xdg-screensaver`, el genérico, después.
var lockCandidates = [][]string{
	{"xscreensaver-command", "-lock"},
	{"gnome-screensaver-command", "--lock"},
	{"light-locker-command", "--lock"},
	{"xflock4"},
	{"xdg-screensaver", "lock"},
	// dm-tool salta al greeter del display manager, que en la práctica también
	// deja la sesión bloqueada.
	{"dm-tool", "lock"},
}

// lockScreen bloquea la sesión.
//
// `loginctl lock-session` se prueba primero porque lo ejecuta root sin entrar a la
// sesión del usuario, pero **su código de salida no sirve como prueba de que algo
// se bloqueó**: sale 0 por el solo hecho de entregarle la señal a logind, que la
// reenvía a quien esté escuchando. Si nadie escucha, no pasa nada y el 0 igual
// llega.
//
// Eso no es teórico: medido en un Ubuntu 24.04 real con LXQt + xscreensaver, la
// señal se entregaba, `loginctl` salía 0 y la pantalla NUNCA se bloqueaba —
// xscreensaver no escucha a logind. El agente contestaba `ok` y el operador
// quedaba creyendo que un equipo estaba bloqueado cuando no lo estaba, que en una
// función antirrobo es el peor error posible.
//
// Por eso el éxito de esta vía exige confirmación de logind (`LockedHint`), y si
// no llega se sigue con las herramientas del escritorio.
func (a *Agent) lockScreen() error {
	sess, err := a.findUserSession()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	loginctl, loginctlErr := exec.LookPath("loginctl")

	if sess.SessionID != "" && loginctlErr == nil {
		cmd := exec.CommandContext(ctx, loginctl, "lock-session", sess.SessionID)
		if err := cmd.Run(); err == nil {
			if a.lockedHintConfirmed(ctx, loginctl, sess.SessionID) {
				a.Logger.Debugln("lockScreen: bloqueado con loginctl lock-session", sess.SessionID)
				return nil
			}
			a.Logger.Debugln("lockScreen: loginctl entregó la señal pero nadie la respetó (LockedHint sigue en no); se prueban las herramientas del escritorio")
		} else {
			a.Logger.Debugln("lockScreen: loginctl lock-session falló:", err)
		}
	}

	for _, f := range lockCandidates {
		bin, err := exec.LookPath(f[0])
		if err != nil {
			continue
		}
		cmd := a.newSessionCmdFor(ctx, sess, bin, f[1:]...)
		if err := cmd.Run(); err != nil {
			a.Logger.Debugln("lockScreen:", f[0], "falló:", err)
			continue
		}

		// Se registra si logind lo confirma o no, pero no se exige: hay
		// bloqueadores que bloquean sin avisarle a logind, y devolver
		// `lock_unavailable` con la pantalla ya bloqueada sería igual de
		// engañoso. Acá el código de salida sí es evidencia.
		if sess.SessionID != "" && loginctlErr == nil &&
			!a.lockedHintConfirmed(ctx, loginctl, sess.SessionID) {
			a.Logger.Debugln("lockScreen: bloqueado con", f[0], "(logind no reporta LockedHint)")
		} else {
			a.Logger.Debugln("lockScreen: bloqueado con", f[0])
		}

		return nil
	}

	return errLockUnavailable
}

// lockedHintConfirmed espera a que logind reporte la sesión como bloqueada.
//
// Se consulta en un bucle corto porque el bloqueo no es instantáneo: logind avisa,
// el escritorio dibuja y sólo entonces llama a SetLockedHint. Un chequeo único
// justo después de mandar la señal daría negativo incluso cuando sí funcionó.
func (a *Agent) lockedHintConfirmed(ctx context.Context, loginctl, sessionID string) bool {
	for i := 0; i < 10; i++ {
		out, err := exec.CommandContext(ctx, loginctl, "show-session", sessionID,
			"--property=LockedHint").Output()
		if err == nil && parseLoginctlProps(out)["LockedHint"] == "yes" {
			return true
		}

		select {
		case <-ctx.Done():
			return false
		case <-time.After(300 * time.Millisecond):
		}
	}

	return false
}

func (a *Agent) alarmLoopCmd(ctx context.Context) (*exec.Cmd, error) {
	sess, err := a.findUserSession()
	if err != nil {
		return nil, err
	}

	for _, p := range alarmPlayers {
		bin, err := exec.LookPath(p.bin)
		if err != nil {
			continue
		}
		sound := firstExisting(p.sounds)
		if sound == "" {
			continue
		}

		// El reproductor es sincrónico, así que el `while` lo repite hasta que se
		// mata el grupo de procesos. La duración la controla PlayAlarm.
		script := "while :; do " + shellQuote(bin) + " " + shellQuote(sound) + "; sleep 0.25; done"

		a.Logger.Debugln("alarmLoopCmd: alarma con", bin, sound)
		return a.newSessionCmdFor(ctx, sess, "/bin/sh", "-c", script), nil
	}

	return nil, errNoAudioPlayer
}
