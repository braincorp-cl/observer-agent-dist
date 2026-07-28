//go:build linux

package agent

import "testing"

// TestEscapeKdialogText congela lo que se MIDIÓ en `FAZOCAR` (LXQt) el 2026-07-28,
// con una captura de pantalla por variante. Las dos veces anteriores que este
// escapado se dedujo leyendo código ajeno se dedujo mal, así que los casos de acá
// son fotos, no teoría.
func TestEscapeKdialogText(t *testing.T) {
	casos := []struct {
		nombre  string
		entrada string
		quiero  string
	}{
		{
			// El caso que destapó el defecto: sin duplicar la barra, kdialog
			// convierte el \n en salto de línea, y ese salto hace que Qt deje de
			// ver rich text y muestre `&lt;urgente&gt;` crudo en pantalla.
			"ruta de Windows y entidades",
			`Revise C:\temp\nueva ruta - contacto <urgente> con Ventas & Cobranzas`,
			`<html>Revise C:\\temp\\nueva ruta - contacto &lt;urgente&gt; con Ventas &amp; Cobranzas</html>`,
		},
		{
			// Medido: con salto real y sin envoltura, las entidades salen crudas.
			"salto de línea real",
			"Linea uno\nLinea dos con <urgente>",
			`<html>Linea uno<br>Linea dos con &lt;urgente&gt;</html>`,
		},
		{
			"CRLF da UN solo br",
			"uno\r\ndos",
			`<html>uno<br>dos</html>`,
		},
		{
			"CR solo",
			"uno\rdos",
			`<html>uno<br>dos</html>`,
		},
		{
			// Medido: sin escapar entidades, kdialog INTERPRETA la etiqueta y la
			// persona ve "urgente" en negrita, sin el <b>.
			"etiqueta HTML de verdad se muestra literal",
			`Reporte <b>urgente</b>`,
			`<html>Reporte &lt;b&gt;urgente&lt;/b&gt;</html>`,
		},
		{
			// El & va primero o las entidades se re-escapan y sale &amp;lt;
			"el ampersand no se escapa dos veces",
			`a & b < c`,
			`<html>a &amp; b &lt; c</html>`,
		},
		{
			"texto sin nada que escapar",
			"Equipo extraviado",
			`<html>Equipo extraviado</html>`,
		},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := escapeKdialogText(c.entrada); got != c.quiero {
				t.Errorf("escapeKdialogText(%q)\n  dio    %q\n  quiero %q", c.entrada, got, c.quiero)
			}
		})
	}
}

// TestBuildAlertArgsUsaElEscapadoDeKdialog mira los argumentos que de verdad se le
// pasan al proceso: probar la función de escapado no prueba que alguien la llame,
// que es el hueco que ya apareció tres veces en esta feature.
func TestBuildAlertArgsUsaElEscapadoDeKdialog(t *testing.T) {
	var kd alertDialog
	for _, d := range alertDialogs {
		if d.bin == "kdialog" {
			kd = d
		}
	}
	if kd.bin == "" {
		t.Fatal("kdialog ya no está en la lista de diálogos")
	}

	args := buildAlertArgs(kd, `Equipo & perdido`, `Ruta C:\temp\nueva con <b>marca</b>`)

	quiero := `<html>Ruta C:\\temp\\nueva con &lt;b&gt;marca&lt;/b&gt;</html>`
	var vistoCuerpo bool
	for _, a := range args {
		if a == quiero {
			vistoCuerpo = true
		}
		// El título es el de la ventana y NO pasa por el parser: escaparlo
		// mostraría `&amp;` literal en la barra.
		if a == `Equipo &amp; perdido` {
			t.Error("el título no debe escaparse")
		}
	}
	if !vistoCuerpo {
		t.Errorf("el cuerpo no llegó escapado a los argumentos: %q", args)
	}
}
