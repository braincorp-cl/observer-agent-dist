package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
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
	// alarmMaxSeconds es el tope del camino normal. La alarma es una herramienta
	// para encontrar físicamente un equipo, no para castigar a quien lo tenga:
	// sin tope, un comando mal enviado deja un equipo sonando indefinidamente y
	// la única salida sería apagarlo.
	//
	// La Fase 2 agrega la ALARMA ETERNA, que cruza esa línea a conciencia y por
	// eso NO se codifica aflojando este tope: es un flag propio
	// (`AlarmOptions.Forever`) que saltea el clamp entero. Así el camino normal
	// sigue acotado y la excepción se ve en el payload, en la auditoría y en la
	// confirmación de la consola.
	alarmMaxSeconds = 300

	// alarmStopGrace es lo que StopAlarm espera a que la goroutine confirme que ya
	// mató el árbol de sonido. Existe porque PlayAlarm llama a StopAlarm antes de
	// arrancar la siguiente alarma: sin la espera, las dos se solapan un instante.
	alarmStopGrace = 3 * time.Second

	// Fase 2 · vigilancia del reproductor.
	//
	// El proceso que suena puede morir antes de la hora: lo mata quien tenga el
	// equipo desde el administrador de tareas, o se cae solo. Sin vigilante eso
	// dejaba el peor estado posible y en silencio — equipo mudo, volumen clavado
	// al máximo reafirmándose cada segundo, el archivo de estado diciendo
	// `forever` (o sea que volvía a "sonar" mudo en el próximo arranque) y la
	// consola convencida de que la alarma seguía corriendo. Un `ok` falso más.
	//
	// En Windows basta con matar UN proceso (el bucle entero es un PowerShell);
	// en macOS y Linux hay que matar el `sh` del bucle, porque matar sólo al
	// reproductor lo relanza el propio `while`.

	// alarmRespawnMaxConsecutive es cuántos fallos seguidos aguanta una alarma CON
	// DURACIÓN antes de rendirse. La eterna no se rinde nunca (ver alarmGivesUp).
	alarmRespawnMaxConsecutive = 5

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
	mu sync.Mutex
	// stop se cierra para pedir el término; done lo cierra la goroutine cuando ya
	// mató el árbol de sonido.
	//
	// Acá NO se guarda el `cancel` del contexto, y eso es el arreglo: cancelarlo
	// desde afuera despertaba a DOS goroutines con el mismo `ctx.Done()` — la que
	// Go registra dentro de `CommandContext`, que mata al padre, y la nuestra, que
	// mata el árbol. Si ganaba la primera, `KillProc` llegaba a un cadáver y se iba
	// sin tocar a los nietos: el bucle de sonido quedaba huérfano en PID 1 y no
	// paraba nunca. Medido en macOS el 2026-07-27 (PPID=1, PGID=pid del agente).
	stop chan struct{}
	done chan struct{}
	// epoch numera las alarmas para que la goroutine de una alarma vieja no
	// borre el `cancel` de la que la reemplazó. Sin esto: llega un segundo
	// `alarm`, StopAlarm despierta a la goroutine de la primera, y esa goroutine
	// —que corre en paralelo— limpia el cancel recién guardado por la segunda.
	// Resultado: una alarma sonando que `stopalarm` ya no puede detener.
	epoch uint64
}

// Tiempos de la vigilancia del reproductor.
//
// Son `var` y no `const` para que las pruebas puedan comprimirlos. El bucle de
// supervisión sólo se prueba de verdad esperando sus propias esperas, y con los
// valores de producción el camino de rendición —seis fallos seguidos con backoff
// que se duplica— tardaría más de un minuto por caso. Nada en producción los
// reasigna.
var (
	// alarmRespawnMinUptime es cuánto tiene que haber vivido un reproductor para
	// que su muerte NO cuente como fallo consecutivo. Distingue "alguien lo está
	// matando a mano" de "no arranca": lo segundo se repite en milisegundos.
	alarmRespawnMinUptime = 5 * time.Second

	// alarmRespawnBaseBackoff y alarmRespawnMaxBackoff acotan la espera entre
	// reintentos. Sin espera, un reproductor que falla al arrancar convertiría la
	// alarma en un bucle de spawn a toda velocidad.
	alarmRespawnBaseBackoff = time.Second
	alarmRespawnMaxBackoff  = 30 * time.Second
)

var runningAlarm alarmState

// alarmPlayer es el proceso que hace el ruido, con su `Wait` ya en curso.
//
// El Wait vive en una goroutine propia y su resultado se publica en `exited`
// porque hacen falta las dos cosas a la vez: cosechar el proceso (sin el Wait
// cada alarma dejaba un zombi) y poder ENTERARSE de que murió desde el `select`
// del supervisor. Un `cmd.Wait()` llamado en línea sólo da lo primero.
type alarmPlayer struct {
	cmd       *exec.Cmd
	exited    chan error
	startedAt time.Time
	// dead marca que su salida ya se consumió del canal. Sin esta marca el
	// teardown se quedaría esperando en `exited` un valor que nadie va a mandar,
	// y le sumaría la gracia entera de 3 s a cada apagado.
	dead bool
}

// alarmSpawner es el arranque del reproductor.
//
// Es una variable de paquete y no una llamada directa por una sola razón: que las
// pruebas puedan sustituirlo por un proceso trivial. El bucle de supervisión es la
// parte de la Fase 2 que más importa —es la que evita el silencio inadvertido— y no
// puede quedar sin cobertura porque en la máquina de pruebas no haya audio ni
// sesión gráfica.
var alarmSpawner = (*Agent).startAlarmPlayer

// startAlarmPlayer lanza el bucle de sonido en la sesión del usuario.
func (a *Agent) startAlarmPlayer(ctx context.Context) (*alarmPlayer, error) {
	cmd, err := a.alarmLoopCmd(ctx)
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	p := &alarmPlayer{
		cmd:       cmd,
		exited:    make(chan error, 1),
		startedAt: time.Now(),
	}

	// Buffer de 1 para que esta goroutine no quede colgada si nadie llega a leer
	// el canal (el caso del teardown, que puede matar sin volver al select).
	go func() { p.exited <- cmd.Wait() }()

	return p, nil
}

// stop mata el árbol de sonido y espera a que quede cosechado.
//
// Esperar la cosecha es parte del orden del teardown y no un detalle: el volumen
// se devuelve DESPUÉS de que el sonido murió, para que el último reproductor en
// vuelo no alcance a sonar al máximo.
func (p *alarmPlayer) stop(grace time.Duration) {
	if p == nil || p.dead {
		return
	}

	killUserSessionCmd(p.cmd)

	select {
	case <-p.exited:
	case <-time.After(grace):
	}
	p.dead = true
}

// alarmRespawnBackoff es la espera antes del reintento número `fails`.
//
// Crece al doble y se acota. El clamp se aplica ANTES del desplazamiento: con la
// alarma eterna `fails` no tiene techo, y `base << 40` no es una espera larga sino
// un desbordamiento —una duración negativa que `time.After` dispara de inmediato,
// devolviendo el bucle de spawn a toda velocidad que este backoff existe para
// evitar.
func alarmRespawnBackoff(fails int) time.Duration {
	if fails <= 1 {
		return alarmRespawnBaseBackoff
	}

	const maxShift = 6 // 1s << 6 = 64 s, ya por encima del techo
	if fails-1 >= maxShift {
		return alarmRespawnMaxBackoff
	}

	d := alarmRespawnBaseBackoff << (fails - 1)
	if d > alarmRespawnMaxBackoff {
		return alarmRespawnMaxBackoff
	}
	return d
}

// alarmGivesUp decide si hay que dejar de reintentar.
//
// La alarma ETERNA no se rinde nunca, y es a propósito: su caso de uso es un
// equipo robado, así que preferimos reintentar cada 30 s indefinidamente antes que
// quedarnos callados porque alguien mató el proceso unas cuantas veces. El costo es
// un spawn cada medio minuto. La alarma con duración sí se rinde: su plazo la iba a
// terminar igual, y reintentar sin tope ahí sólo esconde un equipo sin audio.
func alarmGivesUp(forever bool, fails int) bool {
	if forever {
		return false
	}
	return fails > alarmRespawnMaxConsecutive
}

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

// PlayAlarm hace sonar una alarma en el equipo. Sirve para encontrar
// físicamente una máquina (el caso antirrobo de Prey).
//
// El sonido es el propio del SO; el agente no empaqueta audio. La duración la
// controla este lado y no el script que suena: el comando por SO reproduce en
// bucle indefinidamente y acá lo matamos al vencer el plazo. Así un bucle que
// se cuelgue igual muere, y `StopAlarm` puede cortarlo antes.
//
// El reproductor además queda VIGILADO: si muere sin que nadie lo haya pedido, se
// relanza. Sin eso, matar un proceso —uno solo, en Windows— dejaba el equipo mudo
// con el volumen clavado al máximo y la consola creyendo que la alarma sonaba.
func (a *Agent) PlayAlarm(opts AlarmOptions) error {
	return a.playAlarmWithSavedVolume(opts, volumeState{}, audioOutput{})
}

// playAlarmWithSavedVolume es PlayAlarm con un estado de volumen ya conocido.
//
// `resumed` sólo trae valor cuando se rearma una alarma eterna tras un reinicio
// (ver ResumeAlarm). En ese caso NO se vuelve a leer el volumen del sistema: el
// equipo sigue al máximo desde antes del reinicio —Windows y macOS conservan el
// volumen del dispositivo— y una lectura nueva guardaría ese 100 % como si fuera
// la preferencia del usuario, perdiendo el valor real para siempre.
func (a *Agent) playAlarmWithSavedVolume(opts AlarmOptions, resumed volumeState, resumedOutput audioOutput) error {
	// El tope 5-300 s se conserva como el camino normal. La alarma eterna es la
	// excepción explícita para el caso de robo, y por eso saltea el clamp en vez
	// de codificarse como un valor especial de `Seconds`.
	if !opts.Forever {
		if opts.Seconds <= 0 {
			opts.Seconds = alarmDefaultSeconds
		}
		if opts.Seconds > alarmMaxSeconds {
			opts.Seconds = alarmMaxSeconds
		}
	}

	// Cualquier alarma anterior se detiene antes de arrancar la nueva.
	a.StopAlarm()

	ctx, cancel := context.WithCancel(context.Background())

	// ⚠️ La salida se mueve ANTES de lanzar el reproductor, al revés que el
	// volumen: un flujo de audio ya abierto se queda en el dispositivo con el que
	// nació, así que cambiarla después movería los sonidos del sistema y dejaría
	// la alarma sonando igual en los audífonos.
	//
	// El motivo está medido: la alarma sonó al 100 % en los AirPods del dueño del
	// equipo. En audífonos el sonido máximo no ayuda a encontrar nada y sí puede
	// lastimar a quien los lleva.
	output := resumedOutput
	if !output.Known {
		output = a.switchOutputToInternalSpeakers()
	}

	player, err := alarmSpawner(a, ctx)
	if err != nil {
		// Si el reproductor no arrancó, la salida movida no le sirve a nadie: se
		// devuelve acá mismo en vez de esperar a un teardown que no va a ocurrir.
		a.restoreAudioOutput(output)
		cancel()
		return err
	}

	// El volumen se toca DESPUÉS de que el proceso de sonido arrancó: si no
	// arrancó no hay nada que subirle el volumen a, y subirlo igual dejaría un
	// equipo al máximo y en silencio.
	//
	// Que no se pueda controlar el volumen NO cancela la alarma: suena al volumen
	// que tenga el usuario, que es el comportamiento previo a esta fase. Un
	// equipo que se está buscando debe sonar aunque el mezclador no coopere.
	saved := resumed
	if opts.MaxVolume {
		if saved.Known {
			if err := a.setVolume(maxVolumeState); err != nil {
				a.Logger.Debugln("PlayAlarm: no se pudo reafirmar el volumen máximo al rearmar:", err)
			}
		} else {
			saved = a.raiseVolumeForAlarm()
		}
	}

	stop := make(chan struct{})
	done := make(chan struct{})

	runningAlarm.mu.Lock()
	runningAlarm.epoch++
	myEpoch := runningAlarm.epoch
	runningAlarm.stop = stop
	runningAlarm.done = done
	// Se persiste bajo el mismo mutex que la época, para que la goroutine de una
	// alarma que agotó su gracia de 3 s no pise el archivo de la que ya la
	// reemplazó.
	if opts.Forever || saved.Known || output.Known {
		a.saveAlarmState(persistedAlarm{
			Forever:   opts.Forever,
			MaxVolume: opts.MaxVolume,
			Volume:    saved,
			Output:    output,
		})
	}
	runningAlarm.mu.Unlock()

	go func() {
		defer close(done)

		// Sin duración no hay temporizador: un canal nil en un `select` bloquea
		// para siempre, que es exactamente lo que "eterna" significa acá.
		var timerC <-chan time.Time
		if !opts.Forever {
			timer := time.NewTimer(time.Duration(opts.Seconds) * time.Second)
			defer timer.Stop()
			timerC = timer.C
		}

		// Reafirmación del volumen: si la persona lo baja o silencia el equipo, se
		// le vuelve a subir. Sin esto, bajar el volumen desactiva la alarma sin
		// permiso. Mismo comportamiento que Prey, con la diferencia de que
		// nosotros sí devolvemos el valor previo al terminar.
		var reassertC <-chan time.Time
		if opts.MaxVolume && saved.Known {
			ticker := time.NewTicker(volumeReassertInterval)
			defer ticker.Stop()
			reassertC = ticker.C
		}

		// fails cuenta fallos CONSECUTIVOS, no totales: un reproductor que alcanzó a
		// vivir alarmRespawnMinUptime lo devuelve a cero. Así "alguien lo mata cada
		// tanto" no termina agotando los reintentos, que es lo que pasaría con un
		// contador acumulado en una alarma larga.
		fails := 0

	loop:
		for {
			select {
			case <-timerC:
				a.Logger.Debugln("PlayAlarm: se cumplió la duración, deteniendo")
				break loop
			case <-stop:
				a.Logger.Debugln("PlayAlarm: detenida antes de tiempo")
				break loop
			case <-reassertC:
				if err := a.setVolume(maxVolumeState); err != nil {
					a.Logger.Debugln("PlayAlarm: no se pudo reafirmar el volumen máximo:", err)
				}
			case <-player.exited:
				// El reproductor murió y nadie lo pidió. Se avisa en Error y no en
				// Debug: en un equipo que se está buscando, "dejó de sonar sin que
				// nadie lo detuviera" es justo lo que hay que poder leer después.
				player.dead = true

				if time.Since(player.startedAt) < alarmRespawnMinUptime {
					fails++
				} else {
					fails = 0
				}

				a.Logger.Errorln("PlayAlarm: el reproductor murió sin que se lo pidiera",
					"(vivió", time.Since(player.startedAt).Round(time.Millisecond),
					", fallos seguidos:", fails, ") — relanzando")

				next, ok := a.respawnAlarmPlayer(ctx, opts, stop, timerC, &fails)
				if !ok {
					break loop
				}
				player = next
			}
		}

		// Teardown. El orden de los cuatro pasos está elegido para que cualquier
		// corte a mitad deje el equipo en el estado menos malo posible:
		//
		//  1. Desarmar la resurrección ANTES de matar nada, conservando el volumen
		//     pendiente. Si el equipo se cae acá, al arrancar no vuelve a sonar
		//     pero sí recupera el volumen.
		//  2. Matar el árbol con el padre TODAVÍA VIVO y sólo después cancelar el
		//     contexto — el arreglo de `e192d3c`. Al revés, el vigilante de
		//     `CommandContext` mata al padre primero y el bucle de sonido sobrevive
		//     huérfano.
		//  3. Devolver el volumen DESPUÉS de que el sonido murió, para que el
		//     último `afplay` en vuelo no alcance a sonar al máximo.
		//  4. Recién entonces el archivo de estado deja de hacer falta.
		a.finishAlarm(myEpoch, player, cancel, opts, saved, output)
	}()

	return nil
}

// respawnAlarmPlayer espera y vuelve a lanzar el reproductor. Devuelve false
// cuando hay que terminar la alarma: porque se rindió, porque la detuvieron o
// porque venció el plazo mientras esperaba.
//
// El reintento es un bucle propio y no una vuelta del `select` de arriba por un
// detalle que importa: si el arranque falla, el canal `exited` del reproductor
// muerto ya se consumió y volver al `select` dejaría esa rama esperando un valor
// que nadie va a mandar. Una alarma eterna se quedaría colgada ahí, en silencio.
func (a *Agent) respawnAlarmPlayer(ctx context.Context, opts AlarmOptions,
	stop <-chan struct{}, timerC <-chan time.Time, fails *int) (*alarmPlayer, bool) {

	for {
		if alarmGivesUp(opts.Forever, *fails) {
			a.Logger.Errorln("PlayAlarm: se agotaron los reintentos tras", *fails,
				"fallos seguidos; la alarma se termina y se devuelve el volumen")
			return nil, false
		}

		// La espera es interrumpible. Con un `time.Sleep`, un `stopalarm` que llegara
		// en medio del backoff se quedaría esperando hasta 30 s y StopAlarm
		// reportaría que la alarma no confirmó su término.
		select {
		case <-stop:
			a.Logger.Debugln("PlayAlarm: detenida mientras esperaba para relanzar")
			return nil, false
		case <-timerC:
			a.Logger.Debugln("PlayAlarm: se cumplió la duración mientras esperaba para relanzar")
			return nil, false
		case <-time.After(alarmRespawnBackoff(*fails)):
		}

		next, err := alarmSpawner(a, ctx)
		if err == nil {
			a.Logger.Infoln("PlayAlarm: reproductor relanzado")
			return next, true
		}

		*fails++
		a.Logger.Errorln("PlayAlarm: no se pudo relanzar el reproductor:", err.Error())
	}
}

// finishAlarm es el teardown de la alarma. Vive aparte para que el orden de los
// pasos sea legible de corrido y no quede sepultado en el `select`.
func (a *Agent) finishAlarm(epoch uint64, player *alarmPlayer, cancel context.CancelFunc,
	opts AlarmOptions, saved volumeState, output audioOutput) {

	// (1) Desarmar la resurrección. Sólo si esta alarma sigue siendo la vigente:
	// si otra la reemplazó, el archivo ya es de ella.
	if opts.Forever {
		runningAlarm.mu.Lock()
		if runningAlarm.epoch == epoch {
			a.saveAlarmState(persistedAlarm{
				Forever:   false,
				MaxVolume: opts.MaxVolume,
				Volume:    saved,
			})
		}
		runningAlarm.mu.Unlock()
	}

	// (2) Matar el árbol con el padre TODAVÍA VIVO y sólo después cancelar el
	// contexto. La cosecha va incluida en `stop`: el `Wait` ya corre en su propia
	// goroutine desde que el reproductor arrancó, así que acá no hay que llamarlo
	// de nuevo —hacerlo dos veces sobre el mismo `cmd` devuelve error y no cosecha
	// nada.
	player.stop(alarmStopGrace)
	cancel()

	// (3) y (4).
	runningAlarm.mu.Lock()
	current := runningAlarm.epoch == epoch
	if current {
		runningAlarm.stop = nil
		runningAlarm.done = nil
	}
	runningAlarm.mu.Unlock()

	if !current {
		return
	}

	a.restoreSavedVolume(saved)
	a.restoreAudioOutput(output)
	a.clearAlarmState()
}

// StopAlarm detiene la alarma en curso, si hay una. Es idempotente: llamarla sin
// alarma sonando no es un error.
func (a *Agent) StopAlarm() {
	runningAlarm.mu.Lock()
	stop, done := runningAlarm.stop, runningAlarm.done
	runningAlarm.stop, runningAlarm.done = nil, nil
	runningAlarm.mu.Unlock()

	if stop == nil {
		return
	}

	// Sacarlos del estado bajo el mutex garantiza que sólo un llamado cierra el
	// canal, así que no hace falta proteger el close de un doble cierre.
	close(stop)

	select {
	case <-done:
	case <-time.After(alarmStopGrace):
		a.Logger.Debugln("StopAlarm: la alarma no confirmó su término en", alarmStopGrace)
	}
}

// natsBoolFlag lee una bandera del payload NATS, que viaja como texto porque
// `NatsMsg.Data` es `map[string]string` (igual que `duration`).
//
// Falla cerrado a propósito: cualquier valor que no sea un "verdadero"
// reconocible se lee como falso. Las dos banderas de la Fase 2 —sonido máximo y
// alarma eterna— son las más peligrosas de la feature, y un payload que no se
// entiende no debe activarlas.
func natsBoolFlag(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
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
