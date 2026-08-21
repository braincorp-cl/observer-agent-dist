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
//   - La alarma: es opt-in y, cuando la cascada la trae encendida, suena por el
//     canal de la 028 desde el ciclo de captura, no desde acá.

var (
	lostLockMu     sync.Mutex
	lostLockCancel chan struct{}
)

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
		go a.aplicarNoHibernacion()
	}
}

// revertirContramedidasModoPerdido corre al RECUPERAR. Cancela el bloqueo
// pendiente —recuperar el equipo dentro de la ventana silenciosa lo desactiva—
// y restaura el esquema de energía previo.
func (a *Agent) revertirContramedidasModoPerdido() {
	a.cancelarBloqueoModoPerdido()
	go a.revertirNoHibernacion()
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
