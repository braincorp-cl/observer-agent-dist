package agent

import "unicode"

// titleCasePlatform capitaliza el nombre de la plataforma que reporta gopsutil
// ("ubuntu" -> "Ubuntu", "opensuse-leap" -> "Opensuse-Leap") para mostrarlo en la
// consola. Reemplaza a golang.org/x/text/cases, que era la ÚNICA razón por la que
// este árbol dependía de x/text.
//
// POR QUÉ SE DEJÓ DE USAR x/text: era el único aviso de seguridad alcanzable por
// símbolo que quedaba en los binarios de Linux y macOS —GO-2026-5970 /
// CVE-2026-56852, bucle infinito con entrada inválida en x/text/unicode/norm,
// alcanzado justo por esta llamada—, y no se podía cerrar subiendo el módulo: el
// arreglo (x/text v0.39.0) declara `go 1.25.0` y este go.mod se queda en `go 1.20`
// para que un solo árbol alimente los dos toolchains de Windows (ADR-023 §5). El
// uso era cosmético, así que salía más barato quitarlo que sostener la deuda.
//
// La regla: se abre palabra nueva al principio y después de cualquier rune que no
// sea letra, dígito, '_' o '.'; dentro de cada palabra se pone en mayúscula la
// primera letra y en minúscula todo el resto. Se derivó midiendo la salida real de
// `cases.Title(language.AmericanEnglish)` sobre 50 entradas antes de borrarlo, y
// las reproduce todas — incluidas las raras: "pop!_os" -> "Pop!_Os" ('!' corta,
// '_' no), "os-2/warp_x" -> "Os-2/Warp_x", "a.b.c" -> "A.b.c" ('.' no corta),
// "3cx" -> "3Cx" (la mayúscula va en la primera LETRA, no en el primer rune).
// Ese conjunto está congelado en platformtitle_test.go.
//
// ⚠️ NO es una reimplementación de la segmentación de palabras de Unicode (UAX #29)
// y no pretende serlo: sobre entradas patológicas puede divergir de x/text. El
// dominio real de esta función son los IDs de distro de /etc/os-release ("ubuntu",
// "debian", "linuxmint", "opensuse-leap", "rhel") y "darwin" en macOS.
func titleCasePlatform(s string) string {
	out := make([]rune, 0, len(s))
	nuevaPalabra := true
	for _, r := range s {
		parteDePalabra := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.'
		switch {
		case !parteDePalabra:
			// El separador se copia tal cual y abre palabra nueva.
			out = append(out, r)
			nuevaPalabra = true
		case nuevaPalabra && unicode.IsLetter(r):
			out = append(out, unicode.ToUpper(r))
			nuevaPalabra = false
		default:
			// Dígitos, '_' y '.' no consumen el turno de la mayúscula: en "3cx" la
			// mayúscula la recibe la 'c'. El resto de la palabra va en minúscula.
			out = append(out, unicode.ToLower(r))
		}
	}
	return string(out)
}
