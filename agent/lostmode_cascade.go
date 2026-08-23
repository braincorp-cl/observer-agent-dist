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

// Feature 041 · estado "fuera de geocerca" en memoria. Es la SEGUNDA fuente de la
// no-hibernación (la primera es la cascada del modo perdido). Va en variable de
// paquete con su propio mutex, igual que el estado del modo perdido y por el mismo
// motivo (un mutex dentro de Agent lo volvería no copiable; hay un solo agente por
// proceso). Lo escribe el bucle de poll (T015) y lo leen, desde otras goroutines,
// la reconciliación de no-dormir (acá) y el gate de forceLocationOn (osLocation).
var (
	outsideGeofenceMu  sync.RWMutex
	outsideGeofenceVal bool

	// noHibernacionMu serializa la reconciliación de no-dormir. Feature 041
	// (T015) la llama desde tres goroutines —arranque, tick de geo y transición
	// de modo perdido—; sin este cerrojo, dos reconciliaciones concurrentes
	// podrían leer "no aplicada" a la vez y duplicar el esquema de energía en
	// Windows (aplicar/revertir sólo se protegen por existencia de archivo, no
	// por mutex). El cerrojo vuelve atómico el read-compute-apply.
	noHibernacionMu sync.Mutex

	// noHibernacionUltimoDeseado recuerda el último estado deseado de la no-dormir
	// reactiva para loguear la FUENTE (RF-11 / T019) SÓLO en las transiciones, no
	// en cada poll: la reconciliación corre en cada pasada y sería ruido. nil = aún
	// desconocido (primer arranque del proceso). Va bajo noHibernacionMu.
	noHibernacionUltimoDeseado *bool
)

func (a *Agent) setOutsideGeofence(v bool) {
	outsideGeofenceMu.Lock()
	outsideGeofenceVal = v
	outsideGeofenceMu.Unlock()
}

func (a *Agent) getOutsideGeofence() bool {
	outsideGeofenceMu.RLock()
	defer outsideGeofenceMu.RUnlock()
	return outsideGeofenceVal
}

// reconciliarNoHibernacion recomputa el estado DESEADO de no-dormir a partir de
// TODAS sus fuentes y lo aplica de forma idempotente:
//
//	deseado = (modo perdido activo Y la cascada pide no-hibernar) OR (fuera de geocerca)
//
// RN-03 (invariante multi-fuente): la no-hibernación sólo se REVIERTE cuando
// NINGUNA fuente la pide. Recuperar el equipo de "perdido" estando aún fuera de la
// geocerca MANTIENE el equipo despierto; sólo al desaparecer las dos fuentes se
// restaura el esquema de energía. `aplicarNoHibernacion`/`revertirNoHibernacion`
// ya son idempotentes (guardan estado en disco), así que llamarlo por poll o por
// transición no encadena copias ni re-duplica el inhibidor.
func (a *Agent) reconciliarNoHibernacion() {
	noHibernacionMu.Lock()
	defer noHibernacionMu.Unlock()

	activo, _ := a.getLostMode()
	lostNoHib := a.getLostModeCascade().NoHibernate
	lostPide := activo && lostNoHib
	geoPide := a.getOutsideGeofence()
	deseado := noHibernacionDeseada(activo, lostNoHib, geoPide)

	// RF-11 / T019 · logging por fuente. Se emite sólo al cambiar el estado
	// deseado —no en cada poll— para que el log distinga la fuente (geofence /
	// lost_mode) en el apply y en el revert sin inundar la bitácora. El apply/
	// revert por-SO ya loguea el MECANISMO (inhibidor systemd, esquema powercfg);
	// esta línea agrega la ATRIBUCIÓN de origen que pide RF-11.
	if noHibernacionUltimoDeseado == nil || *noHibernacionUltimoDeseado != deseado {
		if deseado {
			a.Logger.Infoln("no-dormir reactivo: aplicar (fuente:", fuentesNoHibernacion(lostPide, geoPide)+")")
		} else {
			a.Logger.Infoln("no-dormir reactivo: revertir (ninguna fuente reactiva la pide)")
		}
		d := deseado
		noHibernacionUltimoDeseado = &d
	}

	if deseado {
		a.aplicarNoHibernacion()
		return
	}
	a.revertirNoHibernacion()
}

// fuentesNoHibernacion arma la etiqueta de origen para el log de RF-11: qué
// fuente(s) reactiva(s) sostienen la no-dormir en este instante. El baseline
// persistente NO aparece acá —lo loguea baseline.go con su propia etiqueta—
// porque no pasa por esta reconciliación.
func fuentesNoHibernacion(lost, geo bool) string {
	switch {
	case lost && geo:
		return "lost_mode+geofence"
	case lost:
		return "lost_mode"
	case geo:
		return "geofence"
	default:
		return "ninguna"
	}
}

// noHibernacionDeseada es el predicado puro de la reconciliación: el estado
// deseado de no-dormir dado el estado de sus dos fuentes. Se extrae de la acción
// (aplicar/revertir toca el SO) para poder congelar la invariante RN-03 como una
// tabla de verdad, sin systemd ni powercfg de por medio:
//
//	deseado = (perdido Y la cascada pide no-hibernar) OR (fuera de geocerca)
//
// La consecuencia clave —recuperar de "perdido" estando aún fuera MANTIENE el
// equipo despierto— es la fila (lostActivo=false, outsideGeofence=true) ⇒ true.
func noHibernacionDeseada(lostActivo, lostNoHibernate, outsideGeofence bool) bool {
	return (lostActivo && lostNoHibernate) || outsideGeofence
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
	// Feature 041: la no-hibernación es multi-fuente (modo perdido OR geocerca), así
	// que se resuelve por reconciliación en vez de aplicarse a secas. Entrar en modo
	// perdido con la cascada pidiéndola la aplica; si además el equipo ya estaba
	// despierto por geocerca, la reconciliación es idempotente y no la re-duplica.
	enSegundoPlano(a.reconciliarNoHibernacion)
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
	// Feature 041 · RN-03: NO se revierte a secas. Si el equipo sigue fuera de la
	// geocerca, recuperarlo de "perdido" debe MANTENERLO despierto; la reconciliación
	// revierte sólo cuando ninguna fuente pide no-dormir. En este punto el estado de
	// modo perdido ya se resolvió a "no perdido" (SetLostMode corrió antes de avisar
	// al bucle), así que la reconciliación lee la geocerca como única fuente posible.
	enSegundoPlano(a.reconciliarNoHibernacion)
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
