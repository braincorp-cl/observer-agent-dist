package agent

import (
	"errors"
	"testing"
)

// Feature 041 · baseline keep-awake (T025/T026).
//
// Se prueba el DISPATCH y la POLÍTICA de reconciliación (baseline.go) con un
// backend de mentira, sin tocar el SO: eso es justo lo que hace las pruebas
// deterministas y seguras en cualquier equipo (el backend real toca powercfg /
// logind / pmset). El mecanismo por-SO se confirma en terreno (E2E).

type fakeBaselineBackend struct {
	backup     []byte
	capErr     error
	aplicarErr error

	orden          []string
	restauradoCon  [][]byte
	capturas       int
	aplicaciones   int
	restauraciones int
}

func (f *fakeBaselineBackend) capturar() ([]byte, error) {
	f.orden = append(f.orden, "capturar")
	f.capturas++
	return f.backup, f.capErr
}

func (f *fakeBaselineBackend) aplicar() error {
	f.orden = append(f.orden, "aplicar")
	f.aplicaciones++
	return f.aplicarErr
}

func (f *fakeBaselineBackend) restaurar(b []byte) error {
	f.orden = append(f.orden, "restaurar")
	f.restauraciones++
	f.restauradoCon = append(f.restauradoCon, b)
	return nil
}

func conBackendBaselineFalso(t *testing.T, f baselineBackend) *Agent {
	t.Helper()
	prevImpl := baselineBackendImpl
	prevDir := lostModeDirOverride
	baselineBackendImpl = f
	lostModeDirOverride = t.TempDir()
	t.Cleanup(func() {
		baselineBackendImpl = prevImpl
		lostModeDirOverride = prevDir
	})
	return testAgent()
}

// TestBaselineCapturaBackupAntesDeAplicar es T025: la captura del estado previo va
// SIEMPRE antes de aplicar la política (para no fotografiar un estado ya
// modificado), y el revert reproduce EXACTAMENTE ese backup. Se cubre un caso por
// "SO": Linux no captura backup (nil), Windows/macOS sí.
func TestBaselineCapturaBackupAntesDeAplicar(t *testing.T) {
	casos := []struct {
		nombre string
		backup []byte
	}{
		{"linux: sin backup", nil},
		{"windows/macos: con backup", []byte(`{"estado":"previo"}`)},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			f := &fakeBaselineBackend{backup: c.backup}
			a := conBackendBaselineFalso(t, f)

			a.reconciliarBaseline(true)

			// El orden es lo que garantiza que nunca se capture un estado ya
			// modificado por la propia política.
			if len(f.orden) < 2 || f.orden[0] != "capturar" || f.orden[1] != "aplicar" {
				t.Fatalf("se esperaba capturar→aplicar, hubo %v", f.orden)
			}

			st, aplicado := a.loadBaselineState()
			if !aplicado {
				t.Fatal("tras aplicar debía quedar el estado 'aplicado' en disco")
			}
			if string(st.Backup) != string(c.backup) {
				t.Fatalf("el backup persistido no es el capturado: %q vs %q", st.Backup, c.backup)
			}

			// Apagar el toggle restaura con EL MISMO backup capturado.
			a.reconciliarBaseline(false)
			if f.restauraciones != 1 {
				t.Fatalf("apagar el toggle debía restaurar una vez, hubo %d", f.restauraciones)
			}
			if string(f.restauradoCon[0]) != string(c.backup) {
				t.Fatalf("el revert no reprodujo el estado previo: %q vs %q",
					f.restauradoCon[0], c.backup)
			}
			if _, aplicado := a.loadBaselineState(); aplicado {
				t.Fatal("tras revertir no debía quedar 'aplicado'")
			}
		})
	}
}

// TestBaselineReconciliacionIdempotente es T026: por poll, on ⇒ captura+aplica UNA
// sola vez; off ⇒ restaura; re-poll sin efecto; y re-encender tras revertir vuelve
// a aplicar (ciclo completo).
func TestBaselineReconciliacionIdempotente(t *testing.T) {
	f := &fakeBaselineBackend{backup: []byte("bk")}
	a := conBackendBaselineFalso(t, f)

	a.reconciliarBaseline(true)
	a.reconciliarBaseline(true)
	a.reconciliarBaseline(true)
	if f.capturas != 1 || f.aplicaciones != 1 {
		t.Fatalf("on repetido debía capturar/aplicar una sola vez: cap=%d apl=%d",
			f.capturas, f.aplicaciones)
	}

	a.reconciliarBaseline(false)
	a.reconciliarBaseline(false)
	if f.restauraciones != 1 {
		t.Fatalf("off repetido debía restaurar una sola vez: %d", f.restauraciones)
	}

	// Re-encender tras revertir vuelve a capturar/aplicar: es un ciclo nuevo.
	a.reconciliarBaseline(true)
	if f.capturas != 2 || f.aplicaciones != 2 {
		t.Fatalf("re-encender tras revertir debía re-aplicar: cap=%d apl=%d",
			f.capturas, f.aplicaciones)
	}
}

// TestBaselineOffSinAplicarEsNoOp: apagar el toggle cuando nunca se aplicó no
// restaura nada (no hay estado previo que reproducir).
func TestBaselineOffSinAplicarEsNoOp(t *testing.T) {
	f := &fakeBaselineBackend{}
	a := conBackendBaselineFalso(t, f)

	a.reconciliarBaseline(false)
	if f.restauraciones != 0 || f.capturas != 0 || f.aplicaciones != 0 {
		t.Fatalf("off sin haber aplicado debía ser no-op: %v", f.orden)
	}
}

// TestBaselineCapturaFallidaNoAplica: si la captura del estado previo falla, NO se
// aplica la política —aplicar sin poder revertir dejaría el equipo modificado sin
// vuelta—.
func TestBaselineCapturaFallidaNoAplica(t *testing.T) {
	f := &fakeBaselineBackend{capErr: errors.New("no se pudo leer el estado")}
	a := conBackendBaselineFalso(t, f)

	a.reconciliarBaseline(true)
	if f.aplicaciones != 0 {
		t.Fatal("no debía aplicar la política si la captura del backup falló")
	}
	if _, aplicado := a.loadBaselineState(); aplicado {
		t.Fatal("no debía quedar 'aplicado' si nunca se aplicó")
	}
}
