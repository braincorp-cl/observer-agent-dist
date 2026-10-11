package agent

import (
	"encoding/json"
	"testing"
)

// Feature 049-B2 · el polling de config como canal de respaldo del push
// `actividad_monitor`. Lo que no puede romperse: que el servidor diga "apagado"
// y el agente siga muestreando, o que un JSON de servidor viejo (sin las claves)
// encienda algo.

func agenteActividadDePrueba(t *testing.T) *Agent {
	t.Helper()
	previo := actDirOverride
	actDirOverride = t.TempDir()
	reset := func() {
		actMu.Lock()
		actActive = false
		actIntervalSeg = actDefaultIntervalSeg
		actInactivoSeg = actDefaultInactivoSeg
		actConTitulo = false
		actMu.Unlock()
	}
	reset()
	t.Cleanup(func() {
		actDirOverride = previo
		reset()
	})
	return &Agent{Logger: loggerMudo()}
}

func TestActividadDeConfigEnciendeYApaga(t *testing.T) {
	a := agenteActividadDePrueba(t)

	var conf AgentCheckInConfig
	raw := `{"actividad_active":true,"actividad_interval_seg":30,"actividad_inactivo_seg":300,"actividad_con_titulo":true}`
	if err := json.Unmarshal([]byte(raw), &conf); err != nil {
		t.Fatal(err)
	}
	a.aplicarActividadDeConfig(conf)
	activo, intervalo, inactivo, titulo := a.getActividad()
	if !activo || intervalo != 30 || inactivo != 300 || !titulo {
		t.Fatalf("encendido: got %v %d %d %v", activo, intervalo, inactivo, titulo)
	}

	conf.ActividadActive = false
	a.aplicarActividadDeConfig(conf)
	if activo, _, _, _ := a.getActividad(); activo {
		t.Fatal("el servidor dijo apagado y el agente sigue muestreando")
	}
}

func TestActividadServidorViejoNoEnciende(t *testing.T) {
	a := agenteActividadDePrueba(t)

	var conf AgentCheckInConfig
	if err := json.Unmarshal([]byte(`{"checkin_hello":30}`), &conf); err != nil {
		t.Fatal(err)
	}
	a.aplicarActividadDeConfig(conf)
	activo, intervalo, _, _ := a.getActividad()
	if activo {
		t.Fatal("un servidor sin las claves no puede encender el muestreo")
	}
	if intervalo != actDefaultIntervalSeg {
		t.Fatalf("cadencia sin clave debe caer al default, got %d", intervalo)
	}
}
