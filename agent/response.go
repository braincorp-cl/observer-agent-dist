package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
)

// Feature 028 · respuesta rápida de endpoint (homologación de las acciones
// `lock`, `alert` y `alarm` de Prey; ver `_reversa_forward/024-prey-homologacion-backlog/`).
//
// Las tres acciones comparten un problema y una solución:
//
//	El agente corre como servicio del sistema (sesión 0 en Windows, launchd
//	system domain en macOS, systemd system unit en Linux). Ahí NO hay escritorio
//	ni dispositivo de audio: un MessageBox no se vería, un `paplay` no sonaría y
//	`LockWorkStation` no tendría a quién bloquear. Todo esto tiene que ejecutarse
//	dentro de la sesión gráfica del usuario interactivo.
//
// El puente lo pone `newUserSessionCmd`, implementado por SO en los archivos
// `usersession_*.go`. Este archivo sólo contiene lo que es igual en las tres
// plataformas: los límites de la alarma y el registro que permite detenerla.
//
// Regla de diseño (requisito i18n): **el agente no contiene ni un solo texto
// destinado al usuario final**. El título y el mensaje del `alert` llegan desde
// el servidor, escritos por el operador, y el agente los pinta tal cual. Así
// todo lo traducible vive en el i18n de la consola (`es.json`/`en.json`) y el
// agente no necesita catálogo propio ni averiguar el locale del equipo.

const (
	// alarmDefaultSeconds es lo que dura la alarma si el servidor no especifica.
	alarmDefaultSeconds = 30
	// alarmMaxSeconds es un tope duro. La alarma es una herramienta para
	// encontrar físicamente un equipo, no para castigar a quien lo tenga: sin
	// tope, un comando mal enviado deja un equipo sonando indefinidamente y la
	// única salida sería apagarlo.
	alarmMaxSeconds = 300

	// alertMaxTitleLen y alertMaxBodyLen recortan el texto del operador antes de
	// pasarlo al diálogo del SO. No es validación de seguridad (el texto nunca
	// se interpola en un script, viaja por variables de entorno o argv), es
	// evitar un diálogo gigante e imposible de cerrar.
	alertMaxTitleLen = 120
	alertMaxBodyLen  = 2000

	// alertTimeout acota cuánto esperamos por el diálogo. El usuario puede
	// dejarlo abierto para siempre; no queremos un proceso huérfano por cada
	// mensaje enviado.
	alertTimeout = 10 * time.Minute
)

// Códigos de respuesta que viajan al servidor.
//
// El agente devuelve un CÓDIGO, nunca una frase. Es la otra mitad del requisito
// i18n: si devolviera "no hay una sesión gráfica activa", ese texto en español
// llegaría tal cual a la consola de un operador que la tiene en inglés, sin pasar
// por `es.json`/`en.json`. Con un código, la consola traduce y el agente no sabe
// —ni necesita saber— en qué idioma trabaja quien lo maneja.
const (
	RespOK = "ok"
	// RespNoUserSession: nadie tiene sesión gráfica abierta. No es un fallo del
	// agente y el operador necesita distinguirlo: significa que el mensaje NO se
	// vio, o que no había pantalla que bloquear.
	RespNoUserSession = "no_user_session"
	// RespNoDialogTool: no hay con qué dibujar un diálogo (Linux sin zenity,
	// kdialog, notify-send ni xmessage).
	RespNoDialogTool = "no_dialog_tool"
	// RespNoAudioPlayer: no hay reproductor con un sonido disponible.
	RespNoAudioPlayer = "no_audio_player"
	// RespLockUnavailable: ningún método de bloqueo funcionó en este escritorio.
	RespLockUnavailable = "lock_unavailable"
	// RespEmptyMessage: el mensaje venía vacío.
	RespEmptyMessage = "empty_message"
	// RespError: cualquier otra cosa. El detalle queda en el log del agente, que
	// es donde se diagnostica; al operador se le dice que falló sin inventarle una
	// causa.
	RespError = "error"
)

var (
	errNoUserSession   = errors.New("no hay una sesión gráfica de usuario activa")
	errNoDialogTool    = errors.New("no hay herramienta de diálogo instalada")
	errNoAudioPlayer   = errors.New("no hay reproductor de audio con un sonido disponible")
	errLockUnavailable = errors.New("ningún método de bloqueo disponible")
	errEmptyMessage    = errors.New("el mensaje viene vacío")
)

// ResponseCode traduce un error de estas acciones al código que se le manda al
// servidor. Un error desconocido cae en RespError en vez de filtrar su texto.
func ResponseCode(err error) string {
	switch {
	case err == nil:
		return RespOK
	case errors.Is(err, errNoUserSession):
		return RespNoUserSession
	case errors.Is(err, errNoDialogTool):
		return RespNoDialogTool
	case errors.Is(err, errNoAudioPlayer):
		return RespNoAudioPlayer
	case errors.Is(err, errLockUnavailable):
		return RespLockUnavailable
	case errors.Is(err, errEmptyMessage):
		return RespEmptyMessage
	default:
		return RespError
	}
}

// alarmState guarda la alarma en curso para poder detenerla. Sólo puede haber
// una a la vez: un segundo `alarm` reemplaza al primero en vez de sumarle otra
// fuente de ruido encima.
type alarmState struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	// epoch numera las alarmas para que la goroutine de una alarma vieja no
	// borre el `cancel` de la que la reemplazó. Sin esto: llega un segundo
	// `alarm`, StopAlarm despierta a la goroutine de la primera, y esa goroutine
	// —que corre en paralelo— limpia el cancel recién guardado por la segunda.
	// Resultado: una alarma sonando que `stopalarm` ya no puede detener.
	epoch uint64
}

var runningAlarm alarmState

// ShowAlert muestra un mensaje al usuario del equipo dentro de su sesión
// gráfica. Devuelve error si no hay nadie con sesión abierta: es información
// útil para el operador (el mensaje NO se vio), no un fallo del agente.
func (a *Agent) ShowAlert(title, message string) error {
	title = truncate(strings.TrimSpace(title), alertMaxTitleLen)
	message = truncate(strings.TrimSpace(message), alertMaxBodyLen)

	if message == "" {
		return errEmptyMessage
	}

	return a.showAlert(title, message)
}

// LockScreen bloquea la sesión del usuario. Ojo con la expectativa: esto es el
// bloqueo de pantalla nativo del SO (Tier 1), equivalente a Win+L. NO es el
// overlay antirrobo tipo kiosco de Prey, que es una aplicación de UI propia por
// plataforma y quedó explícitamente fuera de alcance (Tier 2 del backlog 024).
// El usuario puede desbloquear con su propia contraseña.
func (a *Agent) LockScreen() error {
	return a.lockScreen()
}

// PlayAlarm hace sonar una alarma en el equipo durante `seconds`. Sirve para
// encontrar físicamente una máquina (el caso antirrobo de Prey).
//
// El sonido es el propio del SO; el agente no empaqueta audio. La duración la
// controla este lado y no el script que suena: el comando por SO reproduce en
// bucle indefinidamente y acá lo matamos al vencer el plazo. Así un bucle que
// se cuelgue igual muere, y `StopAlarm` puede cortarlo antes.
func (a *Agent) PlayAlarm(seconds int) error {
	if seconds <= 0 {
		seconds = alarmDefaultSeconds
	}
	if seconds > alarmMaxSeconds {
		seconds = alarmMaxSeconds
	}

	// Cualquier alarma anterior se detiene antes de arrancar la nueva.
	a.StopAlarm()

	ctx, cancel := context.WithCancel(context.Background())

	cmd, err := a.alarmLoopCmd(ctx)
	if err != nil {
		cancel()
		return err
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}

	runningAlarm.mu.Lock()
	runningAlarm.epoch++
	myEpoch := runningAlarm.epoch
	runningAlarm.cancel = cancel
	runningAlarm.mu.Unlock()

	go func() {
		timer := time.NewTimer(time.Duration(seconds) * time.Second)
		defer timer.Stop()

		select {
		case <-timer.C:
			a.Logger.Debugln("PlayAlarm: se cumplió la duración, deteniendo")
		case <-ctx.Done():
			a.Logger.Debugln("PlayAlarm: detenida antes de tiempo")
		}

		// Matar el árbol de procesos, no sólo el padre: el bucle de sonido es un
		// shell que lanza un reproductor hijo por cada repetición, y matar sólo
		// el shell dejaría el último sonido colgado.
		killUserSessionCmd(cmd)
		cancel()

		// Cosechar el proceso. Sin este Wait cada alarma dejaría un zombi.
		_ = cmd.Wait()

		runningAlarm.mu.Lock()
		if runningAlarm.epoch == myEpoch {
			runningAlarm.cancel = nil
		}
		runningAlarm.mu.Unlock()
	}()

	return nil
}

// StopAlarm detiene la alarma en curso, si hay una. Es idempotente: llamarla sin
// alarma sonando no es un error.
func (a *Agent) StopAlarm() {
	runningAlarm.mu.Lock()
	cancel := runningAlarm.cancel
	runningAlarm.cancel = nil
	runningAlarm.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// truncate recorta respetando runes, para no partir un carácter multibyte por la
// mitad y dejar basura en el diálogo (la flota es hispanohablante: acentos y ñ).
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// firstExisting devuelve la primera ruta de la lista que exista en disco. Se usa
// para elegir el sonido de la alarma: preferimos un sonido concreto, pero no
// damos por sentado que esté (varía entre versiones y ediciones del SO).
func firstExisting(paths []string) string {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// shellQuote deja un valor seguro dentro de comillas simples de shell POSIX.
// Sólo se usa con rutas de sonido que elegimos nosotros de una lista fija —
// nunca con texto del operador—, pero se cita igual: si mañana la lista pasa a
// ser configurable, el punto de inyección ya está cerrado.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
