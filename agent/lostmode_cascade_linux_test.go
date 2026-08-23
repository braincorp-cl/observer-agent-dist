//go:build linux

package agent

import "testing"

// Feature 041 · T008 (efecto real en Linux) — la invariante RN-03 de la
// reconciliación de no-dormir, ejercitada de punta a punta contra el mecanismo
// real (inhibidor de systemd, con las mismas costuras que las pruebas de la 038:
// sin systemd ni procesos reales). El predicado puro lo cubre
// TestNoHibernacionDeseadaRN03; acá se verifica que aplicar/revertir se llaman
// donde deben y NO donde no deben al cruzar las transiciones.
func TestReconcileRN03MantieneDespiertoAlRecuperarEstandoFuera(t *testing.T) {
	_, _, matados := conInhibidorFalso(t)
	a := testAgent()
	t.Cleanup(func() {
		a.setOutsideGeofence(false)
		a.SetLostMode(false, 0)
		a.setLostModeCascade(lostModeCascade{})
	})

	// 1. Fuera de geocerca (sin modo perdido) → la reconciliación aplica no-dormir.
	a.setOutsideGeofence(true)
	a.reconciliarNoHibernacion()
	if _, ok := a.readLostPowerState(); !ok {
		t.Fatal("estar fuera de geocerca debía aplicar la no-dormir")
	}

	// 2. Además entra en modo perdido pidiendo no-hibernar: sigue aplicado, sin
	//    relanzar el inhibidor (idempotencia con dos fuentes activas).
	a.setLostModeCascade(lostModeCascade{NoHibernate: true})
	a.SetLostMode(true, 5)
	a.reconciliarNoHibernacion()
	if _, ok := a.readLostPowerState(); !ok {
		t.Fatal("con modo perdido + geocerca debía seguir aplicada")
	}

	// 3. RN-03: se RECUPERA de perdido pero el equipo SIGUE fuera de la geocerca.
	//    La no-dormir NO se revierte —lo mantiene la fuente geocerca—.
	a.SetLostMode(false, 0)
	a.reconciliarNoHibernacion()
	if _, ok := a.readLostPowerState(); !ok {
		t.Fatal("RN-03 roto: recuperar estando aún fuera revirtió la no-dormir")
	}
	if len(*matados) != 0 {
		t.Fatalf("no debía liberar el inhibidor con la geocerca aún activa: %v", *matados)
	}

	// 4. Recién cuando también vuelve dentro del radio (ambas fuentes false) se
	//    revierte y se libera el inhibidor.
	a.setOutsideGeofence(false)
	a.reconciliarNoHibernacion()
	if _, ok := a.readLostPowerState(); ok {
		t.Fatal("sin ninguna fuente activa debía revertir la no-dormir")
	}
	if len(*matados) != 1 {
		t.Fatalf("debía liberar el inhibidor exactamente una vez: %v", *matados)
	}
}
