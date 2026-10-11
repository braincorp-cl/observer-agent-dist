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
	// Las capturas tienen piso propio: una foto de la pantalla de una persona
	// cada pocos segundos sería vigilancia en video, no un muestreo.
	actMinCapturasMin = 1
	actMaxCapturasMin = 240
)

type persistedActividad struct {
	Active      bool `json:"active"`
	IntervalSeg int  `json:"interval_seg"`
	InactivoSeg int  `json:"inactivo_seg"`
	ConTitulo   bool `json:"con_titulo"`
	// 049-C: cada cuántos minutos se sube una captura de pantalla (0 = nunca) y
	// si se mide el uso de red por aplicación. Apagados por omisión: un servidor
	// viejo no los manda y el agente no captura ni mide red.
	CapturasMin int  `json:"capturas_min,omitempty"`
	Red         bool `json:"red,omitempty"`
}

var (
	actMu          sync.RWMutex
	actActive      bool
	actIntervalSeg = actDefaultIntervalSeg
	actInactivoSeg = actDefaultInactivoSeg
	actConTitulo   bool
	actCapturasMin int
	actRed         bool

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
	a.saveActividadCfg(a.estadoActividadPersistible())
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
	actCapturasMin = clampCapturasMin(st.CapturasMin)
	actRed = st.Red
	actMu.Unlock()
	a.Logger.Infoln("ResumeActividad: el monitoreo seguía ENCENDIDO, cadencia", actIntervalSeg, "s")
}

// aplicarActividadDeConfig aplica el estado de monitoreo que el servidor resolvió
// (feature 049-B2) y entrega por el polling de config. El llamador garantiza que
// la config vino de verdad del servidor (fromServer): con el repliegue todos los
// campos son ceros y apagarían el muestreo por una caída de red.
func (a *Agent) aplicarActividadDeConfig(conf AgentCheckInConfig) {
	a.SetActividadExtras(conf.ActividadCapturasMin, conf.ActividadRed)
	a.SetActividad(conf.ActividadActive, conf.ActividadIntervalSeg, conf.ActividadInactivoSeg, conf.ActividadConTitulo)
}

func clampCapturasMin(m int) int {
	if m <= 0 {
		return 0
	}
	if m < actMinCapturasMin {
		return actMinCapturasMin
	}
	if m > actMaxCapturasMin {
		return actMaxCapturasMin
	}
	return m
}

func (a *Agent) estadoActividadPersistible() persistedActividad {
	actMu.RLock()
	defer actMu.RUnlock()
	return persistedActividad{
		Active:      actActive,
		IntervalSeg: actIntervalSeg,
		InactivoSeg: actInactivoSeg,
		ConTitulo:   actConTitulo,
		CapturasMin: actCapturasMin,
		Red:         actRed,
	}
}

// getActividadExtras devuelve la cadencia de capturas (min, 0 = apagadas) y si
// se mide la red por aplicación (049-C).
func (a *Agent) getActividadExtras() (capturasMin int, red bool) {
	actMu.RLock()
	defer actMu.RUnlock()
	return actCapturasMin, actRed
}

// SetActividadExtras fija capturas y red (049-C). Se llama ANTES de SetActividad
// para que la persistencia que hace aquel ya incluya estos valores; si sólo
// cambian estos, persiste por su cuenta.
func (a *Agent) SetActividadExtras(capturasMin int, red bool) {
	c := clampCapturasMin(capturasMin)
	actMu.Lock()
	cambio := actCapturasMin != c || actRed != red
	actCapturasMin = c
	actRed = red
	actMu.Unlock()
	if !cambio {
		return
	}
	a.saveActividadCfg(a.estadoActividadPersistible())
	a.Logger.Infoln("SetActividadExtras: capturas cada", c, "min (0 = apagadas), red por app:", red)
}
