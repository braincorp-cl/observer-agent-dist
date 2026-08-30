package agent

// borrarRutasSeguro (Linux) — feature 043 · T013.
//
// `shred`-like propio sobre la stdlib de Go (cero GPL, NO invoca `shred(1)`):
// sobrescribe con datos aleatorios → `fsync` → trunca → `unlink`, con verificación
// por relectura (RN-08) en el núcleo portable.
//
// ⚠️ En sistemas de archivos copy-on-write (btrfs, ZFS) la sobrescritura lógica
// puede no pisar el bloque físico original; igual pasa con SSD (wear-leveling) y
// journals. La relectura sólo confirma que la ruta dejó de existir, no la
// irrecuperabilidad a nivel de bloque (ver investigation.md §3.2). Para garantía
// total el camino es crypto-erase / borrado de disco completo (A1/A3).
func borrarRutasSeguro(rutas []string) (map[string]string, bool, string, error) {
	return ejecutarBorradoSeguro(rutas, "overwrite-fsync-unlink/1pass")
}
