package agent

import (
	"sync"
	"time"
)

// Feature 038 · contramedidas de la cascada al marcar perdido/robado.
//
// El agente es el orquestador: el servidor RESUELVE la cascada (precedencia
// incidente > equipo > global) y la entrega por los dos canales; acá se EJECUTA
// al cruzar cada transición del modo perdido —lo detecta el bucle del servicio
// en svc.go—. Reutiliza primitivas ya existentes y verificadas: LockScreen()
// (misma acción del `case "lock"` de la 028) y la gestión de energía por SO.
//
// Qué NO vive acá:
//   - La geo intensiva y la captura de pantalla/webcam: ya las hace el ciclo del
//     modo perdido (feature 030). El override de webcam se resuelve en svc.go.
//
// Qué SÍ vive acá:
//   - La alarma antirrobo (RF-06): es opt-in en la cascada y, cuando la trae
//     encendida, se dispara desde este archivo con la primitiva de la 028
//     (PlayAlarm), NO desde el ciclo de captura. Antes del 2026-08-22 el flag
//     `Alarm` viajaba y se persistía pero nadie lo tocaba, así que un equipo
//     perdido con la alarma habilitada nunca sonaba; ese era el gap RF-06.

var (
	lostLockMu     sync.Mutex
	lostLockCancel chan struct{}

	// lostModeEnSegundoPlano decide si las contramedidas pesadas (alarma,
	// no-hibernación/-suspensión) corren en goroutine. En producción es true para
	// no bloquear el bucle del servicio en la transición; las pruebas lo ponen en
	// false para que la ejecución sea síncrona y no queden goroutines tocando
	// estado global entre tests.
	lostModeEnSegundoPlano = true
)

// enSegundoPlano corre f en goroutine (producción) o en línea (tests), según
// lostModeEnSegundoPlano.
func enSegundoPlano(f func()) {
	if lostModeEnSegundoPlano {
		go f()
	} else {
		f()
	}
}

// cascadeDeConfig arma la cascada resuelta a partir del config del polling.
// El push (rpc.go) la arma a mano desde su payload de texto.
func cascadeDeConfig(c AgentCheckInConfig) lostModeCascade {
	return lostModeCascade{
		AutoLock:       c.LostModeAutoLock,
		LockDelayMin:   c.LostModeLockDelayMin,
		NoHibernate:    c.LostModeNoHibernate,
		WebcamOverride: c.LostModeWebcamOverride,
		Alarm:          c.LostModeAlarm,
	}
}

// aplicarContramedidasModoPerdido corre al ENTRAR en modo perdido. Lee la
// cascada vigente en memoria y dispara lo que corresponda:
//   - bloqueo de sesión SILENCIOSO-DIFERIDO: se agenda para dentro de
//     LockDelayMin minutos, NO de inmediato — así el ciclo recolecta evidencia
//     antes de avisarle a quien tiene el equipo (decisión del usuario 2026-08-20).
//   - no-hibernación: se aplica y se guarda el estado previo para poder revertir.
func (a *Agent) aplicarContramedidasModoPerdido() {
	c := a.getLostModeCascade()

	if c.AutoLock {
		a.agendarBloqueoModoPerdido(c.LockDelayMin)
	}
	if c.NoHibernate {
		enSegundoPlano(a.aplicarNoHibernacion)
	}
	if c.Alarm {
		enSegundoPlano(a.dispararAlarmaModoPerdido)
	}
}

// dispararAlarmaModoPerdido hace sonar la alarma antirrobo como parte de la
// cascada (RF-06). A diferencia del camino manual de la 028 —donde el operador
// elige duración y volumen— acá ambos se fijan sin opción: volumen SIEMPRE al
// máximo y alarma ETERNA. Es una decisión del usuario del 2026-08-22: la alarma
// en modo perdido es de las últimas acciones de un caso de robo, no una molestia
// acotada, así que no se deja a criterio del caso. El máximo se reafirma solo
// cada segundo dentro de PlayAlarm; la eterna sobrevive a un reinicio (ver
// alarmstate.go). Se silencia al recuperar —StopAlarm en la reversa restaura el
// volumen previo y limpia el estado persistido—.
func (a *Agent) dispararAlarmaModoPerdido() {
	a.Logger.Infoln("modo perdido: disparando alarma antirrobo (volumen máximo, sin límite de tiempo)")
	if err := a.PlayAlarm(AlarmOptions{Forever: true, MaxVolume: true}); err != nil {
		a.Logger.Errorln("modo perdido PlayAlarm:", err.Error())
	}
}

// revertirContramedidasModoPerdido corre al RECUPERAR. Cancela el bloqueo
// pendiente —recuperar el equipo dentro de la ventana silenciosa lo desactiva—
// y restaura el esquema de energía previo.
func (a *Agent) revertirContramedidasModoPerdido() {
	a.cancelarBloqueoModoPerdido()
	// StopAlarm es incondicional a propósito: al recuperar el equipo, en memoria
	// la cascada ya se resolvió a "no perdido" (Alarm=false), así que no se puede
	// consultar el flag para saber si la cascada la había encendido. Y da igual:
	// recuperar un equipo = caso cerrado = silencio. StopAlarm es idempotente
	// (no-op si no hay alarma) y restaura el volumen previo + limpia el estado
	// persistido de la alarma eterna.
	enSegundoPlano(a.StopAlarm)
	enSegundoPlano(a.revertirNoHibernacion)
}

// agendarBloqueoModoPerdido programa el bloqueo de sesión para dentro de
// delayMin minutos, cancelando cualquier bloqueo pendiente anterior. delayMin=0
// bloquea de inmediato (sin ventana). Al vencer, sólo bloquea si el equipo
// SIGUE marcado como perdido: recuperarlo dentro de la ventana lo desactiva.
func (a *Agent) agendarBloqueoModoPerdido(delayMin int) {
	a.cancelarBloqueoModoPerdido()

	if delayMin < 0 {
		delayMin = 0
	}

	cancel := make(chan struct{})
	lostLockMu.Lock()
	lostLockCancel = cancel
	lostLockMu.Unlock()

	go func() {
		t := time.NewTimer(time.Duration(delayMin) * time.Minute)
		defer t.Stop()
		select {
		case <-t.C:
			if activo, _ := a.getLostMode(); !activo {
				return
			}
			a.Logger.Infoln("modo perdido: venció la ventana silenciosa, bloqueando la sesión")
			if err := a.LockScreen(); err != nil {
				a.Logger.Errorln("modo perdido LockScreen:", err.Error())
			}
		case <-cancel:
			a.Logger.Debugln("modo perdido: bloqueo pendiente cancelado (equipo recuperado)")
		}
	}()
}

// cancelarBloqueoModoPerdido cancela un bloqueo diferido pendiente si lo hay.
func (a *Agent) cancelarBloqueoModoPerdido() {
	lostLockMu.Lock()
	defer lostLockMu.Unlock()
	if lostLockCancel != nil {
		close(lostLockCancel)
		lostLockCancel = nil
	}
}
