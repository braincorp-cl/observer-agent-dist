package agent

// borrarRutasSeguro (Windows) — feature 043 · T012.
//
// Borrado seguro reimplementado sobre la stdlib de Go (cero GPL, sin `cipher` ni
// herramientas externas): sobrescribe el contenido con datos aleatorios y lo
// fuerza a disco, rompe los metadatos (rename a nombre aleatorio + truncar) y
// elimina el archivo, con verificación por relectura (RN-08) en el núcleo portable.
//
// Nivel NIST SP 800-88 "Clear": una pasada basta para medios magnéticos y SSD
// modernos. El TRIM/wear-leveling de los SSD puede dejar copias físicas fuera del
// alcance lógico; la relectura confirma que la ruta dejó de existir, no la
// irrecuperabilidad a nivel de celda (ver investigation.md §3.2).
func borrarRutasSeguro(rutas []string) (map[string]string, bool, string, error) {
	return ejecutarBorradoSeguro(rutas, "overwrite-rename-delete/1pass")
}
