//go:build darwin

package agent

import (
	"os/exec"
	"regexp"
	"strings"
)

// Feature 041 · baseline keep-awake en macOS (T029).
//
// El paquete REACTIVO de geocerca es no-op en macOS (T020), pero el baseline SÍ
// actúa: es un prerrequisito de la medición y no depende de un caso abierto.
//
// Se captura `pmset -g custom` (para restaurar el sleep de AC previo) y se aplica
// `pmset -c sleep 0` (no dormir con corriente) + `pmset -a disablesleep 1` (freno
// duro, cubre el cierre de tapa). El revert re-habilita el sleep (`disablesleep 0`)
// y restaura el temporizador de sleep de AC previo.
//
// ⚠️ 🟡 `pmset -g custom` vuelca texto legible, NO reimportable directo; el revert
// parsea el valor de `sleep` de la sección AC. Se confirma en terreno. Ver la
// lacuna en actions.md.

func init() { baselineBackendImpl = darwinBaseline{} }

type darwinBaseline struct{}

// pmsetRunner es la costura para pruebas (en producción ejecuta pmset real).
var pmsetRunner = func(args ...string) ([]byte, error) {
	return exec.Command("pmset", args...).Output()
}

func (darwinBaseline) capturar() ([]byte, error) {
	out, err := pmsetRunner("-g", "custom")
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (darwinBaseline) aplicar() error {
	_, _ = pmsetRunner("-c", "sleep", "0")
	_, _ = pmsetRunner("-a", "disablesleep", "1")
	return nil
}

func (darwinBaseline) restaurar(backup []byte) error {
	// Lo esencial primero: soltar el freno duro. Aunque el parseo del temporizador
	// falle, el equipo vuelve a poder dormir.
	_, _ = pmsetRunner("-a", "disablesleep", "0")

	if prev, ok := acSleepPrevio(backup); ok {
		_, _ = pmsetRunner("-c", "sleep", prev)
	}
	return nil
}

// acSleepPrevioRe captura el valor de `sleep` dentro de la sección "AC Power:" del
// volcado de `pmset -g custom`.
var acSleepPrevioRe = regexp.MustCompile(`(?m)^\s*sleep\s+(\d+)`)

// acSleepPrevio extrae el temporizador de sleep de la sección AC del backup. La
// sección AC ("AC Power:") va primero en el volcado de pmset; se toma el primer
// `sleep N` desde ahí.
func acSleepPrevio(backup []byte) (string, bool) {
	texto := string(backup)
	idx := strings.Index(texto, "AC Power")
	if idx >= 0 {
		texto = texto[idx:]
	}
	if m := acSleepPrevioRe.FindStringSubmatch(texto); m != nil {
		return m[1], true
	}
	return "", false
}
