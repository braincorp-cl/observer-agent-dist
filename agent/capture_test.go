package agent

import (
	"image"
	"image/color"
	"strings"
	"testing"
	"time"
)

// Feature 030 · Fase 1 · pruebas del contrato de captura.
//
// Lo que se prueba acá es exactamente lo que en el terreno se equivoca en
// silencio: decidir si una imagen está en negro (el "ok falso" de macOS sin
// permiso y del BitBlt en la sesión 0) y leer la respuesta del sub-proceso de
// Windows. La captura en sí depende de que haya un escritorio vivo, así que su
// verificación es el terreno; estas dos decisiones no dependen de nada y por eso
// no tienen excusa para no estar cubiertas.

// imagenPlana arma una imagen de un solo color.
func imagenPlana(ancho, alto int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, ancho, alto))
	for y := 0; y < alto; y++ {
		for x := 0; x < ancho; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func TestImagenEnNegroDetectaLaPantallaNegra(t *testing.T) {
	negra := imagenPlana(1920, 1080, color.RGBA{A: 0xff})

	if !imagenEnNegro(negra) {
		t.Error("una imagen completamente negra tiene que detectarse como tal: es el resultado de macOS sin permiso TCC y del BitBlt hecho desde la sesión 0")
	}
}

func TestImagenEnNegroNoDescartaUnEscritorioOscuro(t *testing.T) {
	// Gris muy oscuro, del orden de un tema oscuro real. Descartar esto sería
	// perder evidencia buena, que es el error caro.
	oscura := imagenPlana(1920, 1080, color.RGBA{R: 24, G: 24, B: 28, A: 0xff})

	if imagenEnNegro(oscura) {
		t.Error("un escritorio con tema oscuro no es una pantalla negra")
	}
}

func TestImagenEnNegroBastaUnaMuestraConLuz(t *testing.T) {
	// Una sola ventana clara en una pantalla por lo demás negra. La grilla de
	// muestreo tiene que verla: si no, un equipo con el escritorio apagado y una
	// única ventana abierta —el caso interesante— se descartaría.
	img := imagenPlana(640, 480, color.RGBA{A: 0xff})
	for y := 0; y < 480; y++ {
		for x := 0; x < 320; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 200, G: 200, B: 200, A: 0xff})
		}
	}

	if imagenEnNegro(img) {
		t.Error("con media pantalla iluminada no se puede declarar negra")
	}
}

func TestImagenEnNegroAnteLaDudaNoDescarta(t *testing.T) {
	// Imagen más chica que la grilla de muestreo, y nil. En los dos casos la
	// respuesta correcta es "no sé, no la descartes".
	if imagenEnNegro(imagenPlana(4, 4, color.RGBA{A: 0xff})) {
		t.Error("una imagen más chica que la grilla no se puede juzgar")
	}
	if imagenEnNegro(nil) {
		t.Error("nil no es una pantalla negra")
	}
}

func TestScreenshotFilenameEsSeguroEnLosTresSO(t *testing.T) {
	// 12:34:56 UTC del 2026-08-12.
	nombre := screenshotFilename(time.Date(2026, 8, 12, 12, 34, 56, 0, time.UTC))

	if nombre != "pantalla-20260812-123456.png" {
		t.Errorf("nombre = %q, se esperaba pantalla-20260812-123456.png", nombre)
	}

	// Los dos puntos del formato ISO son ilegales en nombres de archivo de
	// Windows, y los acentos viajan mal por el multipart y por la URL.
	for _, prohibido := range []string{":", " ", "á", "\\", "/"} {
		if strings.Contains(nombre, prohibido) {
			t.Errorf("el nombre %q contiene %q, que no sobrevive a los tres SO", nombre, prohibido)
		}
	}
}

func TestScreenshotFilenameUsaUTC(t *testing.T) {
	// Mismo instante, husos distintos: el nombre tiene que ser el mismo. Si el
	// nombre dependiera del huso del equipo, dos capturas simultáneas de dos
	// equipos en distintos husos parecerían de horas distintas.
	instante := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	santiago := time.FixedZone("CLT", -4*3600)

	if a, b := screenshotFilename(instante), screenshotFilename(instante.In(santiago)); a != b {
		t.Errorf("el nombre cambió con el huso: %q vs %q", a, b)
	}
}

func TestParsearSalidaCaptura(t *testing.T) {
	casos := []struct {
		nombre string
		salida string
		quiere ScreenShot
	}{
		{
			nombre: "éxito",
			salida: "OK C:\\Users\\ana\\AppData\\Local\\Temp\\pantalla-20260812-123456.png\r\n",
			quiere: ScreenShot{Path: `C:\Users\ana\AppData\Local\Temp\pantalla-20260812-123456.png`},
		},
		{
			nombre: "fallo con motivo",
			salida: "FAIL " + captureDenied + "\n",
			quiere: ScreenShot{Reason: captureDenied},
		},
		{
			nombre: "ruido antes de la respuesta",
			salida: "time=\"2026-08-12\" level=error msg=\"algo\"\nOK /tmp/x.png\n",
			quiere: ScreenShot{Path: "/tmp/x.png"},
		},
		{
			// El caso que importa: el hijo no dijo nada (murió, lo mató el
			// antivirus, no arrancó). Leer el vacío como éxito daría una fila de
			// evidencia apuntando a un archivo inexistente.
			nombre: "salida vacía",
			salida: "",
			quiere: ScreenShot{Reason: captureFailed},
		},
		{
			nombre: "OK sin ruta",
			salida: "OK \n",
			quiere: ScreenShot{Reason: captureFailed},
		},
		{
			nombre: "texto incomprensible",
			salida: "Acceso denegado.\n",
			quiere: ScreenShot{Reason: captureFailed},
		},
	}

	for _, c := range casos {
		got := parsearSalidaCaptura(c.salida)
		if got != c.quiere {
			t.Errorf("%s: parsearSalidaCaptura(%q) = %+v, se esperaba %+v", c.nombre, c.salida, got, c.quiere)
		}
	}
}

func TestScreenShotOK(t *testing.T) {
	if (ScreenShot{Reason: captureWayland}).OK() {
		t.Error("un resultado con motivo y sin archivo no es una captura")
	}
	if !(ScreenShot{Path: "/tmp/x.png"}).OK() {
		t.Error("un resultado con archivo es una captura")
	}
}
