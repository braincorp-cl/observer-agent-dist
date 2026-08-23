package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Feature 030 · el estado de modo perdido que sobrevive al reinicio.
//
// Es la mitad de la feature que no se puede ver funcionando en una demo: el
// equipo robado se reinicia y nadie está mirando. Sin estas pruebas, la única
// forma de descubrir que no persiste sería en terreno — el camino que a la 028
// le costó ocho "ok falso".

// agenteDePrueba devuelve un agente cuyo estado vive en un directorio temporal,
// y deja el estado en memoria en limpio (vive en variables de paquete, así que
// un test podría contaminar al siguiente).
func agenteDePrueba(t *testing.T) *Agent {
	t.Helper()

	dir := t.TempDir()
	previo := lostModeDirOverride
	lostModeDirOverride = dir

	lostModeMu.Lock()
	lostModeActive = false
	lostModeIntervalMin = lostModeDefaultIntervalMin
	lostModeCascadeVal = lostModeCascade{}
	lostModeMu.Unlock()

	t.Cleanup(func() {
		lostModeDirOverride = previo
		lostModeMu.Lock()
		lostModeActive = false
		lostModeIntervalMin = lostModeDefaultIntervalMin
		lostModeCascadeVal = lostModeCascade{}
		lostModeMu.Unlock()
	})

	return &Agent{Logger: loggerMudo()}
}

func TestClampLostModeInterval(t *testing.T) {
	casos := []struct {
		nombre string
		desde  int
		quiero int
	}{
		// El cero NO cae al piso sino al default: "el campo no venía" no es lo
		// mismo que "me pidieron el mínimo". Un piso de 1 min por un JSON sin la
		// clave sería una cadencia agresiva que nadie eligió.
		{"cero (clave ausente) cae al default", 0, lostModeDefaultIntervalMin},
		{"negativo cae al default", -5, lostModeDefaultIntervalMin},
		{"el piso exacto se respeta", lostModeMinIntervalMin, lostModeMinIntervalMin},
		{"por encima del techo baja al techo", 999, lostModeMaxIntervalMin},
		{"el techo exacto se respeta", lostModeMaxIntervalMin, lostModeMaxIntervalMin},
		{"un valor normal pasa tal cual", 15, 15},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := clampLostModeInterval(c.desde); got != c.quiero {
				t.Errorf("clampLostModeInterval(%d) = %d, se esperaba %d", c.desde, got, c.quiero)
			}
		})
	}
}

// TestLostModeSobreviveAlReinicio es el test central de T005.
//
// Simula el ciclo real: el servidor marca el equipo, el equipo se reinicia (se
// pierde toda la memoria del proceso) y el agente tiene que volver sabiendo que
// sigue perdido, sin haber hablado con nadie.
func TestLostModeSobreviveAlReinicio(t *testing.T) {
	a := agenteDePrueba(t)

	a.SetLostMode(true, 15)

	if activo, min := a.getLostMode(); !activo || min != 15 {
		t.Fatalf("tras marcar: activo=%v intervalo=%d; se esperaba true/15", activo, min)
	}

	// El reinicio: se borra TODO el estado en memoria.
	lostModeMu.Lock()
	lostModeActive = false
	lostModeIntervalMin = lostModeDefaultIntervalMin
	lostModeMu.Unlock()

	if activo, _ := a.getLostMode(); activo {
		t.Fatal("el reinicio simulado no limpió la memoria; el test no probaría nada")
	}

	a.ResumeLostMode()

	activo, min := a.getLostMode()
	if !activo {
		t.Error("tras el reinicio el equipo dejó de estar perdido: el estado no persistió")
	}
	if min != 15 {
		t.Errorf("tras el reinicio la cadencia es %d min, se esperaba 15", min)
	}
}

func TestRecuperarBorraElArchivo(t *testing.T) {
	a := agenteDePrueba(t)

	a.SetLostMode(true, 5)
	if _, err := os.Stat(a.lostModeStatePath()); err != nil {
		t.Fatalf("marcar como perdido no dejó el archivo de estado: %v", err)
	}

	a.SetLostMode(false, 0)

	// Se borra en vez de escribir active=false: el estado por omisión ya es "no
	// está perdido", y así no queda en el equipo un rastro de que se lo marcó.
	if _, err := os.Stat(a.lostModeStatePath()); !os.IsNotExist(err) {
		t.Errorf("recuperar no borró el archivo de estado (err=%v)", err)
	}

	// Y un arranque posterior no debe resucitarlo.
	a.ResumeLostMode()
	if activo, _ := a.getLostMode(); activo {
		t.Error("tras recuperar, el arranque volvió a marcar el equipo como perdido")
	}
}

func TestJSONIlegibleNoDejaElEquipoMarcado(t *testing.T) {
	a := agenteDePrueba(t)

	ruta := a.lostModeStatePath()
	if err := os.MkdirAll(filepath.Dir(ruta), 0755); err != nil {
		t.Fatal(err)
	}
	// Lo que dejaría un corte de luz a mitad de escritura, si no fuera atómica.
	if err := os.WriteFile(ruta, []byte(`{"active": tr`), 0600); err != nil {
		t.Fatal(err)
	}

	a.ResumeLostMode()

	if activo, _ := a.getLostMode(); activo {
		t.Error("un JSON truncado dejó el equipo marcado como perdido")
	}
	if _, err := os.Stat(ruta); !os.IsNotExist(err) {
		t.Error("el archivo ilegible no se limpió; volvería a fallar en cada arranque")
	}
}

// TestEscrituraAtomica comprueba que no queda un temporal tirado tras una
// escritura normal. Un `.tmp` huérfano en el directorio del agente es basura que
// el desinstalador sí borra, pero que delata el mecanismo a quien mire el disco.
func TestEscrituraAtomica(t *testing.T) {
	a := agenteDePrueba(t)
	a.SetLostMode(true, 5)

	entradas, err := os.ReadDir(a.lostModeDataDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entradas {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("quedó un temporal huérfano: %s", e.Name())
		}
	}
}

// TestElIntervaloDelServidorTambienSeAcota: el clamp del agente no es
// redundante con el del servidor. El mismo valor llega por DOS canales (el push
// por NATS y el polling de config) y el piso no puede depender de que quien lo
// mandó lo haya acotado.
func TestElIntervaloDelServidorTambienSeAcota(t *testing.T) {
	a := agenteDePrueba(t)

	a.SetLostMode(true, 9999)
	if _, min := a.getLostMode(); min != lostModeMaxIntervalMin {
		t.Errorf("intervalo %d; el agente debía acotarlo a %d", min, lostModeMaxIntervalMin)
	}

	// Y el valor acotado es el que queda en disco, no el original.
	st, ok := a.loadLostModeState()
	if !ok {
		t.Fatal("no se pudo releer el estado")
	}
	if st.IntervalMin != lostModeMaxIntervalMin {
		t.Errorf("en disco quedó %d, se esperaba %d", st.IntervalMin, lostModeMaxIntervalMin)
	}
}

// TestRepliegueNoApagaElModoPerdido congela la decisión que evita el peor
// desenlace: que el equipo pierda la red y ESO apague la captura.
func TestRepliegueNoApagaElModoPerdido(t *testing.T) {
	fb := checkInConfFallback()

	if fb.fromServer {
		t.Error("el repliegue se está marcando como config del servidor; su lost_mode=false se aplicaría")
	}
	if fb.LostMode {
		t.Error("el repliegue no debe afirmar que el equipo está perdido; sólo no debe apagarlo")
	}
}

// TestGeoTickIntervalEnModoPerdido congela las dos mitades de la decisión: el
// piso de privacidad protege la operación normal y NO se aplica al modo
// perdido, donde el límite lo pone el clamp de 1-60 min.
func TestGeoTickIntervalEnModoPerdido(t *testing.T) {
	casos := []struct {
		nombre       string
		perdido      bool
		intervaloMin int
		desdeConf    int
		quiero       int
	}{
		{"normal: el piso protege", false, 0, 60, geoMinInterval},
		{"normal: por encima del piso pasa tal cual", false, 0, 1800, 1800},
		// 5 min = 300 s coincide con el piso por casualidad; se usa 1 min para
		// que el test falle si alguien vuelve a aplicar el piso.
		{"perdido: 1 min SALTEA el piso de 300 s", true, 1, 1800, 60},
		{"perdido: 15 min", true, 15, 1800, 900},
		{"perdido: el clamp sigue aplicando por arriba", true, 9999, 1800, lostModeMaxIntervalMin * 60},
		{"perdido: intervalo ausente cae al default", true, 0, 1800, lostModeDefaultIntervalMin * 60},
		{"perdido: la config de geo del servidor se ignora", true, 2, 60, 120},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			got := geoTickIntervalFor(c.perdido, c.intervaloMin, false, 0, c.desdeConf)
			if got != c.quiero {
				t.Errorf("geoTickIntervalFor(%v, %d, %d) = %d, se esperaba %d",
					c.perdido, c.intervaloMin, c.desdeConf, got, c.quiero)
			}
		})
	}
}

// TestModoPerdidoNoBajaDelMinutoEnElTicker es la salvaguarda que queda cuando se
// saltea el piso: sin ella, un intervalo de 0 —o negativo— dejaría un ticker de
// alta frecuencia que mataría la batería y delataría al agente.
func TestModoPerdidoNoBajaDelMinutoEnElTicker(t *testing.T) {
	for _, min := range []int{-100, -1, 0, 1} {
		if got := geoTickIntervalFor(true, min, false, 0, 1800); got < lostModeMinIntervalMin*60 {
			t.Errorf("con intervalo %d el ticker quedó en %ds, por debajo del mínimo de %ds",
				min, got, lostModeMinIntervalMin*60)
		}
	}
}

// Feature 030 · el aviso que despierta al bucle del servicio.
//
// Es la prueba del hallazgo de terreno del 2026-08-11: el push fijaba el estado
// al instante y la cadencia del caso recién empezaba en el tick normal siguiente
// —25 a 35 min con el `checkin_geo` por omisión, con el equipo encendido—. Lo que
// se puede probar acá es el aviso; que el bucle lo consuma vive en `AgentSvc()` y
// sólo corre con un servicio real contra NATS (W-030-20).
func TestSetLostModeAvisaAlBucle(t *testing.T) {
	a := agenteDePrueba(t)
	drenar := func() {
		for {
			select {
			case <-lostModeChanged:
			default:
				return
			}
		}
	}
	hayAviso := func() bool {
		select {
		case <-lostModeChanged:
			return true
		default:
			return false
		}
	}

	drenar()

	a.SetLostMode(true, 1)
	if !hayAviso() {
		t.Error("marcar como perdido no avisó al bucle: la cadencia del caso esperaría al próximo tick")
	}

	// Sin cambio real no hay aviso: un re-marcaje idéntico llega por los dos
	// canales (push y polling) y no tiene por qué re-armar el ticker.
	a.SetLostMode(true, 1)
	if hayAviso() {
		t.Error("un SetLostMode sin cambio avisó igual")
	}

	a.SetLostMode(false, 0)
	if !hayAviso() {
		t.Error("recuperar no avisó al bucle: la cadencia intensiva seguiría hasta el próximo tick")
	}
}

// El aviso NO puede bloquear a quien lo emite: SetLostMode corre en la goroutine
// del push por NATS, y si el bucle está ocupado —una captura de geo en macOS
// puede costar 40 s— un canal sin buffer dejaría esa goroutine colgada y el push
// sin responder. Dos cambios seguidos sin consumidor tienen que pasar de largo.
func TestElAvisoNoBloqueaAunqueNadieEscuche(t *testing.T) {
	a := agenteDePrueba(t)

	hecho := make(chan struct{})
	go func() {
		a.SetLostMode(true, 1)
		a.SetLostMode(false, 0)
		a.SetLostMode(true, 5)
		close(hecho)
	}()

	select {
	case <-hecho:
	case <-time.After(3 * time.Second):
		t.Fatal("SetLostMode quedó bloqueado esperando que alguien leyera el aviso")
	}
}

// TestCascadaSobreviveAlReinicio (feature 038): la cascada persiste junto al
// estado, para poder REVERTIR las contramedidas —p. ej. la no-hibernación— al
// recuperar el equipo aunque de por medio haya habido un reinicio.
func TestCascadaSobreviveAlReinicio(t *testing.T) {
	a := agenteDePrueba(t)

	a.setLostModeCascade(lostModeCascade{
		AutoLock:       true,
		LockDelayMin:   0,
		NoHibernate:    true,
		WebcamOverride: true,
		Alarm:          false,
	})
	a.SetLostMode(true, 15)

	// Reinicio: se borra TODO el estado en memoria, incluida la cascada.
	lostModeMu.Lock()
	lostModeActive = false
	lostModeIntervalMin = lostModeDefaultIntervalMin
	lostModeCascadeVal = lostModeCascade{}
	lostModeMu.Unlock()

	a.ResumeLostMode()

	c := a.getLostModeCascade()
	if !c.AutoLock || !c.NoHibernate || !c.WebcamOverride || c.Alarm {
		t.Errorf("la cascada no se restauró tras el reinicio: %+v", c)
	}
	if c.LockDelayMin != 0 {
		t.Errorf("LockDelayMin tras el reinicio = %d, se esperaba 0", c.LockDelayMin)
	}
}

// TestRecuperarLimpiaLaCascada: al recuperar, el archivo de estado se borra, así
// que un ResumeLostMode posterior no rearma ninguna cascada (no queda rastro).
func TestRecuperarLimpiaLaCascada(t *testing.T) {
	a := agenteDePrueba(t)

	a.setLostModeCascade(lostModeCascade{NoHibernate: true})
	a.SetLostMode(true, 5)
	a.SetLostMode(false, 0)

	// Reinicio y resume: no debe haber estado que restaurar.
	lostModeMu.Lock()
	lostModeCascadeVal = lostModeCascade{}
	lostModeMu.Unlock()

	a.ResumeLostMode()

	if activo, _ := a.getLostMode(); activo {
		t.Fatal("tras recuperar y reiniciar el equipo volvió a estar perdido")
	}
	if c := a.getLostModeCascade(); c.NoHibernate {
		t.Errorf("la cascada sobrevivió a la recuperación: %+v", c)
	}
}
