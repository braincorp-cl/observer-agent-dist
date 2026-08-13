package agent

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"time"
)

// Feature 030 · Fase 2 · contrato común de la foto de webcam (ADR-025).
//
// Mismo molde que la captura de pantalla (capture.go): cada SO implementa
// `capturarWebcam` con su etiqueta de build y acá vive lo que no depende del
// sistema. Lo que cambia respecto de la pantalla son tres cosas, y las tres
// importan más de lo que parecen:
//
//  1. **LA WEBCAM NO NECESITA SESIÓN GRÁFICA.** La captura de pantalla depende
//     de que alguien haya abierto sesión: sin escritorio no hay nada que
//     capturar y el ciclo reporta `sin_sesion`. La cámara es un dispositivo del
//     sistema, así que el agente la abre aunque el equipo esté en la pantalla de
//     inicio de sesión. Para un equipo robado eso la vuelve MÁS útil que la
//     pantalla, no menos: a quien se lo llevó se le puede tomar la foto antes de
//     que entre a ninguna cuenta.
//
//  2. **EL LED SE ENCIENDE Y NO SE OCULTA.** Es hardware: en la enorme mayoría
//     de los equipos el LED está cableado a la alimentación del sensor y no hay
//     API que lo apague. La foto NUNCA es invisible, ADR-025 lo asume
//     explícitamente y el producto lo documenta en vez de prometer lo contrario.
//     Cualquier intento de ocultarlo sería, además del peor camino legal, una
//     promesa que el hardware no puede cumplir.
//
//  3. **LOS PRIMEROS CUADROS SALEN NEGROS.** No es una avería: el sensor arranca
//     con la exposición automática sin converger y entrega varios cuadros
//     oscuros o completamente negros antes de estabilizar. Una implementación
//     que pida "un cuadro" a la cámara obtiene, casi siempre, ese primer cuadro
//     negro — y ahí nace el "ok falso" de esta fase: una foto negra que parece
//     evidencia. Por eso cada plataforma DESCARTA cuadros de calentamiento y
//     entrega uno posterior; ver `cuadrosDeCalentamiento`.

// Motivos por los que un ciclo no trae foto. Códigos, no texto: viajan al
// servidor y los traduce el catálogo i18n de la consola, igual que los de la
// captura de pantalla.
const (
	// El equipo no tiene ninguna cámara. En una VM del datacenter es lo normal,
	// y es un dato del caso: ese equipo no va a dar fotos nunca.
	webcamNoDevice = "sin_camara"
	// La cámara existe pero otro programa la tiene tomada. Es información útil
	// del caso —alguien está en una videollamada— y no un fallo del producto.
	webcamBusy = "camara_ocupada"
	// No hay ninguna herramienta de captura de cámara instalada (Linux).
	webcamNoTool = "sin_herramienta"
	// El SO niega el permiso de cámara. En macOS es el caso NORMAL y no una
	// excepción: TCC no se concede por administración remota.
	webcamDenied = "permiso_denegado"
	// Se obtuvo una imagen, pero después de descartar los cuadros de
	// calentamiento seguía completamente negra. Cubre tres situaciones que el
	// agente NO puede distinguir entre sí, y por eso el código es uno solo: la
	// tapa de privacidad puesta, la habitación a oscuras, o un controlador que
	// entrega cuadros vacíos. Las tres se ven igual desde acá, y decir cuál es
	// sería inventar.
	webcamNoImage = "camara_sin_imagen"
	// La herramienta corrió y falló, o el archivo salió vacío/ilegible.
	webcamFailed = "fallo_captura"
	// Plataforma sin implementación de cámara.
	webcamUnsupported = "so_no_soportado"
)

// cuadrosDeCalentamiento es cuántos cuadros se descartan antes de quedarse con
// uno. Ver la decisión 3 del encabezado.
//
// 20 no es un número mágico ni una cifra copiada: es el orden de magnitud que
// necesita la exposición automática de una webcam típica a 30 cuadros por
// segundo, o sea unos dos tercios de segundo. Subirlo alarga la captura sin
// ganar nada; bajarlo devuelve la foto negra que esta constante existe para
// evitar.
const cuadrosDeCalentamiento = 20

// WebcamShot es el resultado de intentar una foto. O trae `Path`, o trae
// `Reason`: nunca las dos ni ninguna. Mismo contrato que `ScreenShot`.
type WebcamShot struct {
	Path   string
	Reason string
}

// OK indica si hay un archivo que subir.
func (w WebcamShot) OK() bool { return w.Path != "" }

// webcamFilename arma el nombre con el reloj del EQUIPO, igual que la captura
// de pantalla, y por el mismo motivo: entre el reloj del equipo y el del
// servidor puede haber horas si estuvo sin red.
//
// Extensión `.jpg` porque es lo que entregan las cámaras y las herramientas que
// las manejan. La verificación de imagen en negro decodifica por contenido, no
// por extensión, así que un PNG tampoco la rompería.
func webcamFilename(t time.Time) string {
	return fmt.Sprintf("webcam-%s.jpg", t.UTC().Format("20060102-150405"))
}

// CaptureWebcam intenta una foto y devuelve el resultado ya verificado.
//
// La verificación es la misma idea que en la pantalla y por la misma razón: lo
// único que distingue el éxito del fracaso es MIRAR los píxeles. Una cámara sin
// permiso, con la tapa puesta o con el controlador roto entrega un archivo
// perfectamente formado, del tamaño esperado, y con `exit 0`.
func (a *Agent) CaptureWebcam() WebcamShot {
	shot := a.capturarWebcam(webcamFilename(time.Now()))
	if !shot.OK() {
		return shot
	}

	// Igual que la captura de pantalla: mientras espera la subida es la foto de
	// la CARA de una persona. Se cierra a 0600 en cuanto existe.
	if err := os.Chmod(shot.Path, 0600); err != nil {
		a.Logger.Debugln("CaptureWebcam: no se pudo restringir el permiso del archivo:", err)
	}

	data, err := os.ReadFile(shot.Path)
	if err != nil || len(data) == 0 {
		a.Logger.Debugln("CaptureWebcam: el archivo capturado no se pudo leer:", err)
		_ = os.Remove(shot.Path)
		return WebcamShot{Reason: webcamFailed}
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		// No se pudo decodificar. A diferencia de la pantalla —donde el formato
		// lo decide el SO y se deja pasar— acá una imagen ilegible se descarta:
		// la cámara siempre entrega JPEG o PNG, así que un archivo que no
		// decodifica es una captura a medio escribir, no un formato exótico.
		a.Logger.Debugln("CaptureWebcam: la imagen no se pudo decodificar, se descarta:", err)
		_ = os.Remove(shot.Path)
		return WebcamShot{Reason: webcamFailed}
	}

	if fotoSinImagen(img) {
		// Ya se descartaron los cuadros de calentamiento en la capa de
		// plataforma, así que llegar acá significa que la imagen está negra por
		// algo que el agente no puede ver: la tapa, la oscuridad o el
		// controlador. Se reporta el motivo y NO se sube la foto.
		a.Logger.Infoln("CaptureWebcam: la foto salió completamente en negro tras el calentamiento, se descarta")
		_ = os.Remove(shot.Path)
		return WebcamShot{Reason: webcamNoImage}
	}

	return shot
}

// destinoFotoWebcam decide dónde se escribe la foto mientras espera la subida.
//
// Existe como función con nombre —en vez de una línea dentro de `capturarWebcam`—
// porque acá vivió un bug que ninguna prueba podía ver: era una decisión sin
// nombre, enterrada en medio de la captura, y para ejercitarla hacía falta una
// cámara. Separada, se prueba en cualquier máquina.
//
// Va a `os.TempDir()` y NO a `a.ProgramDir`, igual que la implementación de macOS.
// En Windows `ProgramDir` es el directorio de instalación real, pero en Linux y
// macOS `ResolveProgramDir()` lo arma con `os.Getenv("ProgramFiles")`, una variable
// que sólo existe en Windows: fuera de ahí queda la ruta RELATIVA `ObserverAgent`.
// El servicio corre con el directorio de trabajo en `/`, así que la foto se
// intentaba escribir en `/ObserverAgent/`, que no existe en ninguna instalación.
//
// Medido en terreno el 2026-08-13: `fswebcam` dice `fopen: No such file or
// directory` y **sale con código 0 igual**. Lo único que evitó que el agente diera
// la captura por buena fue la comprobación de tamaño de `capturarWebcam`.
func destinoFotoWebcam(nombre string) string {
	return filepath.Join(os.TempDir(), filepath.Base(nombre))
}

// negroMedioMax es el brillo MEDIO por canal bajo el cual una foto de webcam se
// considera sin imagen.
//
// Es un criterio distinto del de la pantalla (`negroMax` + máximo por muestra) y
// la diferencia la impuso el terreno del 2026-08-13 en un equipo real, con una
// webcam integrada tapada: 15 capturas seguidas de la misma escena, todas con
// brillo medio entre 1,3 y 3,6 sobre 255 —negras a la vista de cualquiera— y las
// 15 clasificadas como imagen VÁLIDA por el criterio de la pantalla. Ninguna
// disparó `camara_sin_imagen`.
//
// La razón es física y no se puede ajustar bajando el umbral: el sensor de una
// cámara real tiene ruido térmico, así que en cualquier imagen oscura hay
// píxeles sueltos por encima de cualquier umbral bajo. Un criterio de "ningún
// píxel supera X" pregunta si existe UN píxel con luz, y en un sensor la
// respuesta es siempre sí. Con `vivid` —la cámara virtual con la que se probó
// primero— no se veía: entrega negro matemático, 0,0,0 exacto, sin ruido.
//
// El promedio sí es estable frente al ruido puntual, que es justamente lo que
// hace falta acá. 12 sobre 255 es menos del 5% de brillo: una foto de una persona
// en penumbra queda muy por encima, así que sigue valiendo el criterio de "ante
// la duda, subir la evidencia".
const negroMedioMax = 12

// fotoSinImagen decide si la foto no muestra nada, que en una webcam es un
// resultado distinto de un fallo y por eso tiene motivo propio.
//
// POR QUÉ NO SE REUSA `imagenEnNegro`: esa función responde "¿hay algo encendido
// en esta pantalla?" y para una pantalla está bien —una ventanita blanca sobre
// fondo negro es evidencia legítima y no hay que descartarla—. Acá la pregunta es
// otra: "¿este sensor está viendo algo?". Con la tapa puesta o el cuarto a
// oscuras la respuesta es no, y el ruido del sensor no la cambia.
//
// Que la distinción importe no es teórico: sin ella la línea de tiempo de un caso
// se llena de fotos negras presentadas como evidencia buena, que además son
// archivos de la cámara de una persona retenidos 90 días. El operador ve una
// miniatura negra y no puede saber si es la tapa, la oscuridad o una avería —
// exactamente lo que el motivo `camara_sin_imagen` existe para decirle.
func fotoSinImagen(img image.Image) bool {
	if img == nil {
		return false
	}

	b := img.Bounds()
	ancho, alto := b.Dx(), b.Dy()
	if ancho < muestrasPorLado || alto < muestrasPorLado {
		// Ante la duda no se descarta: mismo criterio que la pantalla.
		return false
	}

	var suma uint64
	for i := 0; i < muestrasPorLado; i++ {
		for j := 0; j < muestrasPorLado; j++ {
			x := b.Min.X + (ancho*i)/muestrasPorLado
			y := b.Min.Y + (alto*j)/muestrasPorLado

			r, g, bl, _ := img.At(x, y).RGBA()
			suma += uint64(r>>8) + uint64(g>>8) + uint64(bl>>8)
		}
	}

	muestras := uint64(muestrasPorLado) * uint64(muestrasPorLado) * 3
	return suma/muestras <= negroMedioMax
}
