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

// Las candidatas son lo que permite reparar un equipo cuyo servicio quedó
// registrado sin comillas: el lado de Windows prueba cada una contra el disco,
// como hace el propio SCM, y se queda con la primera que existe.
func TestServiceExeCandidates(t *testing.T) {
	casos := []struct {
		nombre  string
		entrada string
		quiero  []string
	}{
		{"el caso real: entre comillas, una sola candidata", binViejo, []string{exeViejo}},
		{"entre comillas sin argumentos", `"` + exeNuevo + `"`, []string{exeNuevo}},

		// Sin comillas y con espacios en la ruta: el corte cae en medio del
		// nombre del directorio y sólo el disco puede decir cuál es la buena.
		// El orden va de la más corta a la más larga, igual que Windows.
		{"sin comillas, con espacios en la ruta", `C:\Program Files\ObserverAgent\observeragent.exe -m svc`, []string{
			`C:\Program`,
			`C:\Program Files\ObserverAgent\observeragent.exe`,
			`C:\Program Files\ObserverAgent\observeragent.exe -m`,
			`C:\Program Files\ObserverAgent\observeragent.exe -m svc`,
		}},

		{"sin comillas ni espacios en la ruta", `C:\obs\observeragent.exe -m svc`, []string{
			`C:\obs\observeragent.exe`,
			`C:\obs\observeragent.exe -m`,
			`C:\obs\observeragent.exe -m svc`,
		}},

		{"sin comillas ni argumentos", `C:\obs\observeragent.exe`, []string{`C:\obs\observeragent.exe`}},
		{"comilla sin cerrar", `"C:\obs\observeragent.exe`, []string{`C:\obs\observeragent.exe`}},
		{"vacío", "", nil},
		{"sólo espacios", "   ", nil},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			got := serviceExeCandidates(c.entrada)
			if len(got) != len(c.quiero) {
				t.Fatalf("serviceExeCandidates(%q) = %q, quería %q", c.entrada, got, c.quiero)
			}
			for i := range got {
				if got[i] != c.quiero[i] {
					t.Fatalf("serviceExeCandidates(%q) = %q, quería %q", c.entrada, got, c.quiero)
				}
			}
		})
	}
}

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

// El defecto que esto cierra fue el peor de los tres arreglos: la resolucion
// contra el disco usaba una comprobacion que devuelve true para DIRECTORIOS, asi
// que `C:\Program Files` se aceptaba como «el ejecutable» y la reescritura dejaba
// el servicio sin `-m svc`. Agente muerto, y sin downgrade ni update lateral que
// lo rescate: hay que ir a la maquina.
func TestLooksLikeAgentExe(t *testing.T) {
	casos := []struct {
		nombre  string
		entrada string
		quiero  bool
	}{
		{"el binario real", `C:\Program Files\ObserverAgent\observeragent.exe`, true},
		{"en el directorio viejo", `C:\Program Files (x86)\ObserverAgent\observeragent.exe`, true},
		{"distinta caja", `C:\obs\OBSERVERAGENT.EXE`, true},
		{"sin directorio", `observeragent.exe`, true},

		// Las candidatas que produce un BinaryPathName sin comillas. La segunda
		// EXISTE en cualquier Windows, y por ser un directorio se colaba.
		{"candidata C:\\Program", `C:\Program`, false},
		{"candidata C:\\Program Files (un DIRECTORIO que existe)", `C:\Program Files`, false},
		{"candidata con argumentos pegados", `C:\Program Files\ObserverAgent\observeragent.exe -m`, false},

		{"el desinstalador", `C:\Program Files\ObserverAgent\unins000.exe`, false},
		{"el meshagent", `C:\Program Files\ObserverAgent\meshagent.exe`, false},
		{"la raíz del disco", `C:\`, false},
		{"vacío", "", false},
		{"sólo espacios", "   ", false},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := looksLikeAgentExe(c.entrada); got != c.quiero {
				t.Errorf("looksLikeAgentExe(%q) = %v, quería %v", c.entrada, got, c.quiero)
			}
		})
	}
}

func TestReplaceServiceExePath(t *testing.T) {
	sinComillas := `C:\Program Files (x86)\ObserverAgent\observeragent.exe -m svc`

	casos := []struct {
		nombre  string
		entrada string
		viejo   string
		nuevo   string
		quiero  string
	}{
		// El caso que repara la flota: cambia la ruta y `-m svc` queda intacto.
		// Si los argumentos se perdieran, el servicio arrancaría el agente sin
		// modo y no levantaría el RPC.
		{"el caso real", binViejo, exeViejo, exeNuevo, `"` + exeNuevo + `" -m svc`},

		// El que antes producía basura: sin comillas y con espacios. Ahora el
		// ejecutable viejo llega YA resuelto contra el disco, así que el resto
		// que se conserva es el de verdad y no un pedazo de la ruta.
		{"sin comillas y con espacios, ya desambiguado", sinComillas, exeViejo, exeNuevo, `"` + exeNuevo + `" -m svc`},

		{"sin argumentos", `"` + exeViejo + `"`, exeViejo, exeNuevo, `"` + exeNuevo + `"`},
		{"sin comillas ni espacios", `C:\obs\observeragent.exe -m svc`, `C:\obs\observeragent.exe`, exeNuevo, `"` + exeNuevo + `" -m svc`},
		{"sin comillas ni argumentos", `C:\obs\observeragent.exe`, `C:\obs\observeragent.exe`, exeNuevo, `"` + exeNuevo + `"`},
		{"con espacios alrededor", "  " + binViejo + "  ", exeViejo, exeNuevo, `"` + exeNuevo + `" -m svc`},
		{"argumentos poco habituales se conservan tal cual", `"` + exeViejo + `" -m svc --debug`, exeViejo, exeNuevo, `"` + exeNuevo + `" -m svc --debug`},
		{"entrada vacía", "", "", exeNuevo, `"` + exeNuevo + `"`},

		// Si el ejecutable recibido no es prefijo de la cadena, no hay resto que
		// se pueda afirmar: se prefiere una línea sin argumentos a una inventada.
		{"viejo que no calza con la cadena", binViejo, `D:\otro\cosa.exe`, exeNuevo, `"` + exeNuevo + `"`},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := replaceServiceExePath(c.entrada, c.viejo, c.nuevo); got != c.quiero {
				t.Errorf("replaceServiceExePath(%q, %q, %q) = %q, quería %q", c.entrada, c.viejo, c.nuevo, got, c.quiero)
			}
		})
	}
}

// La reconciliación no debe tocar un servicio que ya está bien: en amd64 y en
// Windows de 32 bits genuino las dos rutas coinciden, y reescribir la
// configuración del servicio ahí sería trabajo —y riesgo— sin motivo.
func TestReplaceServiceExePathEsIdentidadCuandoYaCoincide(t *testing.T) {
	bin := `"` + exeNuevo + `" -m svc`
	if got := replaceServiceExePath(bin, exeNuevo, exeNuevo); got != bin {
		t.Errorf("replaceServiceExePath(%q, %q, %q) = %q, quería la misma cadena", bin, exeNuevo, exeNuevo, got)
	}
}
