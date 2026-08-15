package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Feature 030 · Fase 2 · T016 — el contrato con el ayudante de captura de macOS.
//
// POR QUÉ ESTE ARCHIVO NO LLEVA ETIQUETA DE PLATAFORMA. Todo lo de acá es macOS
// puro, pero vive en un archivo que Linux también compila, por la misma razón
// que `scriptEstadoTCCCamara` en webcam.go: la CI corre `go test` en Linux, así
// que una prueba encerrada en un archivo `//go:build darwin` se compila y no se
// ejecuta nunca — cobertura de mentira sobre la parte que puede fallar en
// silencio. El Info.plist, el plist del LaunchAgent, el nonce y el formato de la
// respuesta son exactamente eso: texto que nadie mira hasta que un equipo real
// no da la foto.
//
// ────────────────────────────────────────────────────────────────────────────
// LOS DOS MUROS QUE ESTE AYUDANTE RESUELVE
// ────────────────────────────────────────────────────────────────────────────
//
// **1 · El contexto Aqua.** Con el permiso de Cámara concedido
// (`AVAuthorizationStatus` = 3), un capturador lanzado por el LaunchDaemon vía
// `launchctl asuser` devuelve **0 cuadros en 25 s y sin ningún error**: sesión
// corriendo, un input, un output, formato 1920×1080, dispositivo libre. El mismo
// binario cargado como LaunchAgent en `gui/<uid>` fotografía con el rostro
// legible. `asuser` cambia de dominio de sesión, no de contexto gráfico.
//
// **2 · Una fila de TCC rota no se puede reparar… salvo que el cliente sea un
// `.app`.** El `csreq` de un binario firmado ad-hoc es un literal de cdhash, así
// que **cada actualización lo invalida**. Y una fila que quedó en «permitido»
// con el cdhash viejo es la peor de todas: `CGRequestScreenCaptureAccess` **no
// pregunta** —el sistema sólo pregunta cuando el estado es «no determinado»— y
// `tccd` deniega igual al validar. Ajustes lo muestra encendido y la captura
// falla. SIP impide borrar la fila a mano.
//
// 🔑 Lo único que devuelve una fila a «no determinado» es `tccutil reset`, y
// medido el 2026-08-15: `tccutil` resuelve el cliente **por LaunchServices y
// sólo acepta un `.app`** —un `.bundle` de Apple falla con el mismo `rc=64` que
// un ejecutable suelto—, pero **sí alcanza la TCC.db del sistema con SIP
// activo**, porque el trabajo lo hace `tccd` y no un `sqlite` directo.
//
// ⇒ Por eso el ayudante es un **bundle `.app` con identificador propio**, y no
// el binario del servicio. Deja de compartir la fila del servicio —hay que
// conceder una vez para él— y a cambio su permiso pasa a ser **reparable**: el
// paso 2 de la escalada puede resetearlo y volver a preguntar. Es la diferencia
// entre un equipo que se rompe para siempre en la próxima actualización y uno
// que se arregla solo pidiendo un clic.
//
// 🪤 Y hay que **registrarlo en LaunchServices** (`lsregister -f`): recién
// instalado, `tccutil` responde `No such bundle identifier` aunque el `.app`
// exista y esté bien firmado. Medido: antes de registrar `rc=64`, después
// `Successfully reset`.

const (
	// capturaAquaID es el identificador del bundle, el Label del LaunchAgent y
	// el nombre con el que `tccutil` puede alcanzarlo. Los tres tienen que ser
	// el mismo texto: es la identidad del ayudante para el sistema.
	capturaAquaID = "cl.observer.agent.captura"

	// capturaAquaAppDir es el bundle. Cuelga de `nixAgentDir`, que es una ruta
	// ABSOLUTA constante, y NO de `a.ProgramDir`: fuera de Windows
	// `ResolveProgramDir()` se arma con `%ProgramFiles%` y deja una ruta
	// relativa, que con el cwd del servicio en `/` apuntaría a `/ObserverAgent/`.
	// Ese bug ya se pagó una vez (ver `destinoFotoWebcam`).
	capturaAquaAppDir = nixAgentDir + "/ObserverCaptura.app"
	capturaAquaAppExe = capturaAquaAppDir + "/Contents/MacOS/ObserverCaptura"
	capturaAquaAppPl  = capturaAquaAppDir + "/Contents/Info.plist"

	// capturaAquaPlist es el LaunchAgent. `/Library/LaunchAgents` y no
	// `~/Library/LaunchAgents` porque launchd lo carga solo en la sesión de
	// CUALQUIER usuario que entre, sin que el agente tenga que enterarse de los
	// inicios de sesión ni escribir en la carpeta personal de nadie.
	//
	// 🪤 ESTA RUTA Y ESTE IDENTIFICADOR ESTÁN DUPLICADOS EN
	// `core/mac_uninstall.sh`, del lado del servidor, que es quien corre la
	// desinstalación pedida desde la consola. No hay forma de compartir la
	// constante entre un binario Go y un script que se sirve por HTTP: quien
	// cambie el nombre acá tiene que ir a cambiarlo allá, y si no lo hace el
	// síntoma es un LaunchAgent huérfano que sobrevive a la desinstalación.
	capturaAquaPlist = "/Library/LaunchAgents/" + capturaAquaID + ".plist"

	// capturaAquaDir es el buzón entre el servicio (root) y el ayudante (el
	// usuario de consola).
	//
	// No cuelga de `/tmp` a propósito: `/tmp` lo puede escribir cualquiera, y acá
	// viaja la ruta de un archivo que el servicio va a subir como evidencia. Un
	// directorio de root con 0755 hace que nadie más pueda dejar un pedido ni una
	// respuesta falsa dentro.
	capturaAquaDir = nixAgentDir + "/captura"

	capturaAquaPedidoNombre    = "pedido.json"
	capturaAquaRespuestaNombre = "respuesta.json"

	// modoCapturaAqua es el modo que corre el ayudante.
	//
	// Es DISTINTO de `-m webcamcapture` y `-m screencapture`, y la diferencia no
	// es cosmética: esos dos son el instrumento del operador y pasan por todo el
	// camino de producción —o sea que en macOS despiertan a este ayudante—;
	// `capturaaqua` es el otro extremo de ese camino y el único que toca la
	// cámara o la pantalla. Con el mismo nombre, el ayudante se despertaría a sí
	// mismo en un bucle infinito.
	modoCapturaAqua = "capturaaqua"
)

// Tipos de encargo que entiende el ayudante.
const (
	capturaTipoWebcam   = "webcam"
	capturaTipoPantalla = "pantalla"
	// capturaTipoEstado consulta los permisos SIN pedir nada. Existe separado del
	// pedido de permiso porque la diferencia entre consultar y pedir es la
	// diferencia entre mirar y mostrarle un diálogo a quien tiene el equipo — que
	// en un caso de robo es lo que delata al ladrón.
	capturaTipoEstado = "estado"
	// capturaTipoPermiso dispara el diálogo del sistema. Sólo lo usa la ronda de
	// T016b/T016c, con sus guardias.
	capturaTipoPermiso = "permiso"
)

// capturaAquaPedido es lo que el servicio le encarga al ayudante.
type capturaAquaPedido struct {
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
	// Tipo es qué se le pide: `webcam`, `pantalla`, `estado` o `permiso`.
	Tipo string `json:"tipo"`
	// Destino es la ruta que el servicio ya creó, con 0600 y a nombre del usuario
	// de consola. El ayudante escribe ahí y no elige nada: así el archivo nace
	// cerrado y no hay un instante en que la foto de una cara esté legible para
	// el resto del equipo. Vacío en `estado` y `permiso`.
	Destino string `json:"destino,omitempty"`
	// Descarte son los cuadros de calentamiento (ver `cuadrosDeCalentamiento`).
	Descarte int `json:"descarte,omitempty"`
	// Servicio es el permiso TCC en juego para `estado` y `permiso`.
	Servicio string `json:"servicio,omitempty"`
}

// capturaAquaRespuesta es lo que contesta el ayudante.
type capturaAquaRespuesta struct {
	ID string `json:"id"`
	// Motivo vacío significa éxito. Se usa el motivo y no un booleano porque los
	// códigos son los mismos de `webcam.go` y `capture.go`, y viajan hasta la
	// consola.
	Motivo string `json:"motivo"`
	// Estado es el `AVAuthorizationStatus` (0 no determinado, 1 restringido,
	// 2 denegado, 3 concedido) para los pedidos de `estado` y `permiso`.
	Estado int `json:"estado,omitempty"`
	// Detalle es el texto crudo del error, para el log del SERVICIO. No viaja al
	// servidor y no se traduce.
	//
	// Vuelve por el buzón en vez de quedarse en el log del ayudante por una razón
	// práctica: el ayudante corre sin privilegios y su plist no redirige la
	// salida a propósito (ver más abajo), así que lo que no vuelva por acá se
	// pierde. Y es justo lo que hace falta el día que un Mac devuelva
	// `fallo_captura` y haya que saber si fue el formato, la escritura o el
	// dispositivo.
	Detalle string `json:"detalle,omitempty"`
}

func capturaAquaPedidoPath() string { return filepath.Join(capturaAquaDir, capturaAquaPedidoNombre) }
func capturaAquaRespuestaPath() string {
	return filepath.Join(capturaAquaDir, capturaAquaRespuestaNombre)
}

// capturaAquaInfoPlist arma el Info.plist del bundle.
//
// `CFBundleIdentifier` es LA pieza: es lo que convierte al ayudante en un
// cliente de TCC con nombre propio y, por lo tanto, en algo que `tccutil` puede
// resolver y resetear. Sin bundle no hay identificador, y sin identificador el
// permiso roto es definitivo.
//
// `NSCameraUsageDescription` no es opcional: macOS **exige** la clave para poder
// pedir la cámara y, sin ella, mata al proceso que lo pide sin dejar rastro
// útil. El texto lo lee la persona en el diálogo, así que dice para qué es de
// verdad —la evidencia de un equipo perdido o robado, gobernada por ADR-025— y
// no una fórmula genérica. La pantalla no lleva clave equivalente: Apple no
// definió ninguna para `ScreenCapture`.
//
// `LSUIElement` mantiene al ayudante fuera del Dock y del conmutador de
// aplicaciones. Un icono apareciendo y desapareciendo en cada ciclo sería,
// además de feo, un aviso para quien tenga el equipo.
func capturaAquaInfoPlist() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>%s</string>
	<key>CFBundleName</key>
	<string>Observer RMM</string>
	<key>CFBundleExecutable</key>
	<string>ObserverCaptura</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>LSUIElement</key>
	<true/>
	<key>NSCameraUsageDescription</key>
	<string>Observer RMM toma una foto con la cámara solo si el equipo se declara perdido o robado, para identificar a quien lo tenga.</string>
</dict>
</plist>
`, capturaAquaID)
}

// capturaAquaPlistContenido arma el plist del LaunchAgent.
//
// CUATRO DECISIONES QUE NO SE VEN Y QUE IMPORTAN:
//
//  1. El programa es el ejecutable DENTRO DEL BUNDLE. Lanzar el binario del
//     servicio dejaría al ayudante sin identidad de bundle y con él se perdería
//     todo lo que este rediseño compra.
//
//  2. `RunAtLoad` y `KeepAlive` en false. El ayudante NO es un residente: es un
//     trabajo registrado y quieto que sólo corre cuando el servicio lo despierta
//     con `launchctl kickstart`. Un proceso permanente con acceso a la cámara
//     colgado de la sesión de cada usuario sería, además de gratis en consumo,
//     exactamente lo que ADR-025 no quiere prometer.
//
//  3. `LimitLoadToSessionType = Aqua`. Es redundante con el valor por omisión de
//     `/Library/LaunchAgents`, y se escribe igual porque Aqua es LA razón de que
//     este ayudante exista. Que quede dicho en el propio artefacto y no sólo en
//     un comentario.
//
//  4. `-logto stdout` y no el archivo. El ayudante corre SIN PRIVILEGIOS, y el
//     destino por omisión del log es `/var/log/observeragent.log`, que es de
//     root: abrirlo falla y cada línea de log se convierte en un error de
//     escritura. No es una preferencia de verbosidad.
//
// Y UNA QUE SE DESCARTÓ: no hay `StandardOutPath` ni `StandardErrorPath`, que
// habrían sido cómodos para depurar el terreno. launchd abre esos archivos con
// el usuario del trabajo, así que en un Mac con dos cuentas el archivo queda a
// nombre de quien entró primero y el segundo usuario se topa con un trabajo que
// no puede abrir su propia salida. Cambiar la robustez de la captura por
// comodidad de depuración es un mal negocio: el ayudante contesta por el buzón,
// con los mismos códigos de motivo que llegan a la consola, y eso alcanza.
func capturaAquaPlistContenido() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple Computer//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
	<dict>
		<key>Label</key>
		<string>%s</string>

		<key>ServiceDescription</key>
		<string>Observer RMM - captura en la sesion grafica</string>

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
`, capturaAquaID, capturaAquaAppExe, modoCapturaAqua)
}

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
func parsearRespuestaAqua(datos []byte, id string) (r capturaAquaRespuesta, valida bool) {
	if err := json.Unmarshal(datos, &r); err != nil {
		return capturaAquaRespuesta{}, false
	}
	if r.ID == "" || r.ID != id {
		return capturaAquaRespuesta{}, false
	}
	r.Motivo = strings.TrimSpace(r.Motivo)
	return r, true
}
