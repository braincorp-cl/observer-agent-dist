package agent

import "strings"

// Manipulación del BinaryPathName de un servicio de Windows. Vive en un archivo
// sin restricción de plataforma —y no junto al resto del código de servicios—
// porque es manipulación de texto pura y así queda cubierta por los tests, que
// corren en Linux.
//
// Lo que se parsea acá decide si un equipo con las rutas divergidas se repara o
// se queda ejecutando el binario viejo para siempre, así que conviene que tenga
// tests y no sólo una corrida en terreno.

// serviceExePath extrae el ejecutable de un BinaryPathName, que trae los
// argumentos pegados y la ruta entre comillas cuando tiene espacios, que es
// siempre: el agente vive bajo «Program Files».
func serviceExePath(binPath string) string {
	binPath = strings.TrimSpace(binPath)
	if binPath == "" {
		return ""
	}

	if strings.HasPrefix(binPath, `"`) {
		if end := strings.Index(binPath[1:], `"`); end >= 0 {
			return binPath[1 : end+1]
		}
		// Comilla de apertura sin cierre: ruta malformada. Se devuelve lo que
		// hay sin la comilla en vez de inventar un corte.
		return strings.TrimPrefix(binPath, `"`)
	}

	if sp := strings.Index(binPath, " "); sp >= 0 {
		return binPath[:sp]
	}
	return binPath
}

// replaceServiceExePath cambia el ejecutable de un BinaryPathName y deja los
// argumentos exactamente como estaban.
//
// Los argumentos no se reconstruyen a propósito: `-m svc` es lo que pone
// kardianos/service hoy, pero si un servicio quedó registrado con otra cosa,
// reescribirlo cambiaría su comportamiento sin que nadie lo haya pedido. Acá se
// corrige una ruta, nada más.
func replaceServiceExePath(binPath, exe string) string {
	trimmed := strings.TrimSpace(binPath)
	old := serviceExePath(trimmed)
	if old == "" {
		return quoteExe(exe)
	}

	rest := strings.TrimPrefix(trimmed, `"`+old+`"`)
	if rest == trimmed {
		rest = strings.TrimPrefix(trimmed, old)
	}
	rest = strings.TrimSpace(rest)

	if rest == "" {
		return quoteExe(exe)
	}
	return quoteExe(exe) + " " + rest
}

func quoteExe(exe string) string {
	return `"` + exe + `"`
}
