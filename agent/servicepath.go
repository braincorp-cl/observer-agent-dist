package agent

import "strings"

// Decisiones puras del reapuntado del servicio: parseo del BinaryPathName y la
// guarda de nombre del directorio que se borra al desinstalar.
//
// Viven en un archivo sin restricción de plataforma —y no junto al resto del
// código de servicios— porque son lógica sin E/S y así quedan cubiertas por los
// tests, que corren en Linux.
//
// De esto cuelga si un equipo con las rutas divergidas se repara o se queda
// ejecutando el binario viejo para siempre. Conviene que tenga tests y no sólo
// una corrida en terreno.

// serviceExeCandidates devuelve las rutas que podrían ser el ejecutable dentro
// de un BinaryPathName, de la más corta a la más larga.
//
// Con la ruta entre comillas hay una sola candidata y no hay nada que adivinar.
// Sin comillas, en cambio, Windows admite igual una ruta con espacios: entonces
// `C:\Program Files\ObserverAgent\observeragent.exe -m svc` es indistinguible de
// un ejecutable `C:\Program` con tres argumentos. El SCM resuelve la ambigüedad
// probando las candidatas contra el disco, de la más corta a la más larga, y
// quien llame a esta función debe hacer lo mismo.
//
// Devolver la lista en vez de una respuesta es lo que permite tener la parte
// difícil —cuáles son las candidatas y en qué orden— probada en Linux, y dejar
// del lado de Windows sólo el «¿existe este archivo?».
func serviceExeCandidates(binPath string) []string {
	binPath = strings.TrimSpace(binPath)
	if binPath == "" {
		return nil
	}

	if strings.HasPrefix(binPath, `"`) {
		if end := strings.Index(binPath[1:], `"`); end >= 0 {
			return []string{binPath[1 : end+1]}
		}
		// Comilla de apertura sin cierre: la ruta está malformada. Se ofrece lo
		// que hay, sin la comilla, y que el disco decida.
		return []string{strings.TrimPrefix(binPath, `"`)}
	}

	out := make([]string, 0, 4)
	for i := 0; i < len(binPath); i++ {
		if binPath[i] == ' ' {
			if cand := strings.TrimSpace(binPath[:i]); cand != "" {
				out = append(out, cand)
			}
		}
	}
	return append(out, binPath)
}

// parseServiceExePath extrae el ejecutable de un BinaryPathName sin mirar el
// disco, y dice si el parseo es CONFIABLE.
//
// Es el último recurso, para cuando ninguna candidata existe en disco y no queda
// nada contra qué contrastar. Cuando devuelve false, la cadena es ambigua y no
// hay forma honesta de resolverla.
func parseServiceExePath(binPath string) (string, bool) {
	trimmed := strings.TrimSpace(binPath)
	if trimmed == "" {
		return "", false
	}

	if strings.HasPrefix(trimmed, `"`) {
		if end := strings.Index(trimmed[1:], `"`); end >= 0 {
			return trimmed[1 : end+1], true
		}
		return strings.TrimPrefix(trimmed, `"`), false
	}

	sp := strings.Index(trimmed, " ")
	if sp < 0 {
		return trimmed, true
	}

	exe := trimmed[:sp]
	rest := strings.TrimSpace(trimmed[sp:])

	// Sin comillas, el primer token sólo es la ruta entera si lo que sigue es un
	// modificador. Si parece la continuación de la ruta, el corte cayó en medio
	// del nombre del directorio.
	if strings.HasPrefix(rest, "-") || strings.HasPrefix(rest, "/") {
		return exe, true
	}
	return exe, false
}

// replaceServiceExePath cambia el ejecutable de un BinaryPathName y deja los
// argumentos exactamente como estaban.
//
// El ejecutable viejo se recibe ya resuelto en vez de volver a parsearlo: quien
// llama pudo haberlo desambiguado contra el disco, y re-parsear acá con menos
// información daría un corte distinto y un resto inventado.
//
// Los argumentos no se reconstruyen a propósito: `-m svc` es lo que pone
// kardianos/service hoy, pero si un servicio quedó registrado con otra cosa,
// reescribirlo cambiaría su comportamiento sin que nadie lo haya pedido. Acá se
// corrige una ruta, nada más.
func replaceServiceExePath(binPath, oldExe, newExe string) string {
	trimmed := strings.TrimSpace(binPath)
	if oldExe == "" {
		return quoteExe(newExe)
	}

	rest := strings.TrimPrefix(trimmed, `"`+oldExe+`"`)
	if rest == trimmed {
		rest = strings.TrimPrefix(trimmed, oldExe)
	}
	if rest == trimmed {
		// El ejecutable no era prefijo de la cadena: no hay resto que conservar
		// que se pueda afirmar sin inventar.
		return quoteExe(newExe)
	}
	rest = strings.TrimSpace(rest)

	if rest == "" {
		return quoteExe(newExe)
	}
	return quoteExe(newExe) + " " + rest
}

func quoteExe(exe string) string {
	return `"` + exe + `"`
}

// staleDirNameLooksRight es la guarda de nombre del directorio que se borra al
// desinstalar: sólo se acepta uno que se llame como el directorio de instalación
// del agente.
func staleDirNameLooksRight(dir string) bool {
	return strings.EqualFold(windowsBase(dir), progFilesName)
}

// looksLikeAgentExe es la mitad barata de «¿esta ruta es el binario del agente?»:
// la que se puede decidir sin tocar el disco, y por lo tanto probar.
//
// Existe por un defecto concreto: al resolver un `BinaryPathName` sin comillas se
// aceptaba la primera candidata que existiera en disco, y la comprobación usada
// devolvía true para DIRECTORIOS. En `C:\Program Files (x86)\ObserverAgent\observeragent.exe -m svc`
// la candidata `C:\Program Files` existe —es un directorio— y se tomaba como el
// ejecutable; la reescritura dejaba entonces el servicio sin `-m svc` y el agente
// muerto, en una máquina que la consola ya no alcanza.
//
// El nombre solo no basta (hay que confirmar contra el disco que además es un
// archivo), pero descarta de entrada todo lo que ni siquiera se llama como el
// agente.
func looksLikeAgentExe(path string) bool {
	return strings.EqualFold(windowsBase(path), winExeName)
}

// windowsBase es el último componente de una ruta de Windows.
//
// No se usa filepath.Base a propósito: filepath habla el dialecto del sistema
// donde CORRE el programa, y en Linux —donde corren los tests— la barra
// invertida es un carácter común y `C:\Program Files\ObserverAgent` sería un solo
// componente. Extraer esta decisión para poder probarla no sirve de nada si la
// prueba mide otra cosa que el equipo.
func windowsBase(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimRight(p, `\/`)
	if p == "" {
		return ""
	}
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}
