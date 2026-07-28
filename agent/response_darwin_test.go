//go:build darwin
// +build darwin

package agent

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// Feature 028 · el `exit 0` de pmset no prueba que se bloqueó.
//
// Las dos muestras son la salida REAL de
// `ioreg -n Root -d1 -r -k IOConsoleUsers` en un MacBook Pro con macOS 26.5.2
// (Apple Silicon), capturadas el 2026-07-26 antes y después de un bloqueo
// provocado por el mismo puente que usa el agente
// (`launchctl asuser 501 sudo -u <usuario> /usr/bin/pmset displaysleepnow`).
//
// Se recorta el diccionario de IOConsoleUsers y se deja el nivel de arriba,
// porque la salida completa son ~90 KB de propiedades del nodo Root que no
// aportan nada al parser.

const ioregDesbloqueado = `+-o Root  <class IORegistryEntry, id 0x100000100, retain 35>
    {
      "IOConsoleLocked" = No
      "IOConsoleUsers" = ({"kCGSSessionOnConsoleKey"=Yes,"kSCSecuritySessionID"=100023,"kCGSSessionSystemSafeBoot"=No,"kCGSessionLoginDoneKey"=Yes,"kCGSSessionIDKey"=257,"kCGSSessionUserNameKey"="dsepulveda","kCGSSessionGroupIDKey"=20,"CGSSessionUniqueSessionUUID"="65B34C2F-BACF-4042-802E-21E570B1E3A3","kCGSessionLongUserNameKey"="Daniel Sepulveda","kCGSSessionAuditIDKey"=100023,"kCGSSessionLoginwindowSafeLogin"=No,"kCGSSessionUserIDKey"=501})
    }
`

const ioregBloqueado = `+-o Root  <class IORegistryEntry, id 0x100000100, retain 35>
    {
      "IOConsoleLocked" = Yes
      "IOConsoleUsers" = ({"kCGSSessionOnConsoleKey"=Yes,"kSCSecuritySessionID"=100023,"kCGSSessionSystemSafeBoot"=No,"kCGSessionLoginDoneKey"=Yes,"kCGSSessionIDKey"=257,"kCGSSessionUserNameKey"="dsepulveda","kCGSSessionGroupIDKey"=20,"CGSSessionUniqueSessionUUID"="65B34C2F-BACF-4042-802E-21E570B1E3A3","CGSSessionScreenLockedTime"=1785110347,"kCGSessionLongUserNameKey"="Daniel Sepulveda","CGSSessionScreenIsLocked"=Yes,"kCGSSessionAuditIDKey"=100023,"kCGSSessionLoginwindowSafeLogin"=No,"kCGSSessionUserIDKey"=501})
    }
`

func TestIoregScreenIsLocked(t *testing.T) {
	casos := []struct {
		nombre string
		salida string
		quiero bool
	}{
		{"pantalla bloqueada (muestra real)", ioregBloqueado, true},
		{"pantalla desbloqueada (muestra real)", ioregDesbloqueado, false},

		// El caso que motiva la función: desbloqueado la clave NO está presente
		// como `=No`, sencillamente no aparece. Un parser que buscara un booleano
		// no encontraría nada y tendría que decidir qué hacer con la ausencia.
		{"la clave ausente NO es bloqueo", `"kCGSSessionOnConsoleKey"=Yes`, false},

		// ioreg no es consistente con los espacios: dentro del diccionario no los
		// pone y en el nivel de arriba sí. Si mañana cambia, el parser aguanta.
		{"tolera espacios alrededor del =", `"CGSSessionScreenIsLocked" = Yes`, true},

		// Salida vacía: si ioreg falla, el llamador ya trata el error, pero la
		// función no debe inventar un bloqueo.
		{"salida vacía", "", false},

		// La marca de tiempo del bloqueo aparece junto a la clave, pero por sí
		// sola no dice que SIGA bloqueado: no debe alcanzar.
		{"sólo CGSSessionScreenLockedTime no basta", `"CGSSessionScreenLockedTime"=1785110347`, false},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := ioregScreenIsLocked([]byte(c.salida)); got != c.quiero {
				t.Errorf("ioregScreenIsLocked() = %v, se esperaba %v", got, c.quiero)
			}
		})
	}
}

// TestBloqueoContraElEquipoReal ejecuta la verificación nueva contra la máquina
// donde corre, y **bloquea la pantalla de verdad**.
//
// Por eso está detrás de `OBSERVER_E2E_LOCK=1`: en CI no hay sesión gráfica y en
// el equipo de alguien dejaría la pantalla bloqueada sin avisar. Se corre a mano,
// en un Mac con sesión abierta, sabiendo que después hay que desbloquear:
//
//	OBSERVER_E2E_LOCK=1 ./agent.test -test.v -test.run TestBloqueoContraElEquipoReal
//
// Es la prueba que faltaba cuando se escribió el fix: los otros tests congelan el
// parser con muestras capturadas, y este verifica que en el equipo real la clave
// aparece de verdad —y en cuánto tiempo— después de que `pmset` sale 0.
func TestBloqueoContraElEquipoReal(t *testing.T) {
	if os.Getenv("OBSERVER_E2E_LOCK") != "1" {
		t.Skip("bloquea la pantalla de verdad; se activa con OBSERVER_E2E_LOCK=1")
	}

	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()

	if screenIsLocked(ctx) {
		t.Fatal("la pantalla ya estaba bloqueada: desbloqueá antes de correr esto, " +
			"o el test no prueba nada")
	}
	t.Log("estado inicial: desbloqueado (la clave NO está presente)")

	// Se invoca directo, sin `launchctl asuser`, porque el test ya corre como el
	// usuario de consola. El puente es lo que necesita el agente por ser un
	// demonio del sistema; acá sólo interesa el efecto de pmset.
	salida, err := exec.CommandContext(ctx, "/usr/bin/pmset", "displaysleepnow").CombinedOutput()
	if err != nil {
		t.Fatalf("pmset displaysleepnow falló: %v (%s)", err, salida)
	}
	t.Log("pmset displaysleepnow salió 0 — que es justamente lo que NO prueba nada")

	inicio := time.Now()
	if !(&Agent{}).screenLockConfirmed(ctx) {
		t.Fatalf("el SO no confirmó el bloqueo en %v; en este equipo debería "+
			"(revisá `sysadminctl -screenLock status`)", lockTimeout)
	}

	t.Logf("bloqueo CONFIRMADO por el SO en %v", time.Since(inicio).Round(10*time.Millisecond))
	t.Log("⚠️  la pantalla quedó bloqueada: hay que desbloquearla a mano")
}

// afplayPids devuelve los `afplay` vivos.
//
// Se usa `pgrep -x` y no `ps | grep` por una razón medida: el 2026-07-26 un
// `grep -c afplay` lanzado por SSH se contó a sí mismo y reportó alarma con la
// máquina en silencio.
func afplayPids(t *testing.T) []string {
	t.Helper()

	out, err := exec.Command("/usr/bin/pgrep", "-x", "afplay").Output()
	if err != nil {
		// pgrep sale 1 cuando no hay coincidencias. Eso es silencio, no un fallo.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return nil
		}
		t.Fatalf("pgrep falló: %v", err)
	}

	return strings.Fields(string(out))
}

// esperarAfplay sondea hasta `limite` a que haya sonido (o a que se haga
// silencio, según `quiero`).
func esperarAfplay(t *testing.T, quiero bool, limite time.Duration) ([]string, bool) {
	t.Helper()

	fin := time.Now().Add(limite)
	for {
		pids := afplayPids(t)
		if (len(pids) > 0) == quiero {
			return pids, true
		}
		if time.Now().After(fin) {
			return pids, false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestDetenerAlarmaNoDejaBucleHuerfano es la prueba del arreglo de T001, y
// **hace sonar la alarma de verdad**: por eso va detrás de
// `OBSERVER_E2E_ALARM=1`.
//
//	sudo OBSERVER_E2E_ALARM=1 ./agent.test -test.v -test.run TestDetenerAlarmaNoDejaBucleHuerfano
//
// Lo que verifica no es que el sonido se corte —eso pasaba incluso con el bug,
// porque el `afplay` en vuelo termina su .aiff— sino que **no vuelva a nacer**.
// Con el bucle huérfano en PID 1 el silencio duraba una repetición y después
// arrancaba la siguiente, para siempre.
//
// Se repite varias vueltas porque el bug era una carrera entre dos goroutines:
// una sola pasada verde no distingue "arreglado" de "esta vez gané la carrera".
func TestDetenerAlarmaNoDejaBucleHuerfano(t *testing.T) {
	if os.Getenv("OBSERVER_E2E_ALARM") != "1" {
		t.Skip("hace sonar la alarma de verdad; se activa con OBSERVER_E2E_ALARM=1")
	}
	if os.Geteuid() != 0 {
		t.Skip("hay que correrlo con sudo: el puente a la sesión usa launchctl asuser")
	}

	const vueltas = 6

	log := logrus.New()
	log.SetOutput(io.Discard)
	a := &Agent{Logger: log}

	if pids := afplayPids(t); len(pids) > 0 {
		t.Fatalf("ya había sonido antes de empezar (afplay %v): el test no probaría nada", pids)
	}

	for v := 1; v <= vueltas; v++ {
		// Duración larga a propósito: si el stop no funcionara, no quiero que lo
		// tape el vencimiento del plazo. Con el bug esto sonaba los 120 s enteros.
		if err := a.PlayAlarm(AlarmOptions{Seconds: 120}); err != nil {
			t.Fatalf("vuelta %d: PlayAlarm falló: %v", v, err)
		}

		pids, ok := esperarAfplay(t, true, 5*time.Second)
		if !ok {
			t.Fatalf("vuelta %d: la alarma no llegó a sonar en 5 s", v)
		}

		inicio := time.Now()
		a.StopAlarm()

		if _, ok := esperarAfplay(t, false, 5*time.Second); !ok {
			t.Fatalf("vuelta %d: seguía sonando 5 s después del stop (afplay %v)",
				v, afplayPids(t))
		}
		silencio := time.Since(inicio)

		// El rebrote es la firma del bug, y es lo que el "ok" de la API no veía.
		time.Sleep(2 * time.Second)
		if revividos := afplayPids(t); len(revividos) > 0 {
			t.Fatalf("vuelta %d: el bucle quedó huérfano — afplay %v revivió 2 s "+
				"después del stop", v, revividos)
		}

		t.Logf("vuelta %d: sonó (afplay %v), se detuvo en %v, sin rebrote",
			v, pids, silencio.Round(10*time.Millisecond))
	}
}

// TestSoloPmsetExigeConfirmacion congela la decisión de diseño: a `pmset` no se
// le cree el código de salida y a `CGSession` sí. Si alguien agrega un método
// nuevo, este test obliga a pensar en qué grupo va.
func TestSoloPmsetExigeConfirmacion(t *testing.T) {
	for _, c := range lockCandidates {
		switch {
		case strings.HasSuffix(c.path, "/pmset"):
			if !c.needsConfirm {
				t.Error("pmset displaysleepnow sale 0 aunque no bloquee: tiene que exigir confirmación")
			}
		case strings.HasSuffix(c.path, "/CGSession"):
			if c.needsConfirm {
				t.Error("CGSession habla con el servidor de ventanas y falla si no puede: su 0 es evidencia")
			}
		default:
			t.Errorf("método de bloqueo no contemplado (%q): decidí si su exit 0 es evidencia", c.path)
		}
	}
}

// TestLockTimeoutCabeEnLaEsperaDelServidor congela el acoplamiento con el
// backend: el servidor espera esta acción 15 s (`_endpoint_response` en
// agents/views.py). Si el agente tardara más, el operador vería
// `agent_unreachable` —un problema de red— en vez de saber que no se pudo
// bloquear.
func TestLockTimeoutCabeEnLaEsperaDelServidor(t *testing.T) {
	const esperaDelServidor = 15

	if lockTimeout.Seconds() >= esperaDelServidor {
		t.Errorf("lockTimeout = %v; debe quedar por debajo de los %d s que espera el servidor",
			lockTimeout, esperaDelServidor)
	}

	// Y el sondeo de un candidato tiene que caber dentro del techo total, o el
	// segundo método nunca llegaría a probarse.
	sondeo := lockConfirmInterval * time.Duration(lockConfirmTries)
	if sondeo >= lockTimeout {
		t.Errorf("el sondeo por candidato (%v) no cabe en lockTimeout (%v)", sondeo, lockTimeout)
	}
}
