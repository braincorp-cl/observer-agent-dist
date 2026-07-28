package agent

import "testing"

// Los dos ids válidos son REALES, leídos de equipos de la flota el 2026-07-28:
// FAZOCAR (binario crudo) y HP-ProOne-400 (binario auto-instalador, consultado
// con `--no-embedded=1`). Los inválidos son la salida que efectivamente llegó a
// ejecutarse por `eval` en el equipo nuevo.
const (
	idFazocar = "854BF86A14715296EAFA759DD788BDB243AC4FD063A8363D2BEE940E330DD340CA737DE96498B16EAE9D79DBF135D798"
	idHP      = "B5A5637418544B82ADEBE4C3F27B22DB7B207A756061AE52BA7CE2AA6BFE6D3F9D2B4E0A1D5B3EF3044218F3D917AAD4"
)

func TestEsNodeIDValido(t *testing.T) {
	casos := []struct {
		nombre  string
		entrada string
		quiero  bool
	}{
		{"id real de FAZOCAR", idFazocar, true},
		{"id real del HP con --no-embedded=1", idHP, true},
		{"id en minúsculas", "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", true},
		{"id con salto de línea al final", idFazocar + "\n", true},
		{"id con espacios alrededor", "  " + idHP + "  ", true},

		{"vacío", "", false},
		{"sólo espacios", "   \n\t", false},

		// El valor con el que quedó registrado el equipo: es hexadecimal
		// legítimo, pero es una MAC. Sin piso de largo, esto pasaba.
		{"la MAC que se registró como node id", "0A179C9229E0", false},

		// El centinela que devolvía la propia función: tres palabras que el
		// shell habría partido en tres argumentos.
		{"centinela de error viejo", "error getting meshnodeid", false},

		// Las líneas que el `eval` ejecutó como comandos.
		{"línea de servicio del instalador", "   -> Stopping service... [OK]", false},
		{"línea de error del instalador", "...Installing service [ERROR] fs.readFileSync(): File [/opt/observermesh/meshagent.msh] not found", false},
		{"salida multilínea completa", "   -> Checking\n   -> Stopping\n...Installing service [ERROR]", false},

		// Modos de falla que ya conocía el código de antes.
		{"error gráfico del instalador", "The graphical version of this installer cannot run on this system", false},
		{"aviso de zenity", "Try installing/updating Zenity, and run again", false},
		{"aviso de permisos", "Elevated permissions are required to install/uninstall this software.", false},

		// Un id del largo correcto pero con un carácter que no es hexadecimal
		// no debe pasar: descarta rutas y banderas que casualmente sean largas.
		{"largo correcto pero no hexadecimal", "Z4BF86A14715296EAFA759DD788BDB243AC4FD063A8363D2BEE940E330DD340CA737DE96498B16EAE9D79DBF135D798", false},
		{"un id válido seguido de basura", idFazocar + " && rm -rf /", false},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if hay := esNodeIDValido(c.entrada); hay != c.quiero {
				t.Errorf("esNodeIDValido(%q) = %v, quiero %v", c.entrada, hay, c.quiero)
			}
		})
	}
}
