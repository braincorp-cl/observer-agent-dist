package agent

// Feature 049 · estado del módulo de actividad (encendido, cadencia, umbral de
// inactividad, si captura títulos). Espejo simplificado de lostmodestate.go:
// sobrevive al proceso y al reinicio, y llega por dos canales (push NATS +
// polling de config) — por eso el clamp duplica a propósito el del servidor.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

const (
	actMinIntervalSeg     = 15
	actMaxIntervalSeg     = 600
	actDefaultIntervalSeg = 60
	actDefaultInactivoSeg = 120
)

type persistedActividad struct {
	Active      bool `json:"active"`
	IntervalSeg int  `json:"interval_seg"`
	InactivoSeg int  `json:"inactivo_seg"`
	ConTitulo   bool `json:"con_titulo"`
}

var (
	actMu          sync.RWMutex
	actActive      bool
	actIntervalSeg = actDefaultIntervalSeg
	actInactivoSeg = actDefaultInactivoSeg
	actConTitulo   bool

	actChanged     = make(chan struct{}, 1)
	actDirOverride string
)

func signalActividadChanged() {
	select {
	case actChanged <- struct{}{}:
	default:
	}
}

func clampActInterval(seg int) int {
	if seg <= 0 {
		return actDefaultIntervalSeg
	}
	if seg < actMinIntervalSeg {
		return actMinIntervalSeg
	}
	if seg > actMaxIntervalSeg {
		return actMaxIntervalSeg
	}
	return seg
}

func (a *Agent) actividadDataDir() string {
	if actDirOverride != "" {
		return actDirOverride
	}
	if runtime.GOOS == "windows" {
		return a.WinTmpDir
	}
	return nixAgentDir
}

func (a *Agent) actividadCfgPath() string {
	return filepath.Join(a.actividadDataDir(), "actividad_cfg.json")
}

func (a *Agent) actividadDataPath() string {
	return filepath.Join(a.actividadDataDir(), "actividad_data.json")
}

func (a *Agent) saveActividadCfg(st persistedActividad) {
	path := a.actividadCfgPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		a.Logger.Debugln("saveActividadCfg: mkdir:", err)
		return
	}
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		a.Logger.Debugln("saveActividadCfg: write:", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
}

func (a *Agent) loadActividadCfg() (persistedActividad, bool) {
	var st persistedActividad
	data, err := os.ReadFile(a.actividadCfgPath())
	if err != nil {
		return st, false
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, false
	}
	return st, true
}

func (a *Agent) getActividad() (active bool, intervalSeg, inactivoSeg int, conTitulo bool) {
	actMu.RLock()
	defer actMu.RUnlock()
	return actActive, actIntervalSeg, actInactivoSeg, actConTitulo
}

// SetActividad fija el estado y lo persiste. Idempotente; la llaman el push por
// NATS y el polling de config.
func (a *Agent) SetActividad(active bool, intervalSeg, inactivoSeg int, conTitulo bool) {
	interval := clampActInterval(intervalSeg)
	inact := inactivoSeg
	if inact <= 0 {
		inact = actDefaultInactivoSeg
	}

	actMu.Lock()
	cambio := actActive != active || actIntervalSeg != interval || actInactivoSeg != inact || actConTitulo != conTitulo
	actActive = active
	actIntervalSeg = interval
	actInactivoSeg = inact
	actConTitulo = conTitulo
	actMu.Unlock()

	if !cambio {
		return
	}
	signalActividadChanged()
	a.saveActividadCfg(persistedActividad{Active: active, IntervalSeg: interval, InactivoSeg: inact, ConTitulo: conTitulo})
	if active {
		a.Logger.Infoln("SetActividad: monitoreo ENCENDIDO, cadencia", interval, "s, umbral inactividad", inact, "s")
	} else {
		a.Logger.Infoln("SetActividad: monitoreo APAGADO")
	}
}

// ResumeActividad restaura el estado de una ejecución anterior. Se llama al
// arrancar el servicio, junto a ResumeLostMode.
func (a *Agent) ResumeActividad() {
	st, ok := a.loadActividadCfg()
	if !ok || !st.Active {
		return
	}
	actMu.Lock()
	actActive = true
	actIntervalSeg = clampActInterval(st.IntervalSeg)
	if st.InactivoSeg > 0 {
		actInactivoSeg = st.InactivoSeg
	}
	actConTitulo = st.ConTitulo
	actMu.Unlock()
	a.Logger.Infoln("ResumeActividad: el monitoreo seguía ENCENDIDO, cadencia", actIntervalSeg, "s")
}
