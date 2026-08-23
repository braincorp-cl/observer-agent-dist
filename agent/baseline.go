package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Feature 041 · baseline keep-awake (RN-08 / RF-12).
//
// El baseline mantiene al equipo DESPIERTO de forma persistente para que siga
// siendo medible por la geocerca: un equipo que se durmió al cerrar la tapa nunca
// reporta que salió del sitio. Es un prerrequisito de la detección, no una
// contramedida reactiva, y por eso vive SEPARADO del loop runtime de modo
// perdido/geocerca (lostmode_cascade.go):
//
//   - El runtime reactivo (no-dormir por perdido/geocerca) se aplica y revierte por
//     TRANSICIÓN y es efímero: mientras dura el caso.
//   - El baseline es PERSISTENTE por diseño (sobrevive reinicios) y su
//     reversibilidad es por CICLO DE VIDA: apagar el toggle keep_awake_baseline en
//     la consola, o desinstalar el agente (T033). No por transición.
//
// El toggle viaja por el config (keep_awake_baseline) y se reconcilia por poll, así
// que apagar el toggle restaura la flota enrolada sin reinstalar (a diferencia del
// flag -power del instalador, que sólo fija el estado inicial).
//
// El estado "aplicado" + el backup del estado de energía previo se persisten en
// disco: sin eso, un equipo que se reinicia con el baseline puesto perdería la
// referencia para revertirlo, y un revert sin backup no podría reproducir la
// config previa del equipo.

// baselineBackend es el contrato por SO. Cada plataforma lo implementa en su
// archivo (baseline_windows.go, baseline_linux.go, baseline_darwin.go) y lo
// registra en baselineBackendImpl desde su init(). En un SO sin baseline queda nil
// y la reconciliación es un no-op silencioso.
type baselineBackend interface {
	// capturar toma una foto del estado de energía actual y devuelve un blob opaco
	// para restaurarlo. Linux devuelve nil (su revert borra un drop-in, no
	// restaura valores). Un error aborta el apply: sin captura no hay revert
	// seguro, y aplicar sin poder revertir dejaría el equipo modificado sin vuelta.
	capturar() ([]byte, error)
	// aplicar impone la política keep-awake. Idempotente a nivel SO.
	aplicar() error
	// restaurar reproduce el estado previo a partir del blob de capturar().
	restaurar(backup []byte) error
}

// baselineBackendImpl lo fija el init() del archivo por-SO.
var baselineBackendImpl baselineBackend

// baselineMu serializa la reconciliación: el poll no se solapa consigo mismo ni
// con la limpieza de desinstalación (T033).
var baselineMu sync.Mutex

// baselineState es lo que se persiste. `Backup` es el blob opaco de capturar()
// (se serializa a base64 dentro del JSON por ser []byte).
type baselineState struct {
	Applied bool   `json:"applied"`
	Backup  []byte `json:"backup,omitempty"`
}

func (a *Agent) baselineStatePath() string {
	return filepath.Join(a.lostModeDataDir(), "keep_awake_baseline.json")
}

// loadBaselineState devuelve el estado y si el baseline está aplicado. Un archivo
// ausente o ilegible = "no aplicado" (y se descarta el ilegible).
func (a *Agent) loadBaselineState() (baselineState, bool) {
	var st baselineState
	data, err := os.ReadFile(a.baselineStatePath())
	if err != nil {
		return st, false
	}
	if err := json.Unmarshal(data, &st); err != nil {
		a.Logger.Debugln("baseline: estado ilegible, se descarta:", err.Error())
		_ = os.Remove(a.baselineStatePath())
		return st, false
	}
	return st, st.Applied
}

func (a *Agent) saveBaselineState(st baselineState) error {
	path := a.baselineStatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (a *Agent) clearBaselineState() {
	if err := os.Remove(a.baselineStatePath()); err != nil && !os.IsNotExist(err) {
		a.Logger.Debugln("baseline: no se pudo borrar el estado:", err.Error())
	}
}

// reconciliarBaseline concilia el baseline keep-awake contra el toggle del
// servidor, por poll:
//
//	enabled && !aplicado ⇒ capturar backup, aplicar, persistir {applied, backup}
//	!enabled && aplicado ⇒ restaurar backup, limpiar estado
//	el resto             ⇒ no-op (idempotente: no re-captura ni re-aplica)
//
// La captura del backup va SIEMPRE antes de aplicar: nunca se fotografía un estado
// que la propia política ya modificó. El estado se persiste después de un apply
// exitoso.
func (a *Agent) reconciliarBaseline(enabled bool) {
	if baselineBackendImpl == nil {
		return // SO sin baseline (o no registrado)
	}

	baselineMu.Lock()
	defer baselineMu.Unlock()

	_, aplicado := a.loadBaselineState()

	switch {
	case enabled && !aplicado:
		backup, err := baselineBackendImpl.capturar()
		if err != nil {
			a.Logger.Errorln("baseline: no se pudo capturar el estado previo; no se aplica:", err.Error())
			return
		}
		if err := baselineBackendImpl.aplicar(); err != nil {
			a.Logger.Errorln("baseline: no se pudo aplicar la política keep-awake:", err.Error())
			return
		}
		if err := a.saveBaselineState(baselineState{Applied: true, Backup: backup}); err != nil {
			// Aplicado pero sin poder persistir: se loguea. El próximo poll verá
			// "no aplicado" y re-capturará (ya modificado) — costo asumido de un
			// fallo de disco, contra el escenario normal en que persiste bien.
			a.Logger.Errorln("baseline: aplicado pero no se pudo persistir el estado:", err.Error())
			return
		}
		a.Logger.Infoln("baseline keep-awake aplicado")

	case !enabled && aplicado:
		st, _ := a.loadBaselineState()
		if err := baselineBackendImpl.restaurar(st.Backup); err != nil {
			a.Logger.Errorln("baseline: no se pudo restaurar el estado previo; se conserva el estado para reintentar:", err.Error())
			return
		}
		a.clearBaselineState()
		a.Logger.Infoln("baseline keep-awake revertido")
	}
}
