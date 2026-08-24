//go:build linux || darwin

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// Feature 038 · esqueleto compartido de la no-suspensión REACTIVA en unix
// (Linux T011 + macOS T010).
//
// El mecanismo unix es el mismo en ambos SO: se sostiene vivo un proceso
// inhibidor mientras dure el caso (Linux → `systemd-inhibit`; macOS →
// `caffeinate`) y el revert es matar ese proceso. NO se toca ninguna
// configuración persistente del sistema —a diferencia del baseline keep-awake
// (`pmset`/registro), que es otra capa—, así que el revert es un simple kill sin
// perillas que restaurar. Lo único que cambia por SO es CÓMO se lanza el
// inhibidor: eso vive en `defaultStartLostInhibitor` de cada archivo por-SO
// (`lostmode_power_linux.go`, `lostmode_power_darwin.go`). Todo lo demás —estado
// persistido, idempotencia, limpieza de huérfano y revert— es común y vive acá.
//
// El PID se guarda en disco por dos motivos: idempotencia (no encadenar
// inhibidores si la transición se re-dispara) y limpieza de un huérfano si el
// agente murió sucio y el hijo quedó reparentado a init/launchd. Tras un reinicio
// limpio el hijo muere con el agente y el inhibidor se libera, pero el bucle del
// servicio vuelve a llamar aplicarNoHibernacion al re-detectar el modo perdido en
// el arranque, así que el inhibidor se restablece solo.

// lostPowerState es lo que se persiste para poder revertir y ser idempotente.
type lostPowerState struct {
	InhibitPID int `json:"inhibit_pid"`
}

var (
	lostInhibitMu sync.Mutex

	// Costuras de prueba: en producción lanzan/consultan/matan el proceso real;
	// en los tests se sustituyen por dobles para no depender de systemd/caffeinate.
	// `defaultStartLostInhibitor` lo define cada archivo por-SO.
	startLostInhibitor = defaultStartLostInhibitor
	lostInhibitorAlive = pidAlive
	killLostInhibitor  = killProcessGroup
)

func (a *Agent) lostPowerStatePath() string {
	return filepath.Join(a.lostModeDataDir(), "lost_power.json")
}

// aplicarNoHibernacion lanza el inhibidor de suspensión. Es idempotente: si ya
// hay uno vivo registrado (p. ej. tras re-disparar la transición), no lanza otro.
func (a *Agent) aplicarNoHibernacion() {
	lostInhibitMu.Lock()
	defer lostInhibitMu.Unlock()

	if st, ok := a.readLostPowerState(); ok {
		if lostInhibitorAlive(st.InhibitPID) {
			a.Logger.Debugln("no-suspensión: inhibidor ya activo (pid", st.InhibitPID, "), no se relanza")
			return
		}
		// PID guardado pero muerto: agente reiniciado sucio. Se limpia y se sigue.
		a.Logger.Debugln("no-suspensión: el inhibidor previo (pid", st.InhibitPID, ") ya no vive; se relanza")
		_ = os.Remove(a.lostPowerStatePath())
	}

	pid, err := startLostInhibitor(a)
	if err != nil {
		a.Logger.Errorln("no-suspensión: no se pudo lanzar el inhibidor:", err.Error())
		return
	}

	data, _ := json.Marshal(lostPowerState{InhibitPID: pid})
	if err := os.WriteFile(a.lostPowerStatePath(), data, 0600); err != nil {
		// El inhibidor ya corre; no poder persistir el PID sólo compromete la
		// idempotencia y la limpieza de huérfano, no la no-suspensión en sí.
		a.Logger.Errorln("no-suspensión: no se pudo guardar el estado (pid", pid, "):", err.Error())
	}
	a.Logger.Infoln("no-suspensión aplicada (inhibidor pid", pid, ")")
}

// revertirNoHibernacion mata el inhibidor y borra el estado. Ausencia de estado
// = nada que revertir: es idempotente y silencioso.
func (a *Agent) revertirNoHibernacion() {
	lostInhibitMu.Lock()
	defer lostInhibitMu.Unlock()

	st, ok := a.readLostPowerState()
	if !ok {
		return
	}

	if st.InhibitPID != 0 && lostInhibitorAlive(st.InhibitPID) {
		if err := killLostInhibitor(st.InhibitPID); err != nil {
			a.Logger.Debugln("no-suspensión: no se pudo matar el inhibidor pid", st.InhibitPID, ":", err.Error())
		}
	}
	_ = os.Remove(a.lostPowerStatePath())
	a.Logger.Infoln("no-suspensión revertida (inhibidor pid", st.InhibitPID, "liberado)")
}

func (a *Agent) readLostPowerState() (lostPowerState, bool) {
	var st lostPowerState
	data, err := os.ReadFile(a.lostPowerStatePath())
	if err != nil {
		return st, false
	}
	if err := json.Unmarshal(data, &st); err != nil {
		a.Logger.Debugln("no-suspensión: estado ilegible, se descarta:", err.Error())
		_ = os.Remove(a.lostPowerStatePath())
		return st, false
	}
	return st, true
}

// pidAlive comprueba si un PID sigue vivo sin tocar el proceso (Signal 0).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// killProcessGroup mata el grupo del inhibidor (para llevarse el proceso hijo).
// Si no hubiera grupo o ya estuviera muerto, cae a un kill directo del PID.
func killProcessGroup(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		return syscall.Kill(pid, syscall.SIGTERM)
	}
	return nil
}
