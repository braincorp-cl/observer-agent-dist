package agent

import "strings"

// Validación de la respuesta de `meshagent -nodeid`.
//
// Vive sin etiqueta de plataforma a propósito, igual que comhresult.go: la CI
// corre `go test` en Linux y macOS, y esta regla es demasiado delicada como para
// quedar sin ejercitar.
//
// ## Por qué existe
//
// El binario que MeshCentral entrega para instalar —el que baja el .sh desde
// `/meshagents?id=<meshid>&meshinstall=<arch>`— NO es el agente: es un
// **auto-instalador**, el agente con un script JS anexado al final. Ese script
// se ejecuta ante CUALQUIER argumento, así que `meshagent -nodeid` no devuelve
// un identificador sino la salida completa de una reinstalación:
//
//	   -> Checking for previous installation of "meshagent" [FOUND]
//	   -> Stopping service... [OK]
//	   -> Uninstalling service... [OK]
//	...Installing service [ERROR] fs.readFileSync(): File [...meshagent.msh] not found
//
// Esas líneas terminaban concatenadas en el comando de instalación que el .sh
// arma y luego pasa por `eval`, o sea que **el shell las ejecutaba como
// comandos**. El servicio del mesh quedaba caído y el agente se registraba con
// una MAC en vez de un node id, dejando "Tomar control" inutilizable.
//
// El arreglo de fondo es pedir el id con `--no-embedded=1`, que salta el script
// anexado; esta validación es la segunda línea de defensa, para que ninguna
// salida inesperada vuelva a llegar hasta un `eval`.
//
// ⚠️ El orden de los argumentos importa y está medido: `-nodeid --no-embedded=1`
// devuelve el id, mientras que `--no-embedded=1 -nodeid` devuelve vacío.
func esNodeIDValido(s string) bool {
	s = strings.TrimSpace(s)

	if s == "" {
		return false
	}

	// Un node id es un único token. Cualquier espacio, tabulación o salto de
	// línea delata que lo que volvió es un mensaje y no un identificador —
	// incluido el "error getting meshnodeid" que devolvía esta misma función.
	if strings.ContainsAny(s, " \t\r\n") {
		return false
	}

	// El alfabeto solo no alcanza: el valor con el que quedó registrado el
	// equipo que destapó esto fue `0A179C9229E0`, que es hexadecimal legítimo
	// — es una dirección MAC. Por eso además se exige un largo de identificador.
	//
	// Hoy MeshCentral usa el SHA-384 del certificado del agente, o sea 96
	// caracteres. No se fija ese número exacto para no atarse a una versión,
	// pero sí un piso que descarte MACs, seriales y palabras sueltas.
	//
	// Pasarse de estricto acá es barato: si un id válido fuera rechazado, el
	// agente se instala sin él y SyncMeshNodeID lo corrige en el primer ciclo.
	// Dejar pasar basura, en cambio, ya costó un equipo con el mesh caído.
	const largoMinimo = 64

	if len(s) < largoMinimo {
		return false
	}

	for _, r := range s {
		esHex := (r >= '0' && r <= '9') || (r >= 'A' && r <= 'F') || (r >= 'a' && r <= 'f')
		if !esHex {
			return false
		}
	}

	return true
}
