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

func TestCascadaConAlarmaHaceSonarLaAlarma(t *testing.T) {
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

func TestCascadaSinAlarmaNoHaceSonarNada(t *testing.T) {
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
