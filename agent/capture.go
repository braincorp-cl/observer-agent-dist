package agent

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"
	"time"
)

// Feature 030 · Fase 1 · contrato común de la captura de pantalla (ADR-025).
//
// Cada SO implementa `capturarPantalla` con su etiqueta de build
// (capture_windows.go / capture_linux.go / capture_darwin.go). Acá vive lo que
// NO depende del sistema: los motivos, el nombre del archivo y —lo más
// importante— la verificación de que lo capturado no sea una pantalla en negro.
//
// EL PRINCIPIO DE ESTE ARCHIVO: una captura que no se pudo hacer se REPORTA,
// nunca se calla. La 028 llegó al terreno con ocho "ok falso" —la alarma que
// respondía `ok` sin sonar, el diálogo que se dibujaba en la pantalla de
// login— y la lección quedó escrita en la 025: sin permiso, la API de macOS no
// falla, devuelve negro **en silencio**. Un caso de robo alimentado con
// capturas negras que nadie marcó como fallidas es peor que un caso vacío,
// porque el operador cree que está mirando la pantalla del ladrón.
//
// Por eso el resultado de una captura NUNCA es sólo (archivo, error): es una
// pieza con un motivo legible cuando no hay archivo, y el motivo viaja al
// servidor y se ve en la línea de tiempo del caso.

// Motivos por los que un ciclo no trae imagen. Son CÓDIGOS, no texto: viajan al
// servidor y los traduce el catálogo i18n de la consola, igual que hacen las
// respuestas de la 028 (`endpoint_response:<código>`). Un agente en un equipo
// con locale ES no puede mandar una frase distinta que uno en EN.
const (
	// No hay nadie con sesión gráfica abierta: el equipo está en la pantalla de
	// inicio de sesión o nadie entró desde que arrancó. No es un fallo — es un
	// dato del caso, y además dice que el ladrón todavía no abrió sesión.
	captureNoSession = "sin_sesion"
	// Sesión Wayland en la que NO EXISTE ninguna vía de captura: ni la nativa
	// del compositor ni el portal (falta `gdbus`). Ver capture_linux_wayland.go.
	captureWayland = "wayland_no_soportado"
	// Sesión Wayland en la que la captura SÍ es posible pero falta el
	// enrolamiento: el compositor no ofrece vía silenciosa y el portal nunca fue
	// autorizado. Es distinto del anterior porque es REPARABLE, y quien opera el
	// caso necesita saber que a ese equipo le falta un paso, no que no puede.
	captureWaylandSinAutorizacion = "wayland_sin_autorizacion"
	// No hay ninguna herramienta de captura instalada (Linux).
	captureNoTool = "sin_herramienta"
	// El SO negó el permiso — macOS sin "Grabación de pantalla" concedida.
	captureDenied = "permiso_denegado"
	// Se capturó, pero la imagen salió completamente negra y no se pudo saber por
	// qué. Ver `imagenEnNegro`.
	captureBlank = "pantalla_en_negro"
	// La imagen salió negra **porque la pantalla estaba apagada** (protector de
	// pantalla o ahorro de energía). Es un motivo distinto y mucho más útil: no
	// es que la captura fallara, es que no había nada que capturar — y para el
	// caso significa que nadie está usando el equipo en este momento.
	captureScreenOff = "pantalla_apagada"
	// La herramienta corrió y falló, o el archivo salió vacío/ilegible.
	captureFailed = "fallo_captura"
	// Plataforma sin implementación de captura.
	captureUnsupported = "so_no_soportado"
)

// ScreenShot es el resultado de intentar una captura. O trae `Path`, o trae
// `Reason`: nunca las dos ni ninguna.
type ScreenShot struct {
	Path   string
	Reason string
}

// OK indica si hay un archivo que subir.
func (s ScreenShot) OK() bool { return s.Path != "" }

// screenshotFilename arma el nombre del archivo con el reloj del EQUIPO.
//
// El reloj del equipo y no el del servidor: entre los dos puede haber horas si
// el equipo estuvo sin red, y para un caso que puede terminar en una denuncia
// esa diferencia importa (la fila guarda los dos, `captured_at` y `created`).
//
// Formato ASCII plano, sin acentos ni dos puntos: el nombre viaja en un
// multipart, se guarda en disco en tres SO distintos y termina en una URL. Los
// dos puntos del formato ISO son ilegales en nombres de archivo de Windows.
func screenshotFilename(t time.Time) string {
	return fmt.Sprintf("pantalla-%s.png", t.UTC().Format("20060102-150405"))
}

// ── Contrato del sub-proceso de captura ──────────────────────────────────────
//
// En Windows la captura NO la hace el servicio: se re-ejecuta a sí mismo dentro
// de la sesión del usuario (ver capture_windows.go) y el hijo contesta por
// stdout con una sola línea. El nombre del modo, los prefijos y el parseo viven
// acá, sin etiqueta de plataforma, POR LA MISMA RAZÓN que `pickUserSession`: la
// CI corre `go test` en Linux y macOS, así que una prueba dentro de un archivo
// `//go:build windows` se compilaría y no se ejecutaría nunca. Cobertura de
// mentira sobre la parte que puede fallar en silencio.

const (
	// Nombre del modo con el que el agente se re-ejecuta.
	modoCapturaPantalla = "screencapture"

	// Prefijos de la única línea que imprime el hijo. Mismo contrato que la
	// sonda de geolocalización de la 025: una línea, parseable, sin i18n.
	prefijoCapturaOK    = "OK "
	prefijoCapturaFallo = "FAIL "
)

// parsearSalidaCaptura interpreta la línea del hijo.
//
// Se recorre línea por línea y no se mira sólo la primera porque el hijo puede
// haber escrito antes algo que no controlamos —un aviso del runtime, una línea
// de log que se escapó—. Una salida vacía o incomprensible es `fallo_captura` y
// nunca un éxito: leer el vacío como OK daría una fila de evidencia apuntando a
// un archivo que no existe, que es peor que no tener la fila.
func parsearSalidaCaptura(salida string) ScreenShot {
	for _, linea := range strings.Split(salida, "\n") {
		linea = strings.TrimSpace(linea)
		switch {
		case strings.HasPrefix(linea, prefijoCapturaOK):
			if ruta := strings.TrimSpace(strings.TrimPrefix(linea, prefijoCapturaOK)); ruta != "" {
				return ScreenShot{Path: ruta}
			}
		case strings.HasPrefix(linea, prefijoCapturaFallo):
			if motivo := strings.TrimSpace(strings.TrimPrefix(linea, prefijoCapturaFallo)); motivo != "" {
				return ScreenShot{Reason: motivo}
			}
		}
	}

	return ScreenShot{Reason: captureFailed}
}

// CaptureScreen intenta una captura y devuelve el resultado ya verificado.
//
// No recibe directorio: lo elige cada plataforma, porque no es una preferencia
// sino una restricción. En los tres SO quien escribe el archivo corre CON LOS
// PRIVILEGIOS DEL USUARIO del escritorio —la herramienta del sistema en Linux y
// macOS, el propio agente re-ejecutado en Windows—, así que el archivo tiene
// que caer donde ese usuario pueda escribir: `/opt/observeragent` es de root
// con 0755 y ahí fallarían todas las capturas.
//
// Si la imagen resulta estar en negro, el archivo se borra acá mismo y se
// devuelve el motivo: subir una imagen negra como si fuera evidencia es
// exactamente la mentira que esta feature no puede permitirse.
func (a *Agent) CaptureScreen() ScreenShot {
	shot := a.capturarPantalla(screenshotFilename(time.Now()))
	if !shot.OK() {
		return shot
	}

	// El archivo nace en un directorio del usuario (ver arriba) y con los
	// permisos por omisión de la herramienta que lo escribió, que en la mayoría
	// son 0644. Se cierra a 0600 en cuanto existe: mientras espera la subida es
	// una foto de la pantalla de una persona, y en un equipo con más de una
	// cuenta no tiene por qué poder leerla nadie más. No se aborta si falla —el
	// archivo ya existe y borrarlo por esto sería perder la evidencia.
	//
	// En Windows esto sólo toca el bit de sólo-lectura y no las ACL, así que
	// **no cierra nada**: ahí lo que acota el alcance es que el archivo vive en
	// el temporal del propio usuario. Se deja igual porque es correcto en los
	// otros dos y porque el archivo dura segundos.
	if err := os.Chmod(shot.Path, 0600); err != nil {
		a.Logger.Debugln("CaptureScreen: no se pudo restringir el permiso del archivo:", err)
	}

	data, err := os.ReadFile(shot.Path)
	if err != nil || len(data) == 0 {
		a.Logger.Debugln("CaptureScreen: el archivo capturado no se pudo leer:", err)
		_ = os.Remove(shot.Path)
		return ScreenShot{Reason: captureFailed}
	}

	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		// No es PNG legible. Puede ser que la herramienta escribiera un JPEG, o
		// que muriera a mitad. No se descarta la captura por eso —el formato lo
		// decide cada SO— pero sí se salta la verificación de negro, y eso se
		// deja dicho en el log para que no parezca que se verificó.
		a.Logger.Debugln("CaptureScreen: la imagen no es un PNG legible, no se puede verificar si está en negro:", err)
		return shot
	}

	if imagenEnNegro(img) {
		// Antes de reportar, se pregunta POR QUÉ está negra. Medido en terreno el
		// 2026-08-12: en un equipo real, 25 s después de arrancar la sesión la
		// captura salía perfectamente legible y unos minutos más tarde salía
		// completamente negra — porque la pantalla se apaga sola. Sin esta
		// distinción, la línea de tiempo de un equipo robado que nadie está
		// usando queda llena de "la captura salió en negro", que se lee como una
		// avería del producto cuando en realidad es información del caso.
		//
		// Lo que NO se hace: encender la pantalla para capturar. Sería alterar la
		// escena y, en un equipo robado, avisarle a quien lo tiene que alguien
		// está mirando.
		motivo := a.motivoDePantallaNegra()
		a.Logger.Infoln("CaptureScreen: la imagen salió completamente en negro, se descarta y se reporta:", motivo)
		_ = os.Remove(shot.Path)
		return ScreenShot{Reason: motivo}
	}

	return shot
}

// negroMax es el umbral por canal bajo el cual un píxel se considera negro.
//
// No es 0: un escritorio oscuro real tiene ruido de compresión y bordes de
// ventana que no llegan a cero, mientras que una captura fallida da ceros
// exactos. Se deja un margen chico para no llamar "negro" a un fondo oscuro
// legítimo, que sería el error caro: descartaría evidencia buena.
const negroMax = 8

// muestrasPorLado es el lado de la grilla de muestreo. 16×16 = 256 píxeles
// mirados, cueste lo que cueste la pantalla: 1366×768 o 8K dan el mismo trabajo.
const muestrasPorLado = 16

// imagenEnNegro decide si una captura es la pantalla negra que devuelven las
// APIs cuando no tienen permiso o no hay escritorio donde dibujar.
//
// POR QUÉ EXISTE: es el "ok falso" característico de esta feature. En macOS sin
// "Grabación de pantalla" concedida, `screencapture` no falla: entrega una
// imagen. En Windows, un BitBlt hecho desde la sesión 0 tampoco falla: entrega
// negro. En los dos casos el código de salida es 0 y el archivo pesa lo suyo —
// el `exit 0` no prueba el efecto. Lo único que distingue el éxito del fracaso
// es MIRAR los píxeles.
//
// El método es el mismo que se usó a mano el 2026-08-02 para probar que el KVM
// de MeshCentral daba negro en una sesión Wayland: reducir la imagen a una
// grilla de muestras y contar cuántas son cero. Acá se muestrea en vez de
// recorrer todo porque una captura 4K son 8 millones de píxeles y esto corre en
// el equipo del usuario, cada N minutos.
//
// Se devuelve `false` ante cualquier duda (imagen minúscula, geometría rara):
// ante la duda se sube la evidencia. Descartar una captura buena es peor que
// subir una negra que alguien va a ver como negra de todos modos.
func imagenEnNegro(img image.Image) bool {
	if img == nil {
		return false
	}

	b := img.Bounds()
	ancho, alto := b.Dx(), b.Dy()
	if ancho < muestrasPorLado || alto < muestrasPorLado {
		return false
	}

	for i := 0; i < muestrasPorLado; i++ {
		for j := 0; j < muestrasPorLado; j++ {
			x := b.Min.X + (ancho*i)/muestrasPorLado
			y := b.Min.Y + (alto*j)/muestrasPorLado

			// RGBA() devuelve canales de 16 bits pre-multiplicados; se bajan a 8
			// para comparar contra el umbral en la escala en que se piensa.
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 > negroMax || g>>8 > negroMax || bl>>8 > negroMax {
				return false
			}
		}
	}

	return true
}

// LOS SCRIPTS DE APPLESCRIPT DE LA PANTALLA SE FUERON EN T016.
//
// Eran `scriptPreflightPantalla` y `scriptPedirPantalla`, dos `osascript`
// cargando CoreGraphics, y estaban acá —sin etiqueta de plataforma— para que la
// CI pudiera vigilarlos. Los reemplazó una llamada nativa desde el ayudante
// (`observer_pantalla_preflight` / `observer_pantalla_pedir`, en
// captura_darwin.m), y no por gusto: desde T016 el permiso lo tiene el BUNDLE
// del ayudante, y un `osascript` lanzado por el servicio consultaba y pedía a
// nombre del cliente equivocado.
//
// 🪤 Lo que NO hay que perder de aquellos scripts, porque volverá a morder a
// quien escriba AppleScript en este repo: `st`, `nd`, `rd` y `th` son PALABRAS
// RESERVADAS —los ordinales `1st`, `2nd`…—, así que `set st to (…)` no compila
// nunca y el script falla siempre, en silencio y devolviendo lo mismo que una
// denegación legítima. Costó una medición de terreno entera descubrirlo. Queda
// AppleScript vivo en `volume_darwin.go` y en el aviso de la ronda.
