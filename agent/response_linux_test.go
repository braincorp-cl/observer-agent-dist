//go:build linux
// +build linux

package agent

import (
	"strings"
	"testing"
)

// Feature 028 · fidelidad del texto del `alert` en Linux.
//
// Estas pruebas no verifican el escapado comparándolo con una cadena que yo mismo
// escribí a mano —eso sólo probaría que sé copiar—, sino **deshaciendo** lo que hace
// la herramienta y comprobando que se recupera el mensaje original. Si el escapado
// está de más, de menos o en el orden equivocado, el viaje de vuelta no cierra.

// gStrcompress imita a la función de GLib que zenity aplica al texto antes de
// parsear las marcas (`gtk_label_set_markup(g_strcompress(texto))`, `src/msg.c`).
// Convierte las secuencias con barra invertida en el carácter que representan.
func gStrcompress(s string) string {
	var b strings.Builder

	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}

		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'v':
			b.WriteByte('\v')
		case '\\', '"', '\'':
			b.WriteByte(s[i])
		default:
			b.WriteByte(s[i])
		}
	}

	return b.String()
}

// undoEntities imita al parser de marcas resolviendo las tres entidades que nos
// interesan. `&amp;` va al final, igual que en un parser real: si se resolviera
// primero, un `&amp;lt;` legítimo terminaría convertido en `<`.
func undoEntities(s string) string {
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&amp;", "&")
	return s
}

// mensajesQueRompen son los mensajes que un operador escribe de verdad y que sin
// escapado salen mutilados.
var mensajesQueRompen = []string{
	"Equipo bloqueado por el área de TI",
	"El stock quedó en a < b, revisar",
	"<urgente> devolver el equipo hoy",
	"Ventas & Cobranzas necesitan el notebook",
	`Recupera los archivos de C:\temp\nuevo antes del viernes`,
	`Ruta con tabulador falso: C:\trabajo`,
	`Barra suelta al final \`,
	"Mezcla: <b>Ventas & Cobranzas</b> en C:\\respaldo\\notas",
	"Acentos y ñ: la señal está caída",
	"Comillas \"dobles\" y 'simples'",
}

// dialogo busca un candidato por nombre de binario.
func dialogo(t *testing.T, bin string) alertDialog {
	t.Helper()

	for _, d := range alertDialogs {
		if d.bin == bin {
			return d
		}
	}
	t.Fatalf("no hay candidato para %s", bin)
	return alertDialog{}
}

// cuerpoEnviado devuelve el argumento del CUERPO tal como se le pasaría al proceso.
//
// Va por buildAlertArgs y no por la función de escapado a propósito: lo que hay que
// probar es lo que llega al diálogo. Probar sólo el escapado dejaba pasar la
// mutación que lo desconectaba del armado de argumentos.
func cuerpoEnviado(t *testing.T, d alertDialog, title, message string) string {
	t.Helper()

	args := buildAlertArgs(d, title, message)
	if len(args) != len(d.args) {
		t.Fatalf("%s: buildAlertArgs devolvió %d argumentos y la plantilla tiene %d",
			d.bin, len(args), len(d.args))
	}

	for i, arg := range d.args {
		if arg == "%BODY%" {
			return args[i]
		}
	}

	t.Fatalf("%s: la plantilla no tiene %%BODY%%", d.bin)
	return ""
}

func TestElTextoDeZenityLlegaIntactoAlUsuario(t *testing.T) {
	d := dialogo(t, "zenity")

	for _, original := range mensajesQueRompen {
		// El viaje completo de zenity: primero g_strcompress, después el parser de
		// marcas de Pango.
		visto := undoEntities(gStrcompress(cuerpoEnviado(t, d, "Aviso", original)))

		if visto != original {
			t.Errorf("el usuario habría leído otra cosa:\n  enviado: %q\n    leído: %q", original, visto)
		}
	}
}

// undoKdialog deshace, en orden inverso, todo lo que kdialog le hace al texto.
//
// ⚠️ Este modelo se MIDIÓ en `FAZOCAR` (LXQt) el 2026-07-28 con una captura de
// pantalla por variante. La versión anterior de este test decía que kdialog
// "parsea marcas pero NO descomprime barras invertidas", y eso era **falso**: es
// justo lo que hacía que el usuario leyera la ruta partida en dos y las entidades
// crudas. Cuando este modelo y la pantalla no coincidan, la que manda es la
// pantalla.
func undoKdialog(s string) string {
	s = strings.TrimPrefix(s, "<html>")
	s = strings.TrimSuffix(s, "</html>")
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = undoEntities(s)
	// Descomprime las barras igual que g_strcompress: medido, se queja por stderr
	// ("Unrecognized escape sequence \\t") con el texto crudo.
	return gStrcompress(s)
}

func TestElTextoDeKdialogLlegaIntacto(t *testing.T) {
	d := dialogo(t, "kdialog")

	for _, original := range mensajesQueRompen {
		visto := undoKdialog(cuerpoEnviado(t, d, "Aviso", original))

		if visto != original {
			t.Errorf("kdialog: el usuario habría leído otra cosa:\n  enviado: %q\n    leído: %q",
				original, visto)
		}
	}
}

// notify-send conserva el modelo viejo —entidades sí, barras no— porque a
// diferencia de kdialog **eso no está medido en ningún equipo**: el camino no se
// ejercitó en campo. Queda escrito para que la próxima medición sepa qué está
// asumiendo.
func TestElTextoDeNotifySendLlegaIntacto(t *testing.T) {
	d := dialogo(t, "notify-send")

	for _, original := range mensajesQueRompen {
		visto := undoEntities(cuerpoEnviado(t, d, "Aviso", original))

		if visto != original {
			t.Errorf("notify-send: el usuario habría leído otra cosa:\n  enviado: %q\n    leído: %q",
				original, visto)
		}
	}
}

// xmessage no parsea nada, así que su cuerpo tiene que viajar crudo: escaparlo
// mostraría `&lt;` literal en pantalla.
func TestXmessageRecibeElTextoCrudo(t *testing.T) {
	d := dialogo(t, "xmessage")

	for _, original := range mensajesQueRompen {
		if got := cuerpoEnviado(t, d, "Aviso", original); got != original {
			t.Errorf("xmessage recibió el cuerpo alterado:\n  enviado: %q\n  recibido: %q", original, got)
		}
	}
}

// El título NO se escapa: es el título de la ventana y no pasa por ningún parser.
func TestElTituloViajaSinEscapar(t *testing.T) {
	const title = "<Área de TI> & Soporte"

	for _, d := range alertDialogs {
		args := buildAlertArgs(d, title, "cuerpo")

		for i, arg := range d.args {
			if arg != "%TITLE%" {
				continue
			}
			if args[i] != title {
				t.Errorf("%s: el título se alteró: %q", d.bin, args[i])
			}
		}
	}
}

// El caso concreto del orden: si el `&` no se escapa primero, cada `<` termina en
// pantalla como `&lt;` literal en vez de como `<`.
func TestElAmpersandSeEscapaPrimero(t *testing.T) {
	got := escapeMarkupEntities("<")

	if got != "&lt;" {
		t.Fatalf("escapeMarkupEntities(\"<\") = %q, se esperaba %q", got, "&lt;")
	}
	if strings.Contains(got, "&amp;") {
		t.Errorf("el escapado se aplicó en el orden equivocado: %q", got)
	}
}

// La mitad exclusiva de zenity. Se prueba aparte porque es la que se descubrió
// leyendo su fuente y la que `--no-markup` no habría arreglado.
func TestZenityDuplicaLaBarraInvertida(t *testing.T) {
	if got := escapeZenityText(`C:\temp`); got != `C:\\temp` {
		t.Errorf("escapeZenityText(%q) = %q; sin duplicar la barra, \\t se vuelve un tabulador", `C:\temp`, got)
	}

	// Y el escapado de entidades NO debe duplicar barras: kdialog las muestra tal cual.
	if got := escapeMarkupEntities(`C:\temp`); got != `C:\temp` {
		t.Errorf("escapeMarkupEntities tocó la barra invertida: %q", got)
	}
}

// El cuerpo se escapa; el título NO. El título es el de la ventana y no pasa por
// ningún parser, así que escaparlo mostraría un `&lt;` literal en la barra.
func TestSoloElCuerpoSeEscapa(t *testing.T) {
	for _, d := range alertDialogs {
		tieneCuerpo := false
		for _, arg := range d.args {
			if arg == "%BODY%" {
				tieneCuerpo = true
			}
		}
		if !tieneCuerpo {
			t.Errorf("%s no recibe el cuerpo del mensaje", d.bin)
		}
	}

	// xmessage es el único que no parsea nada.
	for _, d := range alertDialogs {
		if d.bin == "xmessage" && d.escapeBody != nil {
			t.Error("a xmessage se le escapa el cuerpo y no parsea marcas: mostraría entidades literales")
		}
		if d.bin != "xmessage" && d.escapeBody == nil {
			t.Errorf("%s parsea el cuerpo y quedó sin escapar", d.bin)
		}
	}
}
