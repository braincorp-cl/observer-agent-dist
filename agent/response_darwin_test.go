//go:build darwin
// +build darwin

package agent

import (
	"strings"
	"testing"
	"time"
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
