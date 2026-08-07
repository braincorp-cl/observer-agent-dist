package agent

import "testing"

// Las dos rutas son las REALES de la divergencia 386 × x64, medida el
// 2026-08-07: el instalador deja el binario en `C:\Program Files\ObserverAgent`
// (setup.iss fija `{sd}\Program Files`) mientras el servicio quedó registrado en
// `C:\Program Files (x86)\ObserverAgent`, que es lo que vale `%ProgramFiles%`
// dentro de un proceso de 32 bits bajo WOW64.
//
// El formato del BinaryPathName es el que escribe kardianos/service: la ruta
// entre comillas y `-m svc` detrás.
const (
	exeViejo = `C:\Program Files (x86)\ObserverAgent\observeragent.exe`
	exeNuevo = `C:\Program Files\ObserverAgent\observeragent.exe`
	binViejo = `"C:\Program Files (x86)\ObserverAgent\observeragent.exe" -m svc`
)

func TestParseServiceExePath(t *testing.T) {
	casos := []struct {
		nombre    string
		entrada   string
		quiero    string
		confiable bool
	}{
		{"el caso real: ruta entre comillas con argumentos", binViejo, exeViejo, true},
		{"ruta entre comillas sin argumentos", `"` + exeNuevo + `"`, exeNuevo, true},
		{"ruta sin comillas ni espacios, con argumentos", `C:\obs\observeragent.exe -m svc`, `C:\obs\observeragent.exe`, true},
		{"ruta sin comillas ni espacios ni argumentos", `C:\obs\observeragent.exe`, `C:\obs\observeragent.exe`, true},
		{"con espacios alrededor", "  " + binViejo + "  ", exeViejo, true},
		{"sin comillas y con modificador estilo Windows", `C:\obs\observeragent.exe /m svc`, `C:\obs\observeragent.exe`, true},

		// Sin comillas y CON espacios en la ruta no hay forma de saber dónde
		// termina el ejecutable: el corte cae en medio del nombre del directorio.
		// El SCM lo resuelve probando candidatos contra el disco; acá no se puede,
		// así que se marca NO confiable y quien llama se abstiene de reescribir.
		// Sin esta marca, la reescritura producía
		//   "C:\...\observeragent.exe" Files\ObserverAgent\observeragent.exe -m svc
		// y el servicio arrancaba sin `-m svc`: sin RPC, agente offline en la
		// consola y el servicio "corriendo" para el usuario.
		{"ruta con espacios y sin comillas: ambigua", `C:\Program Files\obs.exe -m svc`, `C:\Program`, false},

		{"vacío", "", "", false},
		{"sólo espacios", "   ", "", false},
		{"comilla sin cerrar", `"C:\obs\observeragent.exe`, `C:\obs\observeragent.exe`, false},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			got, confiable := parseServiceExePath(c.entrada)
			if got != c.quiero || confiable != c.confiable {
				t.Errorf("parseServiceExePath(%q) = (%q, %v), quería (%q, %v)",
					c.entrada, got, confiable, c.quiero, c.confiable)
			}
		})
	}
}

// La guarda de nombre es lo último que separa una ruta leída de la configuración
// de un servicio de un os.RemoveAll.
func TestStaleDirNameLooksRight(t *testing.T) {
	casos := []struct {
		nombre  string
		entrada string
		quiero  bool
	}{
		{"el directorio huérfano real", `C:\Program Files (x86)\ObserverAgent`, true},
		{"con barra al final", `C:\Program Files (x86)\ObserverAgent\`, true},
		{"en otra unidad", `D:\ObserverAgent`, true},
		{"distinta caja", `C:\Program Files\OBSERVERAGENT`, true},

		{"la raíz del disco", `C:\`, false},
		{"Program Files entero", `C:\Program Files`, false},
		{"Windows", `C:\Windows`, false},
		{"el directorio de Mesh", `C:\Program Files\Mesh Agent`, false},
		{"un subdirectorio del directorio bueno", `C:\Program Files\ObserverAgent\bin`, false},
		{"vacío", "", false},
		{"sólo espacios", "   ", false},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := staleDirNameLooksRight(c.entrada); got != c.quiero {
				t.Errorf("staleDirNameLooksRight(%q) = %v, quería %v", c.entrada, got, c.quiero)
			}
		})
	}
}

func TestReplaceServiceExePath(t *testing.T) {
	casos := []struct {
		nombre  string
		entrada string
		exe     string
		quiero  string
	}{
		// El caso que repara la flota: cambia la ruta y `-m svc` queda intacto.
		// Si los argumentos se perdieran, el servicio arrancaría el agente sin
		// modo y no levantaría el RPC.
		{"el caso real", binViejo, exeNuevo, `"` + exeNuevo + `" -m svc`},

		{"sin argumentos", `"` + exeViejo + `"`, exeNuevo, `"` + exeNuevo + `"`},
		{"sin comillas, con argumentos", `C:\obs\observeragent.exe -m svc`, exeNuevo, `"` + exeNuevo + `" -m svc`},
		{"sin comillas ni argumentos", `C:\obs\observeragent.exe`, exeNuevo, `"` + exeNuevo + `"`},
		{"con espacios alrededor", "  " + binViejo + "  ", exeNuevo, `"` + exeNuevo + `" -m svc`},
		{"argumentos poco habituales se conservan tal cual", `"` + exeViejo + `" -m svc --debug`, exeNuevo, `"` + exeNuevo + `" -m svc --debug`},
		{"entrada vacía", "", exeNuevo, `"` + exeNuevo + `"`},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := replaceServiceExePath(c.entrada, c.exe); got != c.quiero {
				t.Errorf("replaceServiceExePath(%q, %q) = %q, quería %q", c.entrada, c.exe, got, c.quiero)
			}
		})
	}
}

// La reconciliación no debe tocar un servicio que ya está bien: en amd64 y en
// Windows de 32 bits genuino las dos rutas coinciden, y reescribir la
// configuración del servicio ahí sería trabajo —y riesgo— sin motivo.
func TestReplaceServiceExePathEsIdentidadCuandoYaCoincide(t *testing.T) {
	bin := `"` + exeNuevo + `" -m svc`
	if got := replaceServiceExePath(bin, exeNuevo); got != bin {
		t.Errorf("replaceServiceExePath(%q, %q) = %q, quería la misma cadena", bin, exeNuevo, got)
	}
}
