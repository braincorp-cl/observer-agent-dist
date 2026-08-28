package agent

import "fmt"

// borrarRutasSeguro (Windows) — feature 043 · T012 (pendiente en este incremento).
//
// Aquí irá el borrado seguro reimplementado (sobrescribe contenido → rompe
// metadatos → DeleteFile) más la verificación por relectura (RN-08). Cero GPL: se
// usa la API nativa vía Go, no `cipher`/herramientas externas. Mientras no exista,
// reporta el fallo honestamente en vez de un "ok falso" o un borrado sin verificar.
func borrarRutasSeguro(rutas []string) (map[string]string, bool, string, error) {
	return nil, false, "", fmt.Errorf(
		"wipe: borrado seguro Windows no implementado en este incremento (T012)")
}
