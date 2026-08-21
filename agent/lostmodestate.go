package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// Feature 030 (ADR-025) · estado de "equipo perdido/robado" que sobrevive al
// proceso y al reinicio del equipo.
//
// Un equipo robado se reinicia —lo apaga quien lo tiene, o se queda sin
// batería—, así que un estado que sólo viviera en memoria se perdería justo en
// el escenario para el que existe la feature. Mismo razonamiento que la alarma
// eterna de la 028, y por eso este archivo es su espejo deliberado.
//
// Hay DOS canales para enterarse del estado, y ninguno alcanza solo:
//
//  1. El push por NATS (`case "lost_mode"` en rpc.go), inmediato pero inútil si
//     el equipo estaba apagado cuando lo marcaron.
//  2. El polling de config (`/api/v3/<id>/config/`, campo `lost_mode`), que es
//     el canal GARANTIZADO: el agente lo consulta en cada tick de geo y así se
//     entera al reconectar, sin depender de que el push le haya llegado.
//
// El archivo en disco es lo que une los dos: mantiene el estado entre reinicios
// mientras el equipo no puede hablar con el servidor.
//
// ⚠️ Consecuencia asumida (decisión del usuario, 2026-08-11): NO hay fusible por
// falta de contacto. Un agente que jamás vuelve a hablar con el servidor sigue
// capturando indefinidamente. Es deliberado —el caso de uso es un equipo
// robado—, no un olvido.

const (
	// Topes de cadencia, en minutos. Duplican a propósito los del servidor
	// (LOST_MODE_MIN/MAX_INTERVAL_MIN): el intervalo llega por DOS caminos —el
	// push por NATS y el polling de config— y el piso no puede depender de que
	// el que lo mandó lo haya acotado. Un 0 sería captura continua: mata la
	// batería y delata al agente frente a quien tiene el equipo.
	lostModeMinIntervalMin = 1
	lostModeMaxIntervalMin = 60
	// El default coincide con el del modelo del servidor. Se usa cuando el
	// intervalo llega ausente o ilegible.
	lostModeDefaultIntervalMin = 5
)

// lostModeCascade es la cascada de contramedidas de la feature 038, ya RESUELTA
// por el servidor (precedencia incidente > equipo > global). Llega por los dos
// canales —push por NATS y polling de config— y el agente sólo la ejecuta.
type lostModeCascade struct {
	AutoLock       bool
	LockDelayMin   int
	NoHibernate    bool
	WebcamOverride bool
	Alarm          bool
}

// persistedLostMode es lo que queda en disco entre ejecuciones.
type persistedLostMode struct {
	Active bool `json:"active"`
	// En MINUTOS, igual que el campo del servidor. La unidad va en el nombre a
	// propósito: el valor viaja por dos canales distintos y un malentendido de
	// minutos/segundos daría una cadencia 60 veces mayor o menor sin ningún
	// síntoma visible.
	IntervalMin int `json:"interval_min"`
	// Feature 038: la cascada se persiste para poder REVERTIRLA tras un reinicio.
	// Sin esto, un equipo con la no-hibernación aplicada que se reinicia perdería
	// la referencia para restaurar el esquema de energía al recuperarlo.
	AutoLock       bool `json:"auto_lock,omitempty"`
	LockDelayMin   int  `json:"lock_delay_min,omitempty"`
	NoHibernate    bool `json:"no_hibernate,omitempty"`
	WebcamOverride bool `json:"webcam_override,omitempty"`
	Alarm          bool `json:"alarm,omitempty"`
}

// Estado en memoria. Va en variables de paquete y no en el struct Agent porque
// un mutex dentro de Agent lo volvería no copiable (`go vet` copylocks), y
// porque hay un solo agente por proceso. Mismo criterio que `syncMeshRunning`
// en svc.go.
var (
	lostModeMu          sync.RWMutex
	lostModeActive      bool
	lostModeIntervalMin = lostModeDefaultIntervalMin
	// Feature 038: cascada vigente en memoria. La fijan los dos canales
	// (setLostModeCascade) antes de llamar a SetLostMode, y el bucle del
	// servicio la lee al cruzar una transición para aplicar/revertir.
	lostModeCascadeVal lostModeCascade
)

// lostModeChanged avisa al bucle del servicio que el estado del modo perdido
// cambió, para que re-arme el ticker de geo SIN esperar al próximo tick.
//
// Sin este aviso, el push por NATS fijaba el estado al instante y la cadencia del
// caso recién empezaba en el tick normal siguiente: medido en terreno el
// 2026-08-11, 3 min 20 s en un ambiente con `checkin_geo` de 300 s, y 25 a 35 min
// con el valor por omisión del producto — con el equipo ENCENDIDO y en línea.
//
// Buffer de 1 y envío no bloqueante a propósito: `SetLostMode` puede llamarse
// desde la goroutine del push mientras el bucle está ocupado, y un canal sin
// buffer la dejaría colgada. Si ya hay un aviso pendiente, este se descarta sin
// pérdida: el consumidor lee el ESTADO ACTUAL con getLostMode(), no un valor que
// viaje por el canal.
var lostModeChanged = make(chan struct{}, 1)

func signalLostModeChanged() {
	select {
	case lostModeChanged <- struct{}{}:
	default:
	}
}

// clampLostModeInterval acota el intervalo en los dos extremos. Un valor
// ausente o ilegible (<= 0 tras el parseo) cae al default en vez de al piso:
// "no me dijeron" no es lo mismo que "me pidieron el mínimo".
func clampLostModeInterval(min int) int {
	if min <= 0 {
		return lostModeDefaultIntervalMin
	}
	if min < lostModeMinIntervalMin {
		return lostModeMinIntervalMin
	}
	if min > lostModeMaxIntervalMin {
		return lostModeMaxIntervalMin
	}
	return min
}

// lostModeDataDir es el directorio del archivo de estado.
//
// Duplica la rama de `alarmDataDir()` en vez de reusarla: son dos features con
// ciclos de vida distintos y encadenarlas haría que mover el directorio de una
// arrastrara a la otra sin que nadie lo note. En unix es `/opt/observeragent`,
// que el desinstalador borra con RemoveAll —un archivo capaz de reactivar la
// captura de evidencia no debe sobrevivir a la desinstalación—; en Windows es
// `%PROGRAMDATA%\ObserverRMM`, que pese al nombre del campo (WinTmpDir) no es
// un temporal del sistema y persiste entre reinicios.
func (a *Agent) lostModeDataDir() string {
	if lostModeDirOverride != "" {
		return lostModeDirOverride
	}
	if runtime.GOOS == "windows" {
		return a.WinTmpDir
	}
	return nixAgentDir
}

// lostModeDirOverride es la costura para las pruebas. El directorio real
// (`/opt/observeragent`) no es escribible en CI, y la persistencia de este
// estado es justamente lo que hay que probar: sin costura, la parte de la
// feature que sobrevive al reinicio quedaría sin una sola prueba automática —
// que es como la 028 llegó al terreno con ocho "ok falso".
var lostModeDirOverride string

func (a *Agent) lostModeStatePath() string {
	return filepath.Join(a.lostModeDataDir(), "lost_mode.json")
}

// saveLostModeState escribe el estado de forma atómica: temporal + rename.
//
// Un corte a mitad de un `os.WriteFile` dejaría un JSON truncado, y un JSON
// ilegible se comporta como "no había nada" — o sea, un equipo marcado como
// perdido que al arrancar cree que no lo está.
func (a *Agent) saveLostModeState(st persistedLostMode) {
	path := a.lostModeStatePath()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		a.Logger.Debugln("saveLostModeState: no se pudo crear el directorio:", err)
		return
	}

	data, err := json.Marshal(st)
	if err != nil {
		a.Logger.Debugln("saveLostModeState: no se pudo serializar:", err)
		return
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		a.Logger.Debugln("saveLostModeState: no se pudo escribir:", err)
		return
	}

	if err := os.Rename(tmp, path); err != nil {
		a.Logger.Debugln("saveLostModeState: no se pudo renombrar:", err)
		_ = os.Remove(tmp)
	}
}

// loadLostModeState lee el estado del disco. El segundo valor es false cuando
// no hay nada que rearmar: archivo ausente, ilegible, o un caso ya recuperado.
func (a *Agent) loadLostModeState() (persistedLostMode, bool) {
	var st persistedLostMode

	data, err := os.ReadFile(a.lostModeStatePath())
	if err != nil {
		return st, false
	}

	if err := json.Unmarshal(data, &st); err != nil {
		a.Logger.Debugln("loadLostModeState: JSON ilegible, se descarta:", err)
		a.clearLostModeState()
		return st, false
	}

	if !st.Active {
		return st, false
	}

	return st, true
}

func (a *Agent) clearLostModeState() {
	if err := os.Remove(a.lostModeStatePath()); err != nil && !os.IsNotExist(err) {
		a.Logger.Debugln("clearLostModeState:", err)
	}
}

// getLostMode devuelve el estado vigente en memoria.
func (a *Agent) getLostMode() (bool, int) {
	lostModeMu.RLock()
	defer lostModeMu.RUnlock()
	return lostModeActive, lostModeIntervalMin
}

// setLostModeCascade fija la cascada resuelta en memoria. La llaman los dos
// canales (push y config) JUSTO ANTES de SetLostMode, para que la persistencia
// del estado incluya la cascada y el bucle la lea al cruzar la transición.
func (a *Agent) setLostModeCascade(c lostModeCascade) {
	lostModeMu.Lock()
	lostModeCascadeVal = c
	lostModeMu.Unlock()
}

// getLostModeCascade devuelve la cascada vigente en memoria.
func (a *Agent) getLostModeCascade() lostModeCascade {
	lostModeMu.RLock()
	defer lostModeMu.RUnlock()
	return lostModeCascadeVal
}

// SetLostMode fija el estado en memoria y lo persiste. Es idempotente y la
// llaman los dos canales: el push por NATS y el polling de config.
//
// Los errores de disco se loguean y se tragan, igual que en ResumeAlarm: el
// estado en memoria ya quedó bien y negarse a operar porque no se pudo escribir
// un archivo sería peor que perder la persistencia.
func (a *Agent) SetLostMode(active bool, intervalMin int) {
	interval := clampLostModeInterval(intervalMin)

	lostModeMu.Lock()
	cambio := lostModeActive != active || lostModeIntervalMin != interval
	lostModeActive = active
	lostModeIntervalMin = interval
	lostModeMu.Unlock()

	if !cambio {
		return
	}

	// El aviso va ANTES de tocar el disco: lo que el bucle necesita ya está en
	// memoria, y una escritura lenta —o fallida— no debe retrasar el cambio de
	// cadencia. El estado en disco es para sobrevivir al reinicio, no para esto.
	signalLostModeChanged()

	if active {
		a.Logger.Infoln("SetLostMode: equipo marcado como PERDIDO, cadencia", interval, "min")
		c := a.getLostModeCascade()
		a.saveLostModeState(persistedLostMode{
			Active:         true,
			IntervalMin:    interval,
			AutoLock:       c.AutoLock,
			LockDelayMin:   c.LockDelayMin,
			NoHibernate:    c.NoHibernate,
			WebcamOverride: c.WebcamOverride,
			Alarm:          c.Alarm,
		})
		return
	}

	a.Logger.Infoln("SetLostMode: equipo RECUPERADO, se detiene la captura intensiva")
	// Se borra el archivo en vez de escribir Active=false: el estado por omisión
	// es "no está perdido", así que la ausencia del archivo ya lo dice, y no deja
	// en el equipo un rastro de que alguna vez se lo marcó.
	a.clearLostModeState()
}

// ResumeLostMode rearma el estado que dejó una ejecución anterior. Se llama una
// vez, al arrancar el servicio, junto a ResumeAlarm.
//
// Sin esto, reiniciar el equipo —o el servicio— apagaría la captura hasta que
// llegara el próximo push o el próximo tick de config, y en un equipo robado
// ese "hasta que" puede no llegar nunca.
func (a *Agent) ResumeLostMode() {
	st, ok := a.loadLostModeState()
	if !ok {
		return
	}

	interval := clampLostModeInterval(st.IntervalMin)

	lostModeMu.Lock()
	lostModeActive = true
	lostModeIntervalMin = interval
	// Feature 038: se restaura también la cascada, para poder REVERTIR las
	// contramedidas (p. ej. la no-hibernación) cuando el equipo se recupere,
	// aunque de por medio haya habido un reinicio.
	lostModeCascadeVal = lostModeCascade{
		AutoLock:       st.AutoLock,
		LockDelayMin:   st.LockDelayMin,
		NoHibernate:    st.NoHibernate,
		WebcamOverride: st.WebcamOverride,
		Alarm:          st.Alarm,
	}
	lostModeMu.Unlock()

	a.Logger.Infoln("ResumeLostMode: el equipo seguía marcado como PERDIDO, cadencia", interval, "min")
}
