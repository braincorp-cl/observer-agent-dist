//go:build !windows && !linux && !darwin

package agent

import "fmt"

// borrarRutasSeguro (otros SO) — feature 043: sin implementación de borrado seguro.
func borrarRutasSeguro(rutas []string) (map[string]string, bool, string, error) {
	return nil, false, "", fmt.Errorf("wipe: borrado seguro no soportado en este SO")
}
