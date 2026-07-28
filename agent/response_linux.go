//go:build linux
// +build linux

package agent

import (
	"context"
	"os/exec"
	"strings"
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
//
// Lo que sí hay que escapar es otra cosa, y no es seguridad sino fidelidad: casi
// todas estas herramientas INTERPRETAN el cuerpo del mensaje. `escapeBody` lo
// protege para que la persona lea exactamente lo que el operador escribió.
type alertDialog struct {
	bin  string
	args []string // "%TITLE%" y "%BODY%" se sustituyen por los valores reales
	// escapeBody protege el cuerpo del parser de la herramienta. Es distinto por
	// herramienta porque cada una interpreta cosas distintas; nil = lo muestra crudo.
	//
	// Sólo se aplica al CUERPO. El título de zenity y de kdialog es el título de la
	// ventana y no pasa por ningún parser: escaparlo mostraría un `&lt;` literal en
	// la barra.
	escapeBody func(string) string
}

var alertDialogs = []alertDialog{
	// zenity (GTK/GNOME) y kdialog (KDE) son diálogos modales: se ven de verdad.
	{"zenity", []string{"--info", "--title", "%TITLE%", "--text", "%BODY%"}, escapeZenityText},
	{"kdialog", []string{"--title", "%TITLE%", "--msgbox", "%BODY%"}, escapeMarkupEntities},
	// notify-send es una notificación, no un diálogo: menos intrusiva y más fácil
	// de ignorar. Va después a propósito, como red de seguridad.
	{"notify-send", []string{"--urgency=critical", "%TITLE%", "%BODY%"}, escapeMarkupEntities},
	// xmessage es feo pero está en cualquier instalación con X11. Es el único que
	// muestra el texto crudo: no parsea nada, así que escaparlo sería empeorarlo.
	{"xmessage", []string{"-center", "-title", "%TITLE%", "%BODY%"}, nil},
}

// escapeMarkupEntities protege el texto de un parser de marcas: Pango en zenity,
// rich text de Qt en kdialog (KMessageBox lo autodetecta), y el subconjunto de
// markup que los demonios de notificación aplican al cuerpo.
//
// Sin esto, un mensaje en texto libre del operador —"a < b", "<urgente>", "Ventas &
// Cobranzas"— sale mutilado, y si el markup queda inválido el diálogo puede salir
// VACÍO. Es la mentira más barata de esta feature: el diálogo aparece, el agente
// responde `ok`, y lo que la persona leyó no es lo que se envió.
//
// El `&` va primero. Al revés, los `&` que introducen las otras dos sustituciones se
// volverían a escapar y la pantalla mostraría `&amp;lt;`.
func escapeMarkupEntities(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// escapeZenityText suma la otra mitad del destrozo, que es exclusiva de zenity y no
// tiene nada que ver con los signos de mayor y menor.
//
// Verificado en su fuente (`src/msg.c`): cuando el markup está activo —que es por
// omisión— zenity llama `gtk_label_set_markup(g_strcompress(texto))`. O sea que
// ANTES de parsear las marcas le pasa el texto por `g_strcompress`, que convierte
// las secuencias con barra invertida en el carácter que representan. Sin duplicar la
// barra, un mensaje que mencione `C:\temp\nueva` pierde la ruta: `\t` se vuelve un
// tabulador y `\n` un salto de línea. La flota es mixta y los mensajes del operador
// nombran rutas de Windows, así que el caso no es hipotético.
//
// La barra se duplica ANTES de las entidades: al revés no habría interacción igual
// —ninguna entidad contiene barras— pero así el orden refleja el de zenity, que
// descomprime primero y parsea después.
func escapeZenityText(s string) string {
	return escapeMarkupEntities(strings.ReplaceAll(s, `\`, `\\`))
}

// buildAlertArgs sustituye los marcadores por los valores reales y escapa el cuerpo
// según la herramienta.
//
// Vive aparte de showAlert por una razón concreta y aprendida: probar las funciones
// de escapado NO prueba que alguien las llame. La primera versión de estas pruebas
// pasaba en verde con el escapado desconectado del bucle —el mismo hueco que tenía
// el gate `lint:i18n`, que recorría un archivo sin aplicarle las reglas—. Con el
// armado acá, la prueba mira los argumentos que de verdad se le pasan al proceso.
func buildAlertArgs(d alertDialog, title, message string) []string {
	body := message
	if d.escapeBody != nil {
		body = d.escapeBody(message)
	}

	args := make([]string, len(d.args))
	for i, arg := range d.args {
		switch arg {
		case "%TITLE%":
			args[i] = title
		case "%BODY%":
			args[i] = body
		default:
			args[i] = arg
		}
	}

	return args
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

		cmd := a.newSessionCmdFor(ctx, sess, bin, buildAlertArgs(d, title, message)...)

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
