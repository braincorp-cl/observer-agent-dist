package agent

import "strings"

// Decisiones puras del reapuntado del servicio: parseo del BinaryPathName y la
// guarda de nombre del directorio huérfano.
//
// Viven en un archivo sin restricción de plataforma —y no junto al resto del
// código de servicios— porque son lógica sin E/S y así quedan cubiertas por los
// tests, que corren en Linux.
//
// De esto cuelga si un equipo con las rutas divergidas se repara o se queda
// ejecutando el binario viejo para siempre, y también un os.RemoveAll. Conviene
// que tengan tests y no sólo una corrida en terreno.

// parseServiceExePath extrae el ejecutable de un BinaryPathName y dice si el
// parseo es CONFIABLE.
//
// El segundo valor no es un adorno. Windows admite registrar el servicio sin
// comillas aunque la ruta tenga espacios, y entonces «C:\Program Files\...» es
// indistinguible de un ejecutable «C:\Program» con argumentos: el SCM lo resuelve
// probando candidatos contra el disco, cosa que acá no se puede replicar. Cuando
// la cadena es ambigua se devuelve false y quien llama debe abstenerse de
// reescribir: preferimos no tocar un servicio antes que dejarlo con una línea de
// comandos inventada.
func parseServiceExePath(binPath string) (string, bool) {
	binPath = strings.TrimSpace(binPath)
	if binPath == "" {
		return "", false
	}

	if strings.HasPrefix(binPath, `"`) {
		end := strings.Index(binPath[1:], `"`)
		if end < 0 {
			// Comilla de apertura sin cierre: la ruta está malformada y no hay
			// forma honesta de saber dónde termina.
			return strings.TrimPrefix(binPath, `"`), false
		}
		return binPath[1 : end+1], true
	}

	sp := strings.Index(binPath, " ")
	if sp < 0 {
		return binPath, true
	}

	exe := binPath[:sp]
	rest := strings.TrimSpace(binPath[sp:])

	// Sin comillas, el primer token sólo es la ruta entera si lo que sigue es un
	// modificador. Si lo que sigue parece la continuación de la ruta, el corte
	// cayó en medio del nombre del directorio.
	if strings.HasPrefix(rest, "-") || strings.HasPrefix(rest, "/") {
		return exe, true
	}
	return exe, false
}

// serviceExePath es parseServiceExePath cuando sólo interesa comparar rutas y no
// reescribirlas.
func serviceExePath(binPath string) string {
	exe, _ := parseServiceExePath(binPath)
	return exe
}

// replaceServiceExePath cambia el ejecutable de un BinaryPathName y deja los
// argumentos exactamente como estaban.
//
// Los argumentos no se reconstruyen a propósito: `-m svc` es lo que pone
// kardianos/service hoy, pero si un servicio quedó registrado con otra cosa,
// reescribirlo cambiaría su comportamiento sin que nadie lo haya pedido. Acá se
// corrige una ruta, nada más.
//
// Sólo debe llamarse cuando parseServiceExePath dio confiable: con una cadena
// ambigua, el resto que se conserva incluiría el pedazo de ruta que quedó
// cortado, y el servicio arrancaría con un argumento posicional basura que hace
// que Go deje de leer `-m svc`.
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

// staleDirNameLooksRight es la guarda de nombre del directorio que se va a
// borrar: sólo se acepta uno que se llame como el directorio de instalación del
// agente.
//
// Es la última línea entre una ruta leída de la configuración de un servicio
// —que puede ser cualquier cosa— y un os.RemoveAll.
func staleDirNameLooksRight(dir string) bool {
	return strings.EqualFold(windowsBase(dir), progFilesName)
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
