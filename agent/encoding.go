package agent

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// utf8BOM es la marca de orden de bytes que identifica un archivo como UTF-8.
// PowerShell 5.1 es el unico consumidor que la necesita: sin ella lee el .ps1 con
// la pagina ANSI del sistema. Ver ScriptRun en agent_windows.go.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// DecodeCmdOutput convierte en UTF-8 valido la salida cruda de un proceso hijo.
//
// El agente no puede asumir que esa salida ya viene en UTF-8. En Windows, PowerShell
// 5.1 y cmd escriben en la pagina de codigos OEM de la consola, donde una letra
// acentuada es un solo byte que no forma una secuencia UTF-8 valida. El camino
// anterior pasaba la salida por strings.ToValidUTF8(s, ""), que BORRA esas
// secuencias: los acentos de cualquier script desaparecian sin dejar rastro, y no
// solo en los scripts de la biblioteca sino en los que escribe el operador.
//
// El criterio es deliberadamente conservador: si los bytes ya son UTF-8 validos se
// devuelven intactos, que es el caso de Linux, macOS, PowerShell 7 y de cualquier
// script que fije su propio encoding de salida. Solo cuando no lo son se intenta
// decodificar desde la pagina de codigos del sistema.
func DecodeCmdOutput(b []byte) string {
	// Los NUL sueltos vienen de binarios que escriben UTF-16 en la tuberia y rompen
	// la serializacion aguas arriba. Se quitan siempre, igual que hacia CleanString.
	b = bytes.ReplaceAll(b, []byte{0}, nil)

	if utf8.Valid(b) {
		return string(b)
	}

	if s, ok := decodeSystemCodePage(b); ok {
		return s
	}

	// Sin decodificador disponible se vuelve al comportamiento historico. Perder los
	// acentos es malo, pero devolver una string que no sea UTF-8 valida es peor: el
	// backend la rechaza al serializar la respuesta y se pierde la salida entera.
	return strings.ToValidUTF8(string(b), "")
}
