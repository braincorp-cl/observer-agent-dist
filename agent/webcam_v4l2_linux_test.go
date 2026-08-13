//go:build linux
// +build linux

package agent

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
	"unsafe"
)

// Feature 030 · Fase 2 · lo que se puede probar de la captura V4L2 sin cámara.
//
// Estas pruebas cubren la parte que NO se puede ver en terreno hasta que ya es
// tarde: si una struct queda de un tamaño distinto al del kernel, el número de
// ioctl cambia y el kernel responde EINVAL en el equipo del cliente. Compila
// igual, pasa la revisión igual, y falla sólo donde hay una cámara.

// TestTamanosABI fija los tamaños contra los de C.
//
// Los valores de 64 bits se verificaron el 2026-08-13 contra el kernel de
// FAZOCAR (Ubuntu 24.04, x86_64) con una captura real. Los de 32 bits salen de
// la misma ABI con `long` de 4 bytes y el union alineado a 4.
func TestTamanosABI(t *testing.T) {
	de64 := unsafe.Sizeof(uintptr(0)) == 8

	casos := []struct {
		nombre string
		real   uintptr
		en64   uintptr
		en32   uintptr
	}{
		{"v4l2_capability", unsafe.Sizeof(v4l2Capability{}), 104, 104},
		{"v4l2_fmtdesc", unsafe.Sizeof(v4l2Fmtdesc{}), 64, 64},
		{"v4l2_pix_format", unsafe.Sizeof(v4l2PixFormat{}), 48, 48},
		{"v4l2_format", unsafe.Sizeof(v4l2Format{}), 208, 204},
		{"v4l2_requestbuffers", unsafe.Sizeof(v4l2RequestBuffers{}), 20, 20},
		{"v4l2_timecode", unsafe.Sizeof(v4l2Timecode{}), 16, 16},
		{"v4l2_buffer", unsafe.Sizeof(v4l2Buffer{}), 88, 68},
	}

	for _, c := range casos {
		esperado := c.en32
		if de64 {
			esperado = c.en64
		}
		if c.real != esperado {
			t.Errorf("%s mide %d bytes y el kernel espera %d: el ioctl saldría con el número equivocado",
				c.nombre, c.real, esperado)
		}
	}
}

// TestNumerosDeIoctl compara contra los números que publica Linux.
//
// Es la prueba que atrapa un campo mal puesto sin necesidad de cámara: el número
// codifica el tamaño del struct, así que si el layout se desvía, estos valores
// dejan de coincidir con los del kernel.
func TestNumerosDeIoctl(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("los valores publicados que se comparan acá son los de 64 bits")
	}

	casos := []struct {
		nombre   string
		real     uintptr
		esperado uintptr
	}{
		{"VIDIOC_QUERYCAP", vidiocQuerycap, 0x80685600},
		{"VIDIOC_ENUM_FMT", vidiocEnumFmt, 0xc0405602},
		{"VIDIOC_S_FMT", vidiocSFmt, 0xc0d05605},
		{"VIDIOC_REQBUFS", vidiocReqbufs, 0xc0145608},
		{"VIDIOC_QUERYBUF", vidiocQuerybuf, 0xc0585609},
		{"VIDIOC_QBUF", vidiocQbuf, 0xc058560f},
		{"VIDIOC_DQBUF", vidiocDqbuf, 0xc0585611},
		{"VIDIOC_STREAMON", vidiocStreamon, 0x40045612},
		{"VIDIOC_STREAMOFF", vidiocStreamoff, 0x40045613},
	}

	for _, c := range casos {
		if c.real != c.esperado {
			t.Errorf("%s = 0x%08x y el kernel usa 0x%08x", c.nombre, c.real, c.esperado)
		}
	}
}

// TestEsNodoDeCaptura usa las capacidades REALES de FAZOCAR.
//
// Una sola cámara USB publica dos nodos y sólo uno da imágenes. Sin esta
// distinción, el nodo de metadatos devuelve un fallo de formato que se leería
// como «la cámara falló» en un equipo cuya cámara está perfecta.
func TestEsNodoDeCaptura(t *testing.T) {
	// /dev/video0 de FAZOCAR: la cámara.
	camara := v4l2Capability{Capabilities: 0x84a00001, DeviceCaps: 0x04200001}
	if !esNodoDeCaptura(camara) {
		t.Error("el nodo de captura de FAZOCAR tenía que reconocerse como cámara")
	}

	// /dev/video1 de FAZOCAR: los metadatos de la MISMA cámara. Ojo que
	// `Capabilities` es idéntico —describe el dispositivo entero—, así que mirar
	// ese campo daría cámara en los dos.
	metadatos := v4l2Capability{Capabilities: 0x84a00001, DeviceCaps: 0x04a00000}
	if esNodoDeCaptura(metadatos) {
		t.Error("el nodo de metadatos no puede pasar por cámara")
	}

	// Sin `V4L2_CAP_DEVICE_CAPS` manda `Capabilities`, que es como responden los
	// controladores viejos.
	viejo := v4l2Capability{Capabilities: capVideoCapture | capStreaming}
	if !esNodoDeCaptura(viejo) {
		t.Error("un controlador sin DEVICE_CAPS tiene que decidirse por Capabilities")
	}

	// Captura pero sin streaming: no sirve para el anillo de mmap.
	soloLectura := v4l2Capability{Capabilities: capVideoCapture}
	if esNodoDeCaptura(soloLectura) {
		t.Error("sin streaming no se puede capturar por mmap")
	}
}

// TestCodificarCuadroMJPEGNoRecodifica es el ahorro entero de esta enmienda: el
// cuadro que entrega la cámara YA es un JPEG y tiene que salir byte por byte
// igual, sin pasar por ningún codificador.
func TestCodificarCuadroMJPEGNoRecodifica(t *testing.T) {
	var jpg bytes.Buffer
	img := image.NewGray(image.Rect(0, 0, 16, 16))
	if err := jpeg.Encode(&jpg, img, nil); err != nil {
		t.Fatal(err)
	}
	original := jpg.Bytes()

	salida, err := codificarCuadro(original, fmtMJPEG, 16, 16)
	if err != nil {
		t.Fatalf("no tenía que fallar: %v", err)
	}
	if !bytes.Equal(salida, original) {
		t.Error("el cuadro MJPEG se alteró: tiene que salir tal cual")
	}
}

// TestCodificarCuadroRechazaMJPEGSinFirma evita el ok falso más fácil de esta
// fase: escribir un `.jpg` con cualquier cosa adentro. El archivo existiría, con
// tamaño, y sólo se descubriría al abrirlo.
func TestCodificarCuadroRechazaMJPEGSinFirma(t *testing.T) {
	if _, err := codificarCuadro([]byte("no soy un jpeg"), fmtMJPEG, 16, 16); err == nil {
		t.Error("un cuadro sin la firma JPEG tenía que rechazarse")
	}
	if _, err := codificarCuadro(nil, fmtMJPEG, 16, 16); err == nil {
		t.Error("un cuadro vacío tenía que rechazarse")
	}
}

// TestCodificarCuadroYUYV comprueba el camino de las cámaras que no dan MJPEG:
// tiene que salir un JPEG que decodifique y que conserve el brillo, porque una
// conversión torcida devolvería justo la foto oscura que toda esta fase intenta
// evitar.
func TestCodificarCuadroYUYV(t *testing.T) {
	const ancho, alto = 32, 16

	// Gris claro uniforme: Y alto, croma neutro (128).
	crudo := make([]byte, ancho*alto*2)
	for i := 0; i < len(crudo); i += 2 {
		crudo[i] = 200
		crudo[i+1] = 128
	}

	salida, err := codificarCuadro(crudo, fmtYUYV, ancho, alto)
	if err != nil {
		t.Fatalf("no tenía que fallar: %v", err)
	}

	img, formato, err := image.Decode(bytes.NewReader(salida))
	if err != nil {
		t.Fatalf("la salida no decodifica: %v", err)
	}
	if formato != "jpeg" {
		t.Errorf("se esperaba jpeg y salió %q", formato)
	}
	if b := img.Bounds(); b.Dx() != ancho || b.Dy() != alto {
		t.Errorf("dimensiones %dx%d, se esperaba %dx%d", b.Dx(), b.Dy(), ancho, alto)
	}

	r, g, bl, _ := img.At(ancho/2, alto/2).RGBA()
	medio := (r>>8 + g>>8 + bl>>8) / 3
	if medio < 150 {
		t.Errorf("un cuadro gris claro salió con brillo %d: la conversión YUYV está oscureciendo la imagen", medio)
	}
	if fotoSinImagen(img) {
		t.Error("un cuadro gris claro no puede clasificarse como imagen en negro")
	}
}

// TestCodificarCuadroYUYVCorto: un cuadro más corto de lo que dicen las
// dimensiones se rechaza en vez de leer fuera del buffer.
func TestCodificarCuadroYUYVCorto(t *testing.T) {
	if _, err := codificarCuadro(make([]byte, 10), fmtYUYV, 32, 16); err == nil {
		t.Error("un cuadro YUYV corto tenía que rechazarse")
	}
}

func TestCodificarCuadroFormatoDesconocido(t *testing.T) {
	if _, err := codificarCuadro([]byte{0xFF, 0xD8, 0xFF}, 0x3231564E /* NV12 */, 16, 16); err == nil {
		t.Error("un formato que no sabemos leer tenía que dar error, no un archivo con basura")
	}
}

func TestFourcc(t *testing.T) {
	if got := fourcc(fmtMJPEG); got != "MJPG" {
		t.Errorf("fourcc(MJPEG) = %q", got)
	}
	if got := fourcc(fmtYUYV); got != "YUYV" {
		t.Errorf("fourcc(YUYV) = %q", got)
	}
}
