package agent

import (
	"regexp"
	"strings"
)

// Feature 025 · autorización de ubicación en macOS (parte pura, sin build tag).
//
// La autorización de CoreLocation vive en /var/db/locationd/clients.plist, un
// plist BINARIO cuyas entradas incluyen blobs <data> (ClientStorageToken). Eso
// descarta el round-trip por JSON (plutil no serializa <data> a JSON) y no
// podemos depender de python (las Command Line Tools no están en un Mac de
// usuario). PlistBuddy tampoco sirve: usa ":" como separador de keypath y las
// claves de clients.plist CONTIENEN ":".
//
// Estrategia: convertir a XML con `plutil -convert xml1`, hacer una inserción
// QUIRÚRGICA de texto justo después del <dict> de apertura de la entrada que nos
// corresponde, y volver a binario con `plutil -convert binary1` previo
// `plutil -lint`. Al no reserializar el documento, los <data> y todas las demás
// entradas quedan intactas byte a byte.
//
// Esta lógica se aísla aquí, sin build tag, para poder testearla en CI Linux;
// el plumbing específico de macOS vive en geolocation_darwin.go.

// Claves que marcamos en la entrada del cliente. "Authorized" es la que concede
// el permiso; "Registered" evita que locationd trate la entrada como a medio
// registrar.
var locationAuthKeys = []string{"Authorized", "Registered"}

// reBoolOrStringPair matchea un par <key>NOMBRE</key> + su valor cuando este es
// booleano o string. locationd escribe "Registered" a veces como <true/> y a
// veces como <string></string>, así que hay que reconocer ambas formas para no
// dejar la clave duplicada al reescribirla.
func reBoolOrStringPair(name string) *regexp.Regexp {
	return regexp.MustCompile(
		`(?s)[ \t]*<key>` + regexp.QuoteMeta(name) +
			`</key>\s*(?:<(?:true|false)/>|<string>[^<]*</string>)\n?`)
}

var locationAuthPairRes = func() map[string]*regexp.Regexp {
	m := make(map[string]*regexp.Regexp, len(locationAuthKeys))
	for _, k := range locationAuthKeys {
		m[k] = reBoolOrStringPair(k)
	}
	return m
}()

// authorizeClients marca Authorized/Registered = true en toda entrada de
// clients.plist cuya CLAVE mencione bundleID. Devuelve el documento resultante y
// cuántas entradas modificó; 0 significa "no había nada que cambiar", que es la
// señal de idempotencia (ya autorizado ⇒ no reescribir ni reiniciar locationd).
func authorizeClients(doc, bundleID string) (string, int) {
	if bundleID == "" {
		return doc, 0
	}

	var out strings.Builder
	changed := 0
	i := 0

	for {
		rel := strings.Index(doc[i:], "<key>")
		if rel < 0 {
			break
		}
		keyOpen := i + rel
		relEnd := strings.Index(doc[keyOpen:], "</key>")
		if relEnd < 0 {
			break
		}
		keyClose := keyOpen + relEnd
		key := doc[keyOpen+len("<key>") : keyClose]
		afterKey := keyClose + len("</key>")

		if !strings.Contains(key, bundleID) {
			out.WriteString(doc[i:afterKey])
			i = afterKey
			continue
		}

		// Entre </key> y el valor solo puede haber espacios en blanco; si hay
		// otra cosa, no es la estructura que esperamos y se deja intacta.
		gapEnd := afterKey
		for gapEnd < len(doc) && (doc[gapEnd] == ' ' || doc[gapEnd] == '\t' || doc[gapEnd] == '\n' || doc[gapEnd] == '\r') {
			gapEnd++
		}

		// Diccionario vacío: se reemplaza entero.
		if strings.HasPrefix(doc[gapEnd:], "<dict/>") {
			out.WriteString(doc[i:gapEnd])
			out.WriteString("<dict>" + authKeysXML() + "</dict>")
			i = gapEnd + len("<dict/>")
			changed++
			continue
		}

		if !strings.HasPrefix(doc[gapEnd:], "<dict>") {
			out.WriteString(doc[i:afterKey])
			i = afterKey
			continue
		}

		bodyStart := gapEnd + len("<dict>")
		bodyEnd, ok := matchDictClose(doc, bodyStart)
		if !ok {
			out.WriteString(doc[i:afterKey])
			i = afterKey
			continue
		}

		body := doc[bodyStart:bodyEnd]
		if allAuthKeysTrue(body) {
			// Ya autorizado: se copia tal cual y NO se cuenta como cambio.
			out.WriteString(doc[i:bodyEnd])
			i = bodyEnd
			continue
		}

		out.WriteString(doc[i:bodyStart])
		out.WriteString(authKeysXML())
		out.WriteString(stripAuthKeys(body))
		i = bodyEnd
		changed++
	}

	out.WriteString(doc[i:])
	return out.String(), changed
}

// authKeysXML devuelve el bloque XML con las claves de autorización en true.
func authKeysXML() string {
	var b strings.Builder
	for _, k := range locationAuthKeys {
		b.WriteString("\n\t\t<key>" + k + "</key>\n\t\t<true/>")
	}
	b.WriteString("\n")
	return b.String()
}

// stripAuthKeys elimina del cuerpo los pares de las claves que vamos a reponer,
// para no dejarlas duplicadas.
func stripAuthKeys(body string) string {
	for _, k := range locationAuthKeys {
		body = locationAuthPairRes[k].ReplaceAllString(body, "")
	}
	return body
}

// allAuthKeysTrue indica si el cuerpo ya trae todas las claves en <true/>.
func allAuthKeysTrue(body string) bool {
	for _, k := range locationAuthKeys {
		if !keyIsTrue(body, k) {
			return false
		}
	}
	return true
}

// keyIsTrue busca <key>name</key> seguido (ignorando espacios) de <true/>.
func keyIsTrue(body, name string) bool {
	marker := "<key>" + name + "</key>"
	idx := strings.Index(body, marker)
	if idx < 0 {
		return false
	}
	rest := strings.TrimLeft(body[idx+len(marker):], " \t\r\n")
	return strings.HasPrefix(rest, "<true/>")
}

// matchDictClose devuelve el índice del "</dict>" que cierra el <dict> cuyo
// cuerpo empieza en start, respetando el anidamiento.
func matchDictClose(doc string, start int) (int, bool) {
	depth := 1
	i := start
	for i < len(doc) {
		nextOpen := strings.Index(doc[i:], "<dict>")
		nextClose := strings.Index(doc[i:], "</dict>")
		if nextClose < 0 {
			return 0, false
		}
		// Un <dict/> vacío no abre nivel, así que solo cuentan los <dict>.
		if nextOpen >= 0 && nextOpen < nextClose {
			depth++
			i += nextOpen + len("<dict>")
			continue
		}
		depth--
		if depth == 0 {
			return i + nextClose, true
		}
		i += nextClose + len("</dict>")
	}
	return 0, false
}
