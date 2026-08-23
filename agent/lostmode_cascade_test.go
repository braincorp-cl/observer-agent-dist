package agent

import (
	"context"
	"testing"
	"time"
)

// Feature 038 · RF-06 — la cascada del modo perdido tiene que HACER SONAR la
// alarma cuando la trae habilitada.
//
// El gap que motiva estas pruebas (detectado el 2026-08-22): el flag `Alarm`
// viajaba backend→config→agente y se persistía en el estado, pero
// `aplicarContramedidasModoPerdido` sólo actuaba `AutoLock`+`NoHibernate` y
// nunca llamaba a `PlayAlarm`. Un equipo marcado perdido con la alarma
// habilitada quedaba mudo. El reproductor es de mentira (sustituirSpawner), como
// en el resto de las pruebas de alarma: no hay audio ni sesión gráfica de por
// medio, pero el bucle de la alarma corre de verdad.

func alarmaSonando() bool {
	runningAlarm.mu.Lock()
	defer runningAlarm.mu.Unlock()
	return runningAlarm.stop != nil
}

// contramedidasSincronas corre las contramedidas en línea (no en goroutine) para
// que la prueba sea determinista y no deje goroutines tocando estado global
// entre tests (el detector de carreras lo exige).
func contramedidasSincronas(t *testing.T) {
	t.Helper()
	prev := lostModeEnSegundoPlano
	lostModeEnSegundoPlano = false
	t.Cleanup(func() { lostModeEnSegundoPlano = prev })
}

func TestCascadaConAlarmaHaceSonarLaAlarma(t *testing.T) {
	contramedidasSincronas(t)
	a := testAgent()
	sustituirSpawner(t, func(*Agent, context.Context) (*alarmPlayer, error) {
		return fakeAlarmPlayer(), nil
	})
	a.StopAlarm() // parte de un estado limpio; el global es del paquete
	t.Cleanup(a.StopAlarm)

	a.setLostModeCascade(lostModeCascade{Alarm: true})
	a.aplicarContramedidasModoPerdido()

	if !esperarHasta(alarmaSonando, time.Second) {
		t.Fatal("la cascada con Alarm=true no hizo sonar la alarma (gap RF-06)")
	}

	// Recuperar el equipo = caso cerrado = silencio.
	a.revertirContramedidasModoPerdido()
	if !esperarHasta(func() bool { return !alarmaSonando() }, time.Second) {
		t.Fatal("recuperar el equipo no detuvo la alarma de la cascada")
	}
}

// TestNoHibernacionDeseadaRN03 congela la invariante multi-fuente de la feature
// 041 como tabla de verdad. La no-dormir la piden DOS fuentes independientes —la
// cascada del modo perdido y la geocerca— y sólo se apaga cuando NINGUNA la pide.
//
// La fila que importa (RN-03): recuperar el equipo de "perdido" estando AÚN fuera
// de la geocerca deja lostActivo=false pero outsideGeofence=true, y el deseado
// sigue siendo true → el equipo se mantiene despierto. Sólo con las dos fuentes en
// false se revierte.
func TestNoHibernacionDeseadaRN03(t *testing.T) {
	casos := []struct {
		nombre        string
		lostActivo    bool
		lostNoHib     bool
		fueraGeocerca bool
		quiero        bool
	}{
		{"nada activo: se revierte", false, false, false, false},
		{"sólo perdido con no-hibernar", true, true, false, true},
		{"perdido sin no-hibernar en la cascada", true, false, false, false},
		{"sólo geocerca", false, false, true, true},
		{"RN-03: recuperado pero AÚN fuera mantiene despierto", false, true, true, true},
		{"perdido no-hibernar Y geocerca", true, true, true, true},
		// Perdido sin no-hibernar pero fuera de geocerca: la geocerca lo sostiene.
		{"geocerca sostiene aunque la cascada no pida", true, false, true, true},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			got := noHibernacionDeseada(c.lostActivo, c.lostNoHib, c.fueraGeocerca)
			if got != c.quiero {
				t.Errorf("noHibernacionDeseada(%v,%v,%v) = %v, se esperaba %v",
					c.lostActivo, c.lostNoHib, c.fueraGeocerca, got, c.quiero)
			}
		})
	}
}

// TestDentroDelRadioNoActivaContramedidaReactiva es el caso negativo A002 (audit
// clarify ronda 4): dentro del radio y sin modo perdido, NADA reactivo se activa.
//
// El agente NO tiene un disparador por "tapa cerrada" ni por "cargador
// desconectado" (decisión 030: esos eventos nunca escalan). La única señal
// reactiva es outside_geofence, y con ella en false —más el modo perdido apagado—
// el estado deseado de no-dormir es false: ni no-dormir por geocerca, ni WiFi
// forzada, ni cadencia apretada. (Que el equipo pueda quedar despierto por el
// BASELINE keep-awake es otra cosa, y lo cubren T025/T026.)
func TestDentroDelRadioNoActivaContramedidaReactiva(t *testing.T) {
	a := testAgent()
	a.setOutsideGeofence(false)
	t.Cleanup(func() { a.setOutsideGeofence(false) })

	// Sin modo perdido y dentro del radio: ninguna fuente reactiva pide no-dormir.
	if noHibernacionDeseada(false, false, a.getOutsideGeofence()) {
		t.Error("dentro del radio y sin modo perdido no debe pedirse no-dormir reactivo")
	}
	// El gate de forceLocationOn (WiFi forzada) cuelga de las mismas fuentes: sin
	// force-on global ni geocerca, no se fuerza la radio.
	if a.GeoForceOn || a.getOutsideGeofence() {
		t.Error("dentro del radio no debe forzarse la radio WiFi")
	}
}

func TestCascadaSinAlarmaNoHaceSonarNada(t *testing.T) {
	contramedidasSincronas(t)
	a := testAgent()
	sustituirSpawner(t, func(*Agent, context.Context) (*alarmPlayer, error) {
		return fakeAlarmPlayer(), nil
	})
	a.StopAlarm()
	t.Cleanup(a.StopAlarm)

	// Sólo no-hibernación/bloqueo, alarma apagada: default OFF (decisión 4).
	a.setLostModeCascade(lostModeCascade{Alarm: false})
	a.aplicarContramedidasModoPerdido()

	if esperarHasta(alarmaSonando, 200*time.Millisecond) {
		t.Fatal("la cascada con Alarm=false hizo sonar la alarma igual")
	}
}
