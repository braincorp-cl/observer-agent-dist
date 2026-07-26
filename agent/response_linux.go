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

// lockScreen bloquea la sesión.
//
// `loginctl lock-session` es el camino preferido y tiene una ventaja sobre todo
// lo demás: lo ejecuta root directamente, sin necesidad de entrar a la sesión del
// usuario. systemd le manda la señal de bloqueo al gestor de sesión, y GNOME, KDE
// y los demás escritorios modernos la respetan.
//
// El respaldo son las herramientas del escritorio, que sí necesitan correr como
// el usuario. `xdg-screensaver` es el genérico; los otros dos cubren escritorios
// que no lo traen.
func (a *Agent) lockScreen() error {
	sess, err := a.findUserSession()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if sess.SessionID != "" {
		if loginctl, err := exec.LookPath("loginctl"); err == nil {
			cmd := exec.CommandContext(ctx, loginctl, "lock-session", sess.SessionID)
			if err := cmd.Run(); err == nil {
				a.Logger.Debugln("lockScreen: bloqueado con loginctl lock-session", sess.SessionID)
				return nil
			}
			a.Logger.Debugln("lockScreen: loginctl lock-session no funcionó, se prueban las herramientas del escritorio")
		}
	}

	fallbacks := [][]string{
		{"xdg-screensaver", "lock"},
		{"gnome-screensaver-command", "--lock"},
		{"dm-tool", "lock"},
		{"loginctl", "lock-sessions"},
	}

	for _, f := range fallbacks {
		bin, err := exec.LookPath(f[0])
		if err != nil {
			continue
		}
		cmd := a.newSessionCmdFor(ctx, sess, bin, f[1:]...)
		if err := cmd.Run(); err == nil {
			a.Logger.Debugln("lockScreen: bloqueado con", f[0])
			return nil
		}
	}

	return errLockUnavailable
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
