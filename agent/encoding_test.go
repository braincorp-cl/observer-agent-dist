package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDecodeCmdOutputRespetaUTF8(t *testing.T) {
	// El caso normal: Linux, macOS, PowerShell 7 y cualquier script que fije su
	// encoding de salida. Los bytes tienen que llegar intactos.
	casos := []string{
		"servicio detenido",
		"Configuracion aplicada",
		"acentos: aeiou con tilde -> áéíóú ñ Ü",
		"emoji y simbolos: ✓ — °C",
		"",
	}
	for _, c := range casos {
		if got := DecodeCmdOutput([]byte(c)); got != c {
			t.Errorf("DecodeCmdOutput(%q) = %q, se esperaba que no lo tocara", c, got)
		}
	}
}

func TestDecodeCmdOutputQuitaNUL(t *testing.T) {
	// Un binario que escribe UTF-16 en la tuberia mete NUL sueltos que rompen la
	// serializacion aguas arriba.
	if got := DecodeCmdOutput([]byte("es\x00ta\x00do")); got != "estado" {
		t.Errorf("DecodeCmdOutput no quito los NUL: %q", got)
	}
}

func TestDecodeCmdOutputSiempreDevuelveUTF8Valido(t *testing.T) {
	// La garantia que el backend necesita: pase lo que pase con la decodificacion,
	// la string que sale es UTF-8 valida. Si no lo fuera, se pierde la salida entera
	// al serializar la respuesta.
	rotos := [][]byte{
		{0xE1, 0xE9, 0xED},                   // acentos en CP1252/CP850
		{0xFF, 0xFE, 0x00, 0x41},             // preambulo UTF-16LE
		{0x48, 0x6F, 0x6C, 0x61, 0x20, 0xF1}, // "Hola " + n con virgulilla en un byte
	}
	for _, b := range rotos {
		got := DecodeCmdOutput(b)
		if !utf8.ValidString(got) {
			t.Errorf("DecodeCmdOutput(% x) devolvio UTF-8 invalido: %q", b, got)
		}
	}
}

func TestDecodeCmdOutputNoBorraEnSilencioEnWindows(t *testing.T) {
	// Regresion de O-LIB-01. En Windows los bytes de la pagina OEM tienen que
	// decodificarse a letras acentuadas, NO desaparecer. En el resto de las
	// plataformas no hay pagina de codigos que consultar y el fallback historico
	// (borrar la secuencia invalida) sigue siendo lo correcto.
	entrada := []byte{0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x69, 0x6F, 0x20, 0xE1} // "Servicio " + a con tilde
	got := DecodeCmdOutput(entrada)

	if !strings.HasPrefix(got, "Servicio ") {
		t.Fatalf("se perdio la parte ASCII de la salida: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("salida no es UTF-8 valida: %q", got)
	}

	if s, ok := decodeSystemCodePage(entrada); ok {
		// Estamos en Windows con una pagina de codigos utilizable.
		if s != got {
			t.Errorf("DecodeCmdOutput no uso el decodificador de la pagina de codigos: %q vs %q", got, s)
		}
		if len([]rune(got)) != len([]rune("Servicio "))+1 {
			t.Errorf("se esperaba un caracter acentuado al final, se obtuvo %q", got)
		}
	}
}

func TestUTF8BOM(t *testing.T) {
	// El BOM que ScriptRun antepone a los .ps1 tiene que ser exactamente este: si
	// cambia, PowerShell 5.1 vuelve a leer el archivo con la pagina ANSI.
	if len(utf8BOM) != 3 || utf8BOM[0] != 0xEF || utf8BOM[1] != 0xBB || utf8BOM[2] != 0xBF {
		t.Errorf("utf8BOM incorrecto: % x", utf8BOM)
	}
}
