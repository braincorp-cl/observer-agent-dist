package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Feature 030 · Fase 2 · T016b/T016c — gestión de los permisos TCC de macOS.
//
// Acá vive lo que NO depende del sistema: los códigos, el estado que se
// persiste y la decisión de si corresponde molestar a alguien. La parte que
// toca el SO está en tccpermisos_darwin.go. La separación es la misma de
// webcam.go/webcam_darwin.go y tiene el mismo motivo: que la CI —que corre en
// Linux— pueda probar la lógica que decide, porque es donde están los errores
// caros (avisar de más, avisar de menos, avisar en el peor momento).
//
// ── POR QUÉ EXISTE ESTO ───────────────────────────────────────────────────────
//
// En macOS los permisos de Cámara y Grabación de pantalla NO se conceden por
// administración remota: el perfil PPPC de Apple sólo sabe NEGARLOS. La única
// vía es el diálogo del sistema, con una persona frente al equipo. Medido en
// terreno cuatro veces (la última el 2026-08-13), quedaron establecidos tres
// hechos que este archivo asume como cimiento:
//
//  1. **El sujeto del permiso es el `responsible process`, no quien abre el
//     dispositivo.** Con la petición lanzada como hija del agente, `tccd`
//     publica `Sub:{/opt/observeragent/observeragent}` y la fila nace a nombre
//     del agente y con su cdhash vigente. Por eso el agente puede pedir el
//     permiso aunque la foto la saque después otro proceso — SIEMPRE QUE ESE
//     OTRO PROCESO SEA EL MISMO BINARIO EN LA MISMA RUTA (ver más abajo).
//
//  2. **Desde SSH no se puede.** Ahí `tccd` responde `Policy disallows prompt`
//     y deniega SIN preguntar, devolviendo el estado en 0 — indistinguible de
//     una persona que dice que no. O sea: esta ronda sólo tiene sentido
//     lanzada por el servicio, jamás desde una sesión remota.
//
//  3. **El auto-update mata los permisos.** El agente se firma ad-hoc, así que
//     el `csreq` que guarda TCC es un literal de cdhash y cambia con cada
//     compilación. La tabla `access` tiene UNA fila por (servicio, cliente): el
//     update no la pierde, la SOBRESCRIBE con una denegación anclada al binario
//     nuevo. Y una denegación explícita es peor que la ausencia de fila, porque
//     el sistema ya no considera preguntar. De ahí T016c.
//
// 🪤 **Y apagar y encender el interruptor de Ajustes NO arregla nada**: medido,
// reescribe la fila conservando el requisito viejo. Lo ÚNICO que re-ancla el
// csreq al binario vigente es el diálogo. Por eso este código nunca manda a
// nadie a Ajustes como si eso curara: abre el panel sólo como último recurso
// informativo, después de haber intentado lo que sí funciona.
//
// 🔑 **Nota para T016 (el LaunchAgent auxiliar):** la foto de webcam necesita
// contexto Aqua, y la cura identificada es un LaunchAgent. Ese LaunchAgent debe
// ejecutar ESTE MISMO BINARIO EN ESTA MISMA RUTA. TCC identifica a un ejecutable
// suelto por su ruta, así que compartiendo binario comparte la fila de `access`
// y el permiso que concede esta ronda le sirve tal cual. Un helper aparte
// tendría su propia fila y su propio cdhash, y habría que conceder dos veces lo
// mismo.
//
// 🚫 **NUNCA durante un ciclo de modo perdido activo.** Un diálogo de permiso en
// un equipo robado le avisa al ladrón que alguien está mirando, que es justo lo
// que la feature evita. La guardia está en `RondaPermisosTCC`.
//
// ⚠️ Todo esto es un PALIATIVO, y conviene que quede escrito acá: con firma
// Developer ID + notarización (ADR-018) el `csreq` pasaría a ser por identidad
// de firma, los permisos sobrevivirían al update y no habría nada que repedir.
// Mientras no haya certificado, esto es lo que hay.

// Servicios TCC que el agente gestiona. Los nombres son los que entiende
// `tccutil` (y los que aparecen en la TCC.db con el prefijo `kTCCService`).
const (
	tccServicioCamara   = "Camera"
	tccServicioPantalla = "ScreenCapture"
)

// tccServiciosGestionados es el orden en que se piden. La cámara va primero
// porque es la que ya se midió concediéndose bien; si algo sale mal en la
// pantalla, la evidencia de mayor peso legal ya quedó habilitada.
var tccServiciosGestionados = []string{tccServicioCamara, tccServicioPantalla}

// Estados de AVAuthorizationStatus, que es la escala que usa macOS para la
// cámara. La pantalla no expone escala —`CGPreflightScreenCaptureAccess` es un
// booleano—, así que ahí sólo se distingue concedido de no concedido.
const (
	tccEstadoNoDeterminado = 0
	tccEstadoRestringido   = 1
	tccEstadoDenegado      = 2
	tccEstadoConcedido     = 3
)

const (
	// tccMaxRondasPorIdentidad limita cuántas veces se insiste con el MISMO
	// binario. Si la persona dijo que no dos veces, insistir es acoso y no
	// producto. El contador se reinicia solo cuando cambia el binario, que es
	// justo el evento que vuelve a hacer necesaria la pregunta.
	tccMaxRondasPorIdentidad = 2

	// tccEsperaEntreRondas separa dos rondas de la misma identidad. Cubre el
	// caso del equipo que se reinicia varias veces el mismo día: sin esto, cada
	// arranque del servicio sería un diálogo.
	tccEsperaEntreRondas = 24 * time.Hour

	// tccArchivoEstado vive junto al resto del estado del agente en
	// /opt/observeragent, que el desinstalador borra con RemoveAll.
	tccArchivoEstado = "tcc_permisos.json"

	// tccMarcaInstalacion la deja el instalador para que el servicio pida los
	// permisos en su primer arranque. NO los pide el instalador: un proceso
	// lanzado desde Terminal.app queda atribuido a Terminal, y el permiso se
	// crearía a nombre del terminal en vez del agente. La ronda tiene que
	// salir del servicio.
	tccMarcaInstalacion = "tcc_pedir_permisos"
)

// intentoTCC es lo que se recuerda de un servicio entre arranques.
type intentoTCC struct {
	Rondas     int       `json:"rondas"`
	Ultima     time.Time `json:"ultima"`
	Concedido  bool      `json:"concedido"`
	Aviso      bool      `json:"aviso_mostrado"`
	UltimoPaso string    `json:"ultimo_paso"`
}

// estadoTCC es el archivo completo.
//
// `Identidad` es la clave de todo: mientras no cambie, lo que se sepa de los
// permisos sigue valiendo. Cuando cambia —o sea, cuando el binario se
// actualizó— todo lo anterior queda obsoleto de golpe, porque TCC acaba de
// re-anclar (o romper) las filas contra el cdhash nuevo. Ese cambio de
// identidad ES el disparador de T016c.
type estadoTCC struct {
	Identidad string                `json:"identidad"`
	Servicios map[string]intentoTCC `json:"servicios"`
}

func tccEstadoPath(dir string) string {
	return filepath.Join(dir, tccArchivoEstado)
}

func tccMarcaPath(dir string) string {
	return filepath.Join(dir, tccMarcaInstalacion)
}

// identidadBinario devuelve la huella del ejecutable vigente.
//
// Se usa versión + tamaño + mtime y NO el cdhash real, aunque el cdhash sea lo
// que TCC compara. Calcularlo obligaría a invocar `codesign` en cada arranque y
// a parsear su salida, y el dato sólo se necesita para responder "¿es el mismo
// binario que la última vez?" — pregunta que estos tres campos contestan igual
// de bien y sin depender de una herramienta externa. Un reemplazo del binario
// SIEMPRE mueve el mtime: el update escribe un temporal y hace rename.
func identidadBinario(version string, tam int64, mtime time.Time) string {
	return fmt.Sprintf("%s|%d|%d", version, tam, mtime.UTC().Unix())
}

// identidadDelEjecutable resuelve la identidad del binario que corre ahora.
func identidadDelEjecutable(version string) string {
	self, err := os.Executable()
	if err != nil {
		return version + "|desconocida"
	}
	info, err := os.Stat(self)
	if err != nil {
		return version + "|desconocida"
	}
	return identidadBinario(version, info.Size(), info.ModTime())
}

// alinearEstadoTCC descarta lo aprendido si el binario cambió.
//
// Devuelve el estado a usar y si hubo cambio de identidad (o sea: si esta ronda
// es la de T016c, la que sigue a una actualización).
func alinearEstadoTCC(st estadoTCC, ident string) (estadoTCC, bool) {
	if st.Servicios == nil {
		st.Servicios = map[string]intentoTCC{}
	}
	if st.Identidad == ident {
		return st, false
	}
	previo := st.Identidad != ""
	return estadoTCC{Identidad: ident, Servicios: map[string]intentoTCC{}}, previo
}

// debeCorrerRondaTCC decide si toca insistir con un servicio.
//
// El `forzar` viene del pedido manual (`-m tccpermisos`) y de la marca que deja
// el instalador: ahí hay una persona esperando, así que ni el tope de rondas ni
// la espera aplican.
func debeCorrerRondaTCC(reg intentoTCC, ahora time.Time, forzar bool) bool {
	if reg.Concedido {
		return false
	}
	if forzar {
		return true
	}
	if reg.Rondas >= tccMaxRondasPorIdentidad {
		return false
	}
	if !reg.Ultima.IsZero() && ahora.Sub(reg.Ultima) < tccEsperaEntreRondas {
		return false
	}
	return true
}

// nombreServicioTCC traduce el identificador a algo que una persona reconozca
//
// 🚫 SÓLO PARA TEXTO QUE LEE UNA PERSONA. `tccutil` espera la CLAVE del servicio
// (`ScreenCapture`, `Camera`); pasarle esto deja el reset muerto con un error
// que se lee como si el problema fuera otro. Ya pasó una vez: ver
// `resetearPermisoTCC`.
// en Ajustes del Sistema. Se usa en el aviso, no en el log.
func nombreServicioTCC(servicio string) string {
	switch servicio {
	case tccServicioCamara:
		return "Cámara"
	case tccServicioPantalla:
		return "Grabación de pantalla"
	}
	return servicio
}

// panelAjustesTCC es el enlace al panel EXACTO de Ajustes del Sistema. Abrir la
// sección correcta es la diferencia entre que alguien lo haga y que lo deje
// para después.
func panelAjustesTCC(servicio string) string {
	switch servicio {
	case tccServicioCamara:
		return "x-apple.systempreferences:com.apple.preference.security?Privacy_Camera"
	case tccServicioPantalla:
		return "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture"
	}
	return "x-apple.systempreferences:com.apple.preference.security?Privacy"
}

// mensajeAvisoTCC arma el texto que ve la persona cuando el diálogo ya no se
// puede mostrar y hace falta que intervenga.
//
// Dice QUÉ deja de funcionar y no sólo QUÉ falta, porque un mensaje que sólo
// pide un permiso se lee como una molestia y se cierra. Y no promete que
// tocando el interruptor de Ajustes se arregla: está medido que no re-ancla
// nada. Lo que sí funciona —que el sistema vuelva a preguntar— es lo que el
// agente ya intentó antes de llegar acá.
func mensajeAvisoTCC(servicios []string) (string, string) {
	nombres := make([]string, 0, len(servicios))
	for _, s := range servicios {
		nombres = append(nombres, nombreServicioTCC(s))
	}
	sort.Strings(nombres)

	titulo := "Observer RMM · permisos pendientes"
	cuerpo := fmt.Sprintf(
		"Tras actualizarse, el agente perdió estos permisos del sistema: %s.\n\n"+
			"Mientras falten, este equipo no puede aportar evidencia si se pierde o se lo roban.\n\n"+
			"Avise al área de soporte para reponerlos: hay que rehacerlos desde el equipo, "+
			"no se pueden conceder a distancia.",
		strings.Join(nombres, " y "))

	return titulo, cuerpo
}
