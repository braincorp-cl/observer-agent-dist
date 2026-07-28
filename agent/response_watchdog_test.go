package agent

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// Feature 028 · Fase 2 — pruebas del vigilante del reproductor de la alarma.
//
// Lo que se prueba acá es la política de supervisión, que es lo que evita el
// silencio inadvertido: si el proceso que suena muere y nadie lo relanza, el equipo
// queda mudo con el volumen clavado al máximo y la consola creyendo que la alarma
// sigue corriendo.
//
// Los reproductores son de mentira y no hay audio ni sesión gráfica en el medio.
// Eso es a propósito, no una concesión: el arranque real (`startAlarmPlayer`)
// necesita una sesión de usuario y un dispositivo de sonido, así que en una máquina
// de pruebas sólo se podría *saltear*. Sustituyéndolo se ejercita de verdad el
// bucle, que es la parte que cambió.
//
// Un `alarmPlayer` sin proceso detrás es seguro: `stop()` llama a
// `killUserSessionCmd`, que ignora un `cmd` nulo.

func testAgent() *Agent {
	log := logrus.New()
	log.SetOutput(io.Discard)
	return &Agent{Logger: log}
}

func fakeAlarmPlayer() *alarmPlayer {
	return &alarmPlayer{exited: make(chan error, 1), startedAt: time.Now()}
}

// comprimirTiemposDeRespawn baja las esperas a escala de milisegundos y las
// restaura al terminar la prueba.
func comprimirTiemposDeRespawn(t *testing.T) {
	t.Helper()

	minUptime, base, max := alarmRespawnMinUptime, alarmRespawnBaseBackoff, alarmRespawnMaxBackoff
	t.Cleanup(func() {
		alarmRespawnMinUptime, alarmRespawnBaseBackoff, alarmRespawnMaxBackoff = minUptime, base, max
	})

	alarmRespawnMinUptime = 5 * time.Second // los fakes mueren al instante: siempre "fallo rápido"
	alarmRespawnBaseBackoff = 5 * time.Millisecond
	alarmRespawnMaxBackoff = 20 * time.Millisecond
}

// sustituirSpawner cambia el arranque del reproductor y lo restaura al terminar.
func sustituirSpawner(t *testing.T, fn func(*Agent, context.Context) (*alarmPlayer, error)) {
	t.Helper()

	original := alarmSpawner
	t.Cleanup(func() { alarmSpawner = original })
	alarmSpawner = fn
}

// alarmaTerminada informa si la alarma ya se dio por terminada sola. finishAlarm
// deja `runningAlarm.stop` en nil cuando la alarma vigente termina, así que es la
// señal observable de que el bucle salió sin que nadie lo detuviera.
func alarmaTerminada() bool {
	runningAlarm.mu.Lock()
	defer runningAlarm.mu.Unlock()
	return runningAlarm.stop == nil
}

func esperarHasta(cond func() bool, plazo time.Duration) bool {
	limite := time.Now().Add(plazo)
	for time.Now().Before(limite) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

// El caso que motiva todo el vigilante: alguien mata el proceso que suena —en
// Windows es UN PowerShell, un clic en el administrador de tareas— y la alarma
// tiene que volver a sonar en vez de quedarse callada.
func TestVigilanteRelanzaElReproductorQueMuereSolo(t *testing.T) {
	comprimirTiemposDeRespawn(t)

	var mu sync.Mutex
	lanzados := []*alarmPlayer{}
	aparecio := make(chan *alarmPlayer, 8)

	sustituirSpawner(t, func(a *Agent, ctx context.Context) (*alarmPlayer, error) {
		p := fakeAlarmPlayer()
		mu.Lock()
		lanzados = append(lanzados, p)
		mu.Unlock()
		aparecio <- p
		return p, nil
	})

	a := testAgent()
	if err := a.PlayAlarm(AlarmOptions{Seconds: 60}); err != nil {
		t.Fatalf("PlayAlarm falló: %v", err)
	}
	defer a.StopAlarm()

	primero := <-aparecio

	// El proceso muere sin que nadie lo haya pedido.
	primero.exited <- nil

	select {
	case segundo := <-aparecio:
		if segundo == primero {
			t.Fatal("el vigilante devolvió el mismo reproductor en vez de lanzar uno nuevo")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("el reproductor murió y NADIE lo relanzó: la alarma quedó en silencio")
	}

	// Y sigue vigilando al nuevo, no sólo al primero.
	segundo := lanzados[1]
	segundo.exited <- nil

	select {
	case <-aparecio:
	case <-time.After(2 * time.Second):
		t.Fatal("el vigilante dejó de vigilar después del primer relanzamiento")
	}
}

// spawnerDeUnSoloArranque monta un spawner cuyo PRIMER arranque funciona y cuyos
// relanzamientos fallan siempre. El primer arranque tiene que funcionar: si fallara,
// PlayAlarm devolvería error de entrada y no habría bucle de supervisión que probar.
//
// Devuelve el canal por donde llega ese reproductor bueno y una forma de leer cuántos
// intentos hubo en total.
func spawnerDeUnSoloArranque(t *testing.T) (<-chan *alarmPlayer, func() int) {
	t.Helper()

	var mu sync.Mutex
	n := 0
	ch := make(chan *alarmPlayer, 1)

	sustituirSpawner(t, func(a *Agent, ctx context.Context) (*alarmPlayer, error) {
		mu.Lock()
		n++
		esPrimero := n == 1
		mu.Unlock()

		if esPrimero {
			p := fakeAlarmPlayer()
			ch <- p
			return p, nil
		}
		return nil, errors.New("no hay con qué reproducir")
	})

	return ch, func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

// La alarma CON DURACIÓN se rinde: si el reproductor no arranca, su plazo la iba a
// terminar igual y reintentar sin tope sólo esconde un equipo sin audio.
func TestAlarmaConDuracionSeRindeYDevuelveElControl(t *testing.T) {
	comprimirTiemposDeRespawn(t)

	llegoElPrimero, intentos := spawnerDeUnSoloArranque(t)

	a := testAgent()
	// Duración larga a propósito: si el plazo la terminara, la prueba no distinguiría
	// "se rindió" de "se le cumplió el tiempo".
	if err := a.PlayAlarm(AlarmOptions{Seconds: 300}); err != nil {
		t.Fatalf("PlayAlarm falló: %v", err)
	}
	defer a.StopAlarm()

	runningAlarm.mu.Lock()
	stop := runningAlarm.stop
	runningAlarm.mu.Unlock()
	if stop == nil {
		t.Fatal("la alarma no quedó registrada como vigente")
	}

	// Se mata el único reproductor que arrancó bien.
	(<-llegoElPrimero).exited <- nil

	if !esperarHasta(alarmaTerminada, 3*time.Second) {
		t.Fatal("la alarma nunca se rindió: quedó reintentando sin tope con una duración fija")
	}

	total := intentos()

	// 1 arranque bueno + los reintentos hasta pasarse del tope.
	if maxEsperado := 1 + alarmRespawnMaxConsecutive + 1; total > maxEsperado {
		t.Errorf("reintentó %d veces; el tope permite hasta %d", total, maxEsperado)
	}
	if total < 2 {
		t.Errorf("no reintentó ni una vez (intentos=%d)", total)
	}
}

// La alarma ETERNA no se rinde. Su caso de uso es un equipo robado: preferimos
// reintentar cada 30 s indefinidamente antes que quedarnos callados porque a
// alguien se le ocurrió matar el proceso varias veces seguidas.
func TestAlarmaEternaNoSeRindeNunca(t *testing.T) {
	comprimirTiemposDeRespawn(t)

	llegoElPrimero, intentos := spawnerDeUnSoloArranque(t)

	a := testAgent()
	if err := a.PlayAlarm(AlarmOptions{Forever: true}); err != nil {
		t.Fatalf("PlayAlarm falló: %v", err)
	}

	(<-llegoElPrimero).exited <- nil

	// Muy por encima del tope de la alarma con duración: si se rindiera, acá ya
	// estaría terminada.
	if esperarHasta(alarmaTerminada, 500*time.Millisecond) {
		t.Fatal("la alarma eterna se rindió; el equipo robado quedaría en silencio")
	}

	if total := intentos(); total <= 1+alarmRespawnMaxConsecutive {
		t.Errorf("la eterna sólo reintentó %d veces; debería seguir insistiendo más allá del tope (%d)",
			total, alarmRespawnMaxConsecutive)
	}

	// Y detenerla desde la consola tiene que cortar el backoff en el acto, no
	// esperar a que venza. Sin la espera interrumpible, StopAlarm se comía la gracia
	// completa y reportaba que la alarma no confirmó su término.
	inicio := time.Now()
	a.StopAlarm()
	if tardanza := time.Since(inicio); tardanza >= alarmStopGrace {
		t.Errorf("StopAlarm tardó %v: el backoff no es interrumpible", tardanza)
	}
	if !alarmaTerminada() {
		t.Error("StopAlarm volvió pero la alarma sigue registrada como vigente")
	}
}

// El backoff tiene que crecer, quedar acotado y —sobre todo— no desbordarse. Con la
// alarma eterna `fails` no tiene techo, y `1s << 40` no es una espera larga: es una
// duración negativa que `time.After` dispara de inmediato, devolviendo el bucle de
// spawn a toda velocidad que el backoff existe para evitar.
func TestBackoffCreceSeAcotaYNoSeDesborda(t *testing.T) {
	if got := alarmRespawnBackoff(0); got != alarmRespawnBaseBackoff {
		t.Errorf("alarmRespawnBackoff(0) = %v, se esperaba la base %v", got, alarmRespawnBaseBackoff)
	}
	if got := alarmRespawnBackoff(1); got != alarmRespawnBaseBackoff {
		t.Errorf("alarmRespawnBackoff(1) = %v, se esperaba la base %v", got, alarmRespawnBaseBackoff)
	}
	if got := alarmRespawnBackoff(2); got != 2*alarmRespawnBaseBackoff {
		t.Errorf("alarmRespawnBackoff(2) = %v, se esperaba el doble de la base", got)
	}

	anterior := time.Duration(0)
	for fails := 1; fails <= 200; fails++ {
		d := alarmRespawnBackoff(fails)

		if d <= 0 {
			t.Fatalf("alarmRespawnBackoff(%d) = %v: una espera nula o negativa deja el bucle a toda velocidad", fails, d)
		}
		if d > alarmRespawnMaxBackoff {
			t.Fatalf("alarmRespawnBackoff(%d) = %v, por encima del techo %v", fails, d, alarmRespawnMaxBackoff)
		}
		if d < anterior {
			t.Fatalf("alarmRespawnBackoff(%d) = %v bajó respecto de %v", fails, d, anterior)
		}
		anterior = d
	}

	if anterior != alarmRespawnMaxBackoff {
		t.Errorf("con muchos fallos el backoff quedó en %v y no llegó al techo %v", anterior, alarmRespawnMaxBackoff)
	}
}

func TestSoloLaAlarmaConDuracionSeRinde(t *testing.T) {
	if alarmGivesUp(true, 1_000_000) {
		t.Error("la alarma eterna se rindió: su caso de uso es un equipo robado")
	}
	if alarmGivesUp(false, alarmRespawnMaxConsecutive) {
		t.Error("se rindió justo en el tope; el tope es el último intento permitido")
	}
	if !alarmGivesUp(false, alarmRespawnMaxConsecutive+1) {
		t.Error("no se rindió pasado el tope")
	}
}
