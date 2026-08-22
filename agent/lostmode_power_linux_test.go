//go:build linux

package agent

import (
	"os"
	"testing"
)

// Feature 038 · T011 — no-suspensión reversible en Linux.
//
// Se prueba la POLÍTICA (idempotencia, revert exacto, limpieza de huérfano) con
// dobles en las tres costuras: no hay systemd ni procesos reales de por medio,
// que es lo que hace la prueba determinista en CI. El lanzamiento real de
// systemd-inhibit se ejercita en terreno (E2E), no acá.

func conInhibidorFalso(t *testing.T) (arranques *int, vivo map[int]bool, matados *[]int) {
	t.Helper()

	prevDir := lostModeDirOverride
	lostModeDirOverride = t.TempDir()

	prevStart, prevAlive, prevKill := startLostInhibitor, lostInhibitorAlive, killLostInhibitor
	t.Cleanup(func() {
		lostModeDirOverride = prevDir
		startLostInhibitor, lostInhibitorAlive, killLostInhibitor = prevStart, prevAlive, prevKill
	})

	n := 0
	alive := map[int]bool{}
	killed := []int{}

	startLostInhibitor = func(a *Agent) (int, error) {
		n++
		pid := 4200 + n
		alive[pid] = true
		return pid, nil
	}
	lostInhibitorAlive = func(pid int) bool { return alive[pid] }
	killLostInhibitor = func(pid int) error {
		alive[pid] = false
		killed = append(killed, pid)
		return nil
	}

	return &n, alive, &killed
}

func TestNoSuspensionLinuxAplicaUnaVezYRevierte(t *testing.T) {
	arranques, _, matados := conInhibidorFalso(t)
	a := testAgent()

	a.aplicarNoHibernacion()
	if *arranques != 1 {
		t.Fatalf("esperaba 1 inhibidor lanzado, hubo %d", *arranques)
	}
	if _, ok := a.readLostPowerState(); !ok {
		t.Fatal("no se persistió el estado del inhibidor")
	}

	// Re-aplicar con el inhibidor vivo NO debe lanzar otro (idempotencia).
	a.aplicarNoHibernacion()
	if *arranques != 1 {
		t.Fatalf("re-aplicar no debe relanzar; hubo %d arranques", *arranques)
	}

	a.revertirNoHibernacion()
	if len(*matados) != 1 || (*matados)[0] != 4201 {
		t.Fatalf("revert no mató el inhibidor correcto: %v", *matados)
	}
	if _, ok := a.readLostPowerState(); ok {
		t.Fatal("revert no borró el estado")
	}

	// Revert sin estado es no-op silencioso.
	a.revertirNoHibernacion()
	if len(*matados) != 1 {
		t.Fatalf("revert sin estado no debe matar de nuevo: %v", *matados)
	}
}

func TestNoSuspensionLinuxRelanzaSiElPidGuardadoMurio(t *testing.T) {
	arranques, vivo, _ := conInhibidorFalso(t)
	a := testAgent()

	a.aplicarNoHibernacion() // pid 4201, vivo
	// Simula un agente reiniciado sucio: el inhibidor previo ya no vive, pero el
	// archivo de estado sobrevivió apuntando a él.
	vivo[4201] = false

	a.aplicarNoHibernacion()
	if *arranques != 2 {
		t.Fatalf("con el inhibidor previo muerto debía relanzarse; arranques=%d", *arranques)
	}
	st, ok := a.readLostPowerState()
	if !ok || st.InhibitPID != 4202 {
		t.Fatalf("el estado debía apuntar al inhibidor nuevo (4202), quedó %+v (ok=%v)", st, ok)
	}
}

func TestNoSuspensionLinuxEstadoIlegibleSeDescarta(t *testing.T) {
	conInhibidorFalso(t)
	a := testAgent()

	if err := os.WriteFile(a.lostPowerStatePath(), []byte("{no es json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.readLostPowerState(); ok {
		t.Fatal("un estado ilegible no debe leerse como válido")
	}
	if _, err := os.Stat(a.lostPowerStatePath()); !os.IsNotExist(err) {
		t.Fatal("un estado ilegible debe borrarse")
	}
}
