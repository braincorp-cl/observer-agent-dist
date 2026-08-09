package agent

import (
	"strings"
	"testing"
)

// El rollback del Mesh corre en el peor momento posible: el enrolamiento acaba de
// fallar y el proceso se está por morir. No hay nadie mirando la salida, así que
// una invocación mal armada no se nota — se nota meses después, como un equipo
// con acceso remoto que nadie administra. Estos testigos fijan la invocación.
//
// Contexto del hallazgo: `-fulluninstall` es LOCAL por diseño (verificado en la
// fuente de MeshAgent el 2026-08-09: `modules/agent-installer.js` no importa una
// sola primitiva de red). Este rollback cierra el ACCESO en el equipo; el nodo
// que quedó en MeshCentral lo levanta el censo de huérfanos del servidor.

func TestMeshUninstallArgs(t *testing.T) {
	args := meshUninstallArgs()

	if len(args) != 2 {
		t.Fatalf("quiero 2 argumentos, tengo %d: %v", len(args), args)
	}

	if args[0] != "-fulluninstall" {
		t.Errorf("el comando tiene que ir primero; tengo %q", args[0])
	}

	// La trampa medida: el instalador JS anexado se dispara ante CUALQUIER
	// argumento. Sin esta bandera, `-fulluninstall` abre un diálogo en el
	// escritorio y se cuelga esperando un clic (Linux, 2026-07-29).
	if args[1] != "--no-embedded=1" {
		t.Errorf("falta o está fuera de lugar --no-embedded=1; tengo %q", args[1])
	}
}

func TestMeshUninstallShellCmd(t *testing.T) {
	casos := []struct {
		nombre string
		exe    string
		quiero string
	}{
		{
			nombre: "ruta del Mesh en macOS",
			exe:    "/usr/local/mesh_services/meshagent/meshagent",
			quiero: "/usr/local/mesh_services/meshagent/meshagent -fulluninstall --no-embedded=1",
		},
		{
			nombre: "ruta del Mesh en Linux",
			exe:    "/opt/observermesh/meshagent",
			quiero: "/opt/observermesh/meshagent -fulluninstall --no-embedded=1",
		},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := meshUninstallShellCmd(c.exe); got != c.quiero {
				t.Errorf("quiero %q, tengo %q", c.quiero, got)
			}
		})
	}
}

// Control negativo del testigo de arriba: si alguien invierte el orden, la línea
// que sale es DISTINTA de la que este archivo declara correcta. Sin esto, un test
// que sólo compara contra sí mismo pasaría igual con el orden invertido.
func TestMeshUninstallShellCmdDetectaOrdenInvertido(t *testing.T) {
	invertida := "/opt/observermesh/meshagent --no-embedded=1 -fulluninstall"

	if got := meshUninstallShellCmd("/opt/observermesh/meshagent"); got == invertida {
		t.Fatal("la línea armada quedó con el orden invertido")
	}

	// Y la bandera tiene que ir después del comando, no en cualquier lado.
	got := meshUninstallShellCmd("/opt/observermesh/meshagent")
	if strings.Index(got, "--no-embedded=1") < strings.Index(got, "-fulluninstall") {
		t.Errorf("--no-embedded=1 quedó antes del comando: %q", got)
	}
}
