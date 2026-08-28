package agent

import "fmt"

// borrarRutasSeguro (macOS) — feature 043 · T015: FUERA DE ALCANCE de A2.
//
// APFS es copy-on-write: la sobrescritura lógica no pisa el dato, y Apple retiró
// `srm`/`rm -P` justamente por eso. Con verificación por relectura OBLIGATORIA
// (RN-08), un wipe selectivo no puede garantizar `verified=true` en macOS. La vía
// correcta es crypto-erase (A1, feature aparte). Ver investigation.md §3.3.
func borrarRutasSeguro(rutas []string) (map[string]string, bool, string, error) {
	return nil, false, "", fmt.Errorf(
		"wipe: borrado selectivo fuera de alcance en macOS (APFS/CoW); usar crypto-erase A1")
}
