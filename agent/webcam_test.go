package agent

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Feature 030 · Fase 2 · lo que se puede probar sin cámara.
//
// Sin hardware no hay forma de probar la captura en sí, y este archivo NO
// finge que la prueba. Lo que sí se prueba es lo que puede fallar en silencio:
// el nombre del archivo (viaja a un multipart, a tres sistemas de archivos y a
// una URL) y la verificación de imagen en negro sobre JPEG, que es el formato
// que entregan las cámaras y que la Fase 1 nunca tuvo que decodificar.

func TestWebcamFilenameEsASCIIPlanoYOrdenable(t *testing.T) {
	// Los dos puntos del formato ISO son ilegales en nombres de archivo de
	// Windows, y el nombre termina siendo una ruta en disco en los tres SO.
	n := webcamFilename(time.Date(2026, 8, 13, 4, 5, 6, 0, time.UTC))

	if n != "webcam-20260813-040506.jpg" {
		t.Fatalf("nombre = %q", n)
	}
	for _, prohibido := range []string{":", " ", "/", "\\"} {
		if strings.Contains(n, prohibido) {
			t.Errorf("el nombre no puede contener %q: %q", prohibido, n)
		}
	}
}

func TestWebcamFilenameOrdenaCronologicamente(t *testing.T) {
	// El nombre lleva la hora justamente para que ordenar por nombre sea ordenar
	// por tiempo cuando alguien mira el directorio a mano en un peritaje.
	antes := webcamFilename(time.Date(2026, 8, 13, 4, 5, 6, 0, time.UTC))
	despues := webcamFilename(time.Date(2026, 8, 13, 4, 5, 7, 0, time.UTC))

	if !(antes < despues) {
		t.Errorf("%q tenía que ordenar antes que %q", antes, despues)
	}
}

// jpegDeUnColor arma un JPEG real del color pedido. Real y no un fixture: lo que
// se quiere probar es que el decodificador de JPEG se enchufa bien con la
// verificación de negro, y un array de bytes inventado no probaría eso.
func jpegDeUnColor(c color.Color, ancho, alto int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, ancho, alto))
	for x := 0; x < ancho; x++ {
		for y := 0; y < alto; y++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	return buf.Bytes()
}

func TestJPEGNegroSeDetecta(t *testing.T) {
	// El caso que importa: la tapa de privacidad puesta, la habitación a oscuras
	// o un controlador que entrega cuadros vacíos. Los tres dan esto, y los tres
	// tienen que terminar en `camara_sin_imagen` y no en una foto subida.
	img, _, err := image.Decode(bytes.NewReader(jpegDeUnColor(color.Black, 640, 480)))
	if err != nil {
		t.Fatalf("no se pudo decodificar el JPEG de prueba: %v", err)
	}

	if !imagenEnNegro(img) {
		t.Error("un JPEG completamente negro tenía que detectarse como negro")
	}
}

func TestJPEGConContenidoNoSeDescarta(t *testing.T) {
	// El error caro es el otro: descartar una foto buena. Un JPEG de un gris
	// oscuro —una habitación con poca luz, que es evidencia perfectamente
	// válida— no puede leerse como negro.
	img, _, err := image.Decode(bytes.NewReader(jpegDeUnColor(color.RGBA{R: 40, G: 40, B: 45, A: 255}, 640, 480)))
	if err != nil {
		t.Fatalf("no se pudo decodificar el JPEG de prueba: %v", err)
	}

	if imagenEnNegro(img) {
		t.Error("una foto oscura pero con contenido no se puede descartar como negra")
	}
}

func TestWebcamShotContratoExcluyente(t *testing.T) {
	// O archivo, o motivo: nunca los dos ni ninguno. Lo mismo que ScreenShot.
	if (WebcamShot{Path: "/tmp/x.jpg"}).OK() != true {
		t.Error("una pieza con ruta tiene que ser OK")
	}
	if (WebcamShot{Reason: webcamDenied}).OK() != false {
		t.Error("una pieza con motivo no puede ser OK")
	}
	if (WebcamShot{}).OK() != false {
		t.Error("una pieza vacía (webcam apagada) no puede ser OK")
	}
}

func TestCuadrosDeCalentamientoNoEsCero(t *testing.T) {
	// La constante ES la feature: pedir "un cuadro" a una webcam devuelve el
	// primero, que sale negro porque la exposición automática no convergió. Si
	// alguien la pone en 0 "para que sea más rápido", vuelve el ok falso.
	if cuadrosDeCalentamiento < 5 {
		t.Errorf("cuadrosDeCalentamiento = %d: por debajo de ~5 vuelve la foto negra del primer cuadro", cuadrosDeCalentamiento)
	}
}

// TestDestinoDeLaFotoEsRutaAbsoluta congela el arreglo del bug medido en terreno
// el 2026-08-13.
//
// La foto de Linux se escribía en `filepath.Join(a.ProgramDir, nombre)`, y en Linux
// `ProgramDir` no es lo que su nombre sugiere: `ResolveProgramDir()` lo arma con
// `os.Getenv("ProgramFiles")`, una variable que sólo existe en Windows. Fuera de
// Windows queda la ruta RELATIVA `ObserverAgent`, y como el servicio corre con el
// directorio de trabajo en `/`, la foto se intentaba escribir en `/ObserverAgent/`.
//
// El resultado era `fallo_captura` en TODOS los ciclos con la cámara funcionando
// perfectamente: se comprobó a mano que el mismo comando, con destino absoluto,
// capturaba la imagen sin problema.
//
// El test comprueba la propiedad y no la implementación —el destino tiene que ser
// ABSOLUTO— y no necesita cámara, que es justamente lo que le faltaba a la prueba
// original de esta tarea para poder ver el bug.
func TestDestinoDeLaFotoEsRutaAbsoluta(t *testing.T) {
	destino := destinoFotoWebcam(webcamFilename(time.Now()))

	if !filepath.IsAbs(destino) {
		t.Errorf("el destino de la foto es %q, una ruta relativa: el servicio corre con el cwd en / y ahí no existe ese directorio", destino)
	}
}

// TestDestinoDeLaFotoNoDependeDeProgramDir es el control del test de arriba.
//
// Sin esto, cambiar el destino a `ProgramDir` volvería a pasar la prueba anterior
// en cualquier máquina de desarrollo —donde `ProgramFiles` puede estar definida, o
// el cwd ser absoluto— y el bug volvería sin que nadie lo note.
func TestDestinoDeLaFotoNoDependeDeProgramDir(t *testing.T) {
	t.Setenv("ProgramFiles", "")

	if destino := destinoFotoWebcam("webcam-x.jpg"); !filepath.IsAbs(destino) {
		t.Errorf("con ProgramFiles vacía —el estado real de todo Linux— el destino quedó en %q", destino)
	}
}

// imagenConRuidoDeSensor construye una imagen oscura con ruido puntual, que es lo
// que entrega una webcam real tapada. Los números salen de una medición: 15
// capturas de una webcam integrada con la tapa puesta, el 2026-08-13, dieron
// brillo medio entre 1,3 y 3,6 sobre 255 con píxeles sueltos llegando a 25.
func imagenConRuidoDeSensor(medio, pico int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	for y := 0; y < 240; y++ {
		for x := 0; x < 320; x++ {
			v := medio
			// Ruido puntual disperso, como el térmico de un sensor.
			if (x*7+y*13)%97 == 0 {
				v = pico
			}
			img.Set(x, y, color.RGBA{uint8(v), uint8(v), uint8(v), 255})
		}
	}
	return img
}

// TestFotoSinImagenDetectaSensorTapado es el test que habría atrapado el ok falso
// medido en terreno el 2026-08-13.
//
// La foto de webcam usaba `imagenEnNegro`, el criterio de la captura de pantalla:
// "ninguna muestra de la grilla supera `negroMax`". En una webcam real eso no se
// cumple nunca, porque el sensor tiene ruido térmico y siempre hay algún píxel por
// encima de cualquier umbral bajo. Resultado medido en un equipo real con la
// cámara tapada: **15 capturas de 15** clasificadas como imagen válida, todas con
// brillo medio bajo 4 sobre 255 — negras a la vista de cualquiera.
//
// Con `vivid`, la cámara virtual con la que se probó primero, el bug era
// invisible: entrega negro matemático exacto, sin ruido. Hizo falta un sensor de
// verdad para verlo.
func TestFotoSinImagenDetectaSensorTapado(t *testing.T) {
	tapada := imagenConRuidoDeSensor(3, 25)

	if !fotoSinImagen(tapada) {
		t.Error("una webcam tapada (brillo medio 3/255 con ruido a 25) se dio por foto válida: eso sube una imagen negra como evidencia y le oculta al operador que la cámara está tapada")
	}

	// El control de la regresión: dejar escrito que el criterio de la PANTALLA no
	// sirve acá. Si algún día esto pasa a ser `true`, es que alguien volvió a
	// unificar los dos criterios.
	if imagenEnNegro(tapada) {
		t.Error("imagenEnNegro ahora sí detecta el ruido del sensor: revisar si sigue haciendo falta un criterio propio para la webcam")
	}
}

// TestFotoSinImagenNoDescartaEvidenciaBuena es la otra mitad, y la que impide que
// el arreglo se pase de listo: descartar una foto buena es peor que subir una
// negra, porque la foto perdida no se recupera.
func TestFotoSinImagenNoDescartaEvidenciaBuena(t *testing.T) {
	casos := []struct {
		nombre string
		img    image.Image
		quiero bool
	}{
		{"una cara en penumbra, con poca luz pero visible", imagenConRuidoDeSensor(40, 90), false},
		{"una escena normal bien iluminada", imagenConRuidoDeSensor(120, 200), false},
		{"negro perfecto, como el de una cámara virtual", imagenConRuidoDeSensor(0, 0), true},
		{"justo en el umbral", imagenConRuidoDeSensor(negroMedioMax, negroMedioMax), true},
		{"un pelo por encima del umbral", imagenConRuidoDeSensor(negroMedioMax+2, negroMedioMax+2), false},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := fotoSinImagen(c.img); got != c.quiero {
				t.Errorf("fotoSinImagen() = %v, se esperaba %v", got, c.quiero)
			}
		})
	}
}

// TestScriptEstadoTCCCamaraNoDeclaraVariables es el guardián de W-030-F2-08, el
// defecto que sólo el terreno podía destapar y que ninguna prueba veía.
//
// El script decía `set st to (…)`, y `st` es palabra reservada de AppleScript:
// son los ordinales `1st`, `2nd`, `3rd`, `4th`. `osascript -e 'set st to 1'`
// responde `-2741` **incluso sin `use framework`**, así que la consulta de
// permiso fallaba SIEMPRE, `permisoDeCamaraConcedido` devolvía `false`
// incondicionalmente y la cámara de macOS no se habría intentado ni con el
// permiso concedido — publicando el mismo `permiso_denegado` que una denegación
// real, indistinguible desde la consola.
//
// La prueba no compila AppleScript (no hay intérprete en la CI, que corre en
// Linux): prohíbe la construcción que causó el bug. Un script que no declara
// variables no puede chocar con una palabra reservada.
func TestScriptEstadoTCCCamaraNoDeclaraVariables(t *testing.T) {
	for _, linea := range strings.Split(scriptEstadoTCCCamara, "\n") {
		if strings.HasPrefix(strings.TrimSpace(linea), "set ") {
			t.Errorf("el script declara una variable (%q). AppleScript reserva st/nd/rd/th "+
				"—los ordinales— y un nombre de dos letras rompe el script ENTERO, "+
				"dejando el preflight en false para siempre. Ver W-030-F2-08", strings.TrimSpace(linea))
		}
	}
}

// TestScriptEstadoTCCCamaraSigueConsultandoLaCamara impide que el guardián de
// arriba se "cumpla" vaciando el script: sin estas dos piezas la consulta deja
// de preguntar por el permiso de vídeo y el preflight volvería a mentir, ahora
// en silencio y en verde.
func TestScriptEstadoTCCCamaraSigueConsultandoLaCamara(t *testing.T) {
	for _, pieza := range []string{"AVFoundation", "authorizationStatusForMediaType:", "AVMediaTypeVideo"} {
		if !strings.Contains(scriptEstadoTCCCamara, pieza) {
			t.Errorf("el script ya no contiene %q: dejó de consultar el permiso de Cámara", pieza)
		}
	}
}
