package agent

import "fmt"

// borrarRutasSeguro (Linux) — feature 043 · T013 (pendiente en este incremento).
//
// Aquí irá el `shred`-like propio (overwrite → fsync → truncar → unlink) más la
// verificación por relectura (RN-08). Cero GPL: se reimplementa con syscalls, no se
// invoca `shred(1)`. Mientras no exista, reporta el fallo en vez de borrar sin
// verificar (ojo con FS CoW: btrfs/ZFS no garantizan pisar el bloque — ver
// investigation.md §3.2).
func borrarRutasSeguro(rutas []string) (map[string]string, bool, string, error) {
	return nil, false, "", fmt.Errorf(
		"wipe: borrado seguro Linux no implementado en este incremento (T013)")
}
