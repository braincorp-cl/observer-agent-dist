package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Feature 030 · Fase 2 · T016 — el contrato entre el servicio y el ayudante Aqua.
//
// POR QUÉ EXISTE ESTE ARCHIVO SIN ETIQUETA DE PLATAFORMA. Todo lo de acá es
// macOS puro, pero vive en un archivo que Linux también compila, por la misma
// razón que `scriptEstadoTCCCamara` en webcam.go: la CI corre `go test` en
// Linux, así que una prueba encerrada en un archivo `//go:build darwin` se
// compila y no se ejecuta nunca — cobertura de mentira sobre la parte que puede
// fallar en silencio. El plist, el nonce y el formato de la respuesta son
// exactamente eso: texto que nadie mira hasta que un equipo real no da la foto.
//
// EL PROBLEMA QUE ESTE MECANISMO RESUELVE, en una línea: en macOS el permiso de
// Cámara no alcanza. Medido en terreno el 2026-08-13 con el permiso concedido
// (`AVAuthorizationStatus` = 3), un capturador lanzado por el LaunchDaemon vía
// `launchctl asuser` devuelve **0 cuadros en 25 s y sin ningún error**:
// `sesion_corriendo=1`, `inputs=1 outputs=1`, formato 1920×1080, dispositivo
// libre. El MISMO binario, en la MISMA ruta, cargado como LaunchAgent en
// `gui/<uid>`, fotografía con el rostro legible. El muro no es el permiso: es el
// contexto de lanzamiento. `asuser` cambia de dominio de sesión, no de contexto
// gráfico, y la cámara exige Aqua.
//
// 🔑 Y EL AYUDANTE CORRE EL MISMO BINARIO EN LA MISMA RUTA, no una copia. TCC
// indexa a un ejecutable suelto —el agente no es un bundle— POR SU RUTA, así que
// el LaunchAgent comparte la fila de `access` del servicio y hereda el permiso
// que concedió la ronda de T016b. Un ayudante aparte tendría su propio cdhash,
// su propia fila, y habría que conceder dos veces. Verificado el 2026-08-15:
// la fila `kTCCServiceCamera` del cliente `/opt/observeragent/observeragent`
// quedó en `auth_value=2, auth_reason=2` anclada al cdhash vigente.

const (
	// webcamAquaLabel es el Label del LaunchAgent. Cuelga del identificador del
	// agente para que se lea de un vistazo de quién es en `launchctl list`.
	webcamAquaLabel = "cl.observer.agent.camara"

	// webcamAquaPlist es dónde vive. `/Library/LaunchAgents` y no
	// `~/Library/LaunchAgents` porque launchd lo carga solo en la sesión de
	// CUALQUIER usuario que entre, sin que el agente tenga que enterarse de los
	// inicios de sesión ni escribir en la carpeta personal de nadie.
	//
	// 🪤 ESTA RUTA Y ESTE LABEL ESTÁN DUPLICADOS EN `core/mac_uninstall.sh`, del
	// lado del servidor, que es el que corre la desinstalación pedida desde la
	// consola. No hay forma de compartir la constante entre un binario Go y un
	// script que se sirve por HTTP, así que quien cambie el nombre acá tiene que
	// ir a cambiarlo allá — y si no lo hace, el síntoma es un LaunchAgent
	// huérfano que sobrevive a la desinstalación.
	webcamAquaPlist = "/Library/LaunchAgents/" + webcamAquaLabel + ".plist"

	// webcamAquaDir es el buzón entre el servicio (root) y el ayudante (el
	// usuario de consola).
	//
	// Cuelga de `nixAgentDir`, que es una ruta ABSOLUTA constante, y NO de
	// `a.ProgramDir`: fuera de Windows `ResolveProgramDir()` se arma con
	// `%ProgramFiles%` y deja una ruta relativa, que con el cwd del servicio en
	// `/` mandaría la foto a `/ObserverAgent/`. Ese bug ya se pagó una vez
	// (ver `destinoFotoWebcam`).
	//
	// Y no cuelga de `/tmp` a propósito: `/tmp` lo puede escribir cualquiera, y
	// acá viaja la ruta de un archivo que el servicio va a subir como evidencia.
	// Un directorio de root con 0755 hace que nadie más pueda dejar un pedido ni
	// una respuesta falsa dentro.
	webcamAquaDir = nixAgentDir + "/camara"

	// webcamAquaPedidoNombre lo escribe el servicio; webcamAquaRespuestaNombre
	// lo escribe el ayudante. Dos archivos y no uno para que ninguno de los dos
	// tenga que esperar a que el otro suelte nada.
	webcamAquaPedidoNombre    = "pedido.json"
	webcamAquaRespuestaNombre = "respuesta.json"
)

// webcamAquaPedido es lo que el servicio le encarga al ayudante.
type webcamAquaPedido struct {
	// ID es un nonce por pedido. El ayudante lo devuelve tal cual y el servicio
	// descarta cualquier respuesta que no lo traiga.
	//
	// No es ceremonia: `launchctl kickstart` vuelve EN CUANTO launchd acepta el
	// encargo, no cuando el trabajo termina. Sin el nonce, una respuesta que
	// quedó de un ciclo anterior —o la de un `kickstart` que se solapó— se leería
	// como la de este, y el servicio subiría la foto de hace cinco minutos con la
	// marca de tiempo de ahora. En un caso que puede terminar en una denuncia,
	// esa es la peor clase de error posible: no falla, miente.
	ID string `json:"id"`
	// Destino es la ruta que el servicio ya creó, con 0600 y a nombre del usuario
	// de consola. El ayudante escribe ahí y no elige nada: así el archivo nace
	// cerrado y no hay un instante en que la foto de una cara esté legible para
	// el resto del equipo.
	Destino string `json:"destino"`
	// Descarte son los cuadros de calentamiento (ver `cuadrosDeCalentamiento`).
	Descarte int `json:"descarte"`
}

// webcamAquaRespuesta es lo que contesta el ayudante.
type webcamAquaRespuesta struct {
	ID string `json:"id"`
	// Motivo vacío significa éxito. Se usa el motivo y no un booleano porque los
	// códigos son los mismos de `webcam.go` y viajan hasta la consola.
	Motivo string `json:"motivo"`
	// Detalle es el texto crudo del error de AVFoundation, para el log del
	// SERVICIO. No viaja al servidor y no se traduce.
	//
	// Viene por el buzón en vez de quedarse en el log del ayudante por una razón
	// práctica: el ayudante no tiene dónde escribir —corre sin privilegios y su
	// plist no redirige la salida a propósito (ver más abajo)—, así que lo que no
	// vuelva por acá se pierde. Y es justo lo que hace falta el día que un Mac
	// devuelva `fallo_captura` y haya que saber si fue el formato, la escritura o
	// el dispositivo.
	Detalle string `json:"detalle,omitempty"`
}

func webcamAquaPedidoPath() string    { return filepath.Join(webcamAquaDir, webcamAquaPedidoNombre) }
func webcamAquaRespuestaPath() string { return filepath.Join(webcamAquaDir, webcamAquaRespuestaNombre) }

// webcamAquaPlistContenido arma el plist del LaunchAgent.
//
// TRES DECISIONES QUE NO SE VEN Y QUE IMPORTAN:
//
//  1. `RunAtLoad` y `KeepAlive` en false. El ayudante NO es un residente: es un
//     trabajo registrado y quieto que sólo corre cuando el servicio lo despierta
//     con `launchctl kickstart`. Un proceso permanente con acceso a la cámara
//     colgado de la sesión de cada usuario sería, además de gratis en consumo,
//     exactamente lo que ADR-025 no quiere prometer.
//
//  2. `LimitLoadToSessionType = Aqua`. Es redundante con el valor por omisión de
//     `/Library/LaunchAgents`, y se escribe igual porque Aqua es LA razón de que
//     este archivo exista. Que quede dicho en el propio artefacto y no sólo en un
//     comentario.
//
//  3. El programa es `%s -m webcamaqua`, con la ruta del binario INSTALADO. No
//     una copia ni un helper: ver el encabezado. Si esta ruta cambia, el permiso
//     de TCC se queda atrás y la foto vuelve a fallar en silencio.
//
//  4. `-logto stdout` y no el archivo. El ayudante corre SIN PRIVILEGIOS, y el
//     destino por omisión del log es `/var/log/observeragent.log`, que es de
//     root: abrirlo falla y cada línea de log se convierte en un error de
//     escritura. No es una preferencia de verbosidad, es evitar que el ayudante
//     pelee con un archivo que no le pertenece.
//
// Y UNA QUE SE DESCARTÓ: no hay `StandardOutPath` ni `StandardErrorPath`, que
// habrían sido cómodos para depurar el terreno. launchd abre esos archivos con
// el usuario del trabajo, así que en un Mac con dos cuentas el archivo queda a
// nombre de quien entró primero y el segundo usuario se topa con un trabajo que
// no puede abrir su propia salida. Cambiar la robustez de la captura por
// comodidad de depuración es un mal negocio: el ayudante contesta por el buzón,
// con los mismos códigos de motivo que llegan a la consola, y eso alcanza.
func webcamAquaPlistContenido(binario string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple Computer//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
	<dict>
		<key>Label</key>
		<string>%s</string>

		<key>ServiceDescription</key>
		<string>Observer RMM - captura de camara en la sesion grafica</string>

		<key>ProgramArguments</key>
		<array>
			<string>%s</string>
			<string>-logto</string>
			<string>stdout</string>
			<string>-m</string>
			<string>%s</string>
		</array>

		<key>LimitLoadToSessionType</key>
		<string>Aqua</string>

		<key>RunAtLoad</key>
		<false/>

		<key>KeepAlive</key>
		<false/>
	</dict>
</plist>
`, webcamAquaLabel, binario, modoWebcamAqua)
}

// modoWebcamAqua es el nombre del modo que corre el ayudante.
//
// Es DISTINTO de `-m webcamcapture` y la diferencia no es cosmética.
// `webcamcapture` es el instrumento del operador y pasa por todo el camino de
// producción —o sea que en macOS despierta a este ayudante—; `webcamaqua` es el
// otro extremo de ese camino y es el único que toca la cámara. Si fueran el
// mismo nombre, el ayudante se despertaría a sí mismo en un bucle infinito.
const modoWebcamAqua = "webcamaqua"

// parsearRespuestaAqua interpreta lo que dejó el ayudante.
//
// Devuelve `valida=false` para todo lo que NO es una respuesta utilizable de
// este pedido: JSON roto, nonce ajeno, archivo a medio escribir. El que espera
// tiene que seguir esperando en esos tres casos, y son tres casos reales — el
// servicio sondea el archivo mientras otro proceso lo está escribiendo.
//
// Con `valida=true`, el motivo vacío es el éxito y cualquier otro es el fallo
// que reporta el ayudante. Lo que NUNCA puede pasar es que basura se lea como
// OK: eso dejaría una fila de evidencia apuntando a un archivo vacío, que es
// peor que no tener la fila.
func parsearRespuestaAqua(datos []byte, id string) (r webcamAquaRespuesta, valida bool) {
	if err := json.Unmarshal(datos, &r); err != nil {
		return webcamAquaRespuesta{}, false
	}
	if r.ID == "" || r.ID != id {
		return webcamAquaRespuesta{}, false
	}
	r.Motivo = strings.TrimSpace(r.Motivo)
	return r, true
}
