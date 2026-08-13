//go:build linux
// +build linux

package agent

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Feature 030 · Fase 2 · captura de webcam NATIVA por V4L2, sin cgo (enmienda de
// W-030-F2-06).
//
// POR QUÉ EXISTE ESTE ARCHIVO: hasta la 2.15.17 la foto de Linux dependía de que
// el equipo tuviera `fswebcam` o `ffmpeg` instalado, y el terreno midió lo que
// eso vale — DOS de dos equipos Linux reales no tenían ninguno de los dos Y no
// podían instalarlos:
//
//   - `fedora` (2026-08-13): `dnf` falla con «Curl error (60): SSL peer
//     certificate ... self-signed certificate in certificate chain», un
//     middlebox que intercepta TLS rompe la validación de la cadena.
//   - `FAZOCAR` (2026-08-13): los repositorios bloqueados por la red del cliente.
//
// O sea que el caso que originó la funcionalidad —un equipo de cliente, en la
// red del cliente— era justo el que se quedaba en `sin_herramienta`. La salida
// obvia parecía ser que el instalador instalara el paquete; no sirve, y no por
// una razón sino por cuatro: la red del cliente puede no dejarlo (medido, 2 de
// 2), el nombre del paquete son cuatro caminos distintos (`ffmpeg` en Debian,
// `ffmpeg-free` en Fedora, RPM Fusion —repositorio de TERCEROS— si se quiere el
// completo, EPEL+RPM Fusion en RHEL), el árbol pesa 23 MB en 10 paquetes para
// una función APAGADA DE FÁBRICA (ADR-025), y cambia el contrato del instalador,
// que hoy no toca el gestor de paquetes del cliente.
//
// La salida es la misma que tomó T014 en Windows con Media Foundation: hacerlo
// con lo que el sistema YA tiene. En Linux eso es V4L2, que es el kernel — no
// hay nada que instalar y no hay nada que la red pueda bloquear.
//
// Y sale barato porque la cámara hace el trabajo pesado: casi todas entregan
// **MJPEG**, y un cuadro MJPEG YA ES un JPEG. Medido en FAZOCAR el 2026-08-13:
// 1280x720, 109.894 bytes, 1,055 s con los 20 cuadros de calentamiento
// incluidos, sin codificar nada. Para las cámaras que sólo dan YUYV se codifica
// con `image/jpeg` de la biblioteca estándar.
//
// SIN CGO ES REQUISITO, NO PREFERENCIA: el agente cruza-compila 8 binarios desde
// un solo runner. Cualquier `import "C"` rompe eso. Por eso acá hay ioctls
// crudos y no una biblioteca de cámara.

// ---------------------------------------------------------------------------
// ABI de V4L2
//
// Estas structs tienen que coincidir BYTE A BYTE con las del kernel, y el
// problema no es sólo leer campos torcidos: el número de cada ioctl CODIFICA el
// tamaño del struct (ver `ioc`), así que un campo de más manda un ioctl que el
// kernel no reconoce. Un error de layout no se ve al compilar — se ve como
// EINVAL en el equipo del cliente.
//
// Hay exactamente dos lugares donde el layout cambia entre arquitecturas, y los
// dos se resuelven SIN etiquetas de build:
//
//  1. El `union` de `struct v4l2_format` contiene punteros (`v4l2_window`), así
//     que se alinea a 8 en 64 bits y a 4 en 32 bits. El campo `_ [0]uintptr`
//     ocupa cero bytes pero arrastra esa misma alineación, y el padding sale
//     solo: 208 bytes en 64 bits, 204 en 32.
//
//  2. `struct timeval` del kernel y el `union m` de `v4l2_buffer` son `long`.
//     El `int` de Go es exactamente `long` de C en Linux (LP64 en amd64/arm64,
//     ILP32 en 386/arm), así que declararlos `int`/`uintptr` da el tamaño
//     correcto en las cuatro arquitecturas sin escribir una línea por cada una.
//
// Verificado el 2026-08-13 contra el kernel de FAZOCAR (Ubuntu 24.04, x86_64):
// 104 / 64 / 208 / 20 / 88, que son los tamaños canónicos de C. Los tamaños se
// comprueban además en `webcam_v4l2_linux_test.go`, que corre en la CI.
// ---------------------------------------------------------------------------

type v4l2Capability struct {
	Driver       [16]byte
	Card         [32]byte
	BusInfo      [32]byte
	Version      uint32
	Capabilities uint32
	DeviceCaps   uint32
	Reserved     [3]uint32
}

type v4l2Fmtdesc struct {
	Index       uint32
	Type        uint32
	Flags       uint32
	Description [32]byte
	PixelFormat uint32
	MbusCode    uint32
	Reserved    [3]uint32
}

type v4l2PixFormat struct {
	Width        uint32
	Height       uint32
	PixelFormat  uint32
	Field        uint32
	BytesPerLine uint32
	SizeImage    uint32
	Colorspace   uint32
	Priv         uint32
	Flags        uint32
	YcbcrEnc     uint32
	Quantization uint32
	XferFunc     uint32
}

type v4l2Format struct {
	Type uint32
	_    [0]uintptr // ver la nota 1 del encabezado: alineación del union de C
	Data [200]byte
}

func (f *v4l2Format) pix() *v4l2PixFormat {
	return (*v4l2PixFormat)(unsafe.Pointer(&f.Data[0]))
}

type v4l2RequestBuffers struct {
	Count        uint32
	Type         uint32
	Memory       uint32
	Capabilities uint32
	Flags        uint8
	Reserved     [3]uint8
}

type v4l2Timecode struct {
	Type     uint32
	Flags    uint32
	Frames   uint8
	Seconds  uint8
	Minutes  uint8
	Hours    uint8
	Userbits [4]uint8
}

// v4l2Timeval es el `struct timeval` del KERNEL. Ver la nota 2 del encabezado:
// `int` de Go es `long` de C en Linux, que es justo lo que el kernel espera acá
// (y no el `timeval` de 64 bits que glibc usa en el espacio de usuario de las
// arquitecturas de 32 bits).
type v4l2Timeval struct {
	Sec  int
	Usec int
}

type v4l2Buffer struct {
	Index     uint32
	Type      uint32
	BytesUsed uint32
	Flags     uint32
	Field     uint32
	Timestamp v4l2Timeval
	Timecode  v4l2Timecode
	Sequence  uint32
	Memory    uint32
	M         uintptr
	Length    uint32
	Reserved2 uint32
	RequestFD int32
}

const (
	iocWrite = 1
	iocRead  = 2
)

// ioc arma el número de ioctl igual que la macro `_IOC` de Linux: dirección en
// los dos bits altos, tamaño del struct, y el par (tipo, número) que identifica
// la llamada. Todos los ioctls de V4L2 usan el tipo 'V'.
func ioc(dir, nr, size uintptr) uintptr {
	return dir<<30 | size<<16 | 'V'<<8 | nr
}

var (
	vidiocQuerycap  = ioc(iocRead, 0, unsafe.Sizeof(v4l2Capability{}))
	vidiocEnumFmt   = ioc(iocRead|iocWrite, 2, unsafe.Sizeof(v4l2Fmtdesc{}))
	vidiocSFmt      = ioc(iocRead|iocWrite, 5, unsafe.Sizeof(v4l2Format{}))
	vidiocReqbufs   = ioc(iocRead|iocWrite, 8, unsafe.Sizeof(v4l2RequestBuffers{}))
	vidiocQuerybuf  = ioc(iocRead|iocWrite, 9, unsafe.Sizeof(v4l2Buffer{}))
	vidiocQbuf      = ioc(iocRead|iocWrite, 15, unsafe.Sizeof(v4l2Buffer{}))
	vidiocDqbuf     = ioc(iocRead|iocWrite, 17, unsafe.Sizeof(v4l2Buffer{}))
	vidiocStreamon  = ioc(iocWrite, 18, unsafe.Sizeof(int32(0)))
	vidiocStreamoff = ioc(iocWrite, 19, unsafe.Sizeof(int32(0)))
)

const (
	capVideoCapture = 0x00000001
	capStreaming    = 0x04000000
	// Cuando está `capDeviceCaps`, el campo que vale es `DeviceCaps` (lo que hace
	// ESTE nodo) y no `Capabilities` (lo que hace el dispositivo entero). La
	// diferencia es exactamente la que separa la cámara de su nodo de metadatos.
	capDeviceCaps = 0x80000000

	bufTypeVideoCapture = 1
	memoryMMAP          = 1

	fmtMJPEG = 0x47504A4D // 'MJPG'
	fmtJPEG  = 0x4745504A // 'JPEG'
	fmtYUYV  = 0x56595559 // 'YUYV'
)

// Resolución que se le pide a la cámara. El driver la ajusta a la más cercana
// que sepa dar y devuelve la elegida en el mismo `S_FMT`, así que esto es una
// preferencia y no un requisito: si la cámara sólo hace 640x480, entrega
// 640x480. 720p es el punto donde una cara se reconoce sin que el archivo pese
// de más para una subida desde la red de un cliente.
const (
	anchoPedido = 1280
	altoPedido  = 720
)

// buffersPedidos son los buffers en el anillo de mmap. Cuatro es el mínimo
// cómodo: con menos, el driver puede quedarse sin dónde escribir mientras el
// agente descarta cuadros de calentamiento y se pierden cuadros.
const buffersPedidos = 4

// plazoCaptura es cuánto se espera por el cuadro bueno. Medido: 1,055 s en una
// cámara USB normal con los 20 cuadros de calentamiento. 15 s es holgado y a la
// vez impide que una cámara que no responde deje clavado el ciclo del agente.
const plazoCaptura = 15 * time.Second

func ioctl(fd int, req uintptr, arg unsafe.Pointer) error {
	for {
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
		if errno == 0 {
			return nil
		}
		// Una señal puede interrumpir el ioctl sin que haya pasado nada malo.
		if errno == unix.EINTR {
			continue
		}
		return errno
	}
}

// errNodoNoEsDeCaptura marca el nodo que existe pero no captura video: el de
// METADATOS que el kernel publica junto a cada cámara USB desde Linux 4.16.
// Distinguirlo importa — tratarlo como fallo diría «fallo_captura» en un equipo
// cuya cámara está perfecta, y tratarlo como «sin cámara» sería directamente
// falso. Medido en FAZOCAR: `/dev/video0` captura, `/dev/video1` es metadatos.
var errNodoNoEsDeCaptura = errors.New("el nodo no captura video")

// capturarV4L2 saca una foto del nodo indicado y devuelve el JPEG en memoria.
//
// No escribe archivos a propósito: quien decide dónde cae la foto es
// `destinoFotoWebcam`, que ya tiene su propia historia de bug (ver webcam.go).
// Acá sólo se consiguen los bytes.
func capturarV4L2(nodo string) ([]byte, error) {
	fd, err := unix.Open(nodo, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", nodo, err)
	}
	defer unix.Close(fd)

	var caps v4l2Capability
	if err := ioctl(fd, vidiocQuerycap, unsafe.Pointer(&caps)); err != nil {
		return nil, fmt.Errorf("QUERYCAP: %w", err)
	}
	if !esNodoDeCaptura(caps) {
		return nil, errNodoNoEsDeCaptura
	}

	formato, err := elegirFormato(fd)
	if err != nil {
		return nil, err
	}

	var f v4l2Format
	f.Type = bufTypeVideoCapture
	p := f.pix()
	p.Width, p.Height, p.PixelFormat, p.Field = anchoPedido, altoPedido, formato, 0
	if err := ioctl(fd, vidiocSFmt, unsafe.Pointer(&f)); err != nil {
		return nil, fmt.Errorf("S_FMT: %w", err)
	}
	// Lo que vale es lo que el driver NEGOCIÓ, no lo que se pidió.
	p = f.pix()
	negociado, ancho, alto := p.PixelFormat, p.Width, p.Height

	req := v4l2RequestBuffers{Count: buffersPedidos, Type: bufTypeVideoCapture, Memory: memoryMMAP}
	if err := ioctl(fd, vidiocReqbufs, unsafe.Pointer(&req)); err != nil {
		return nil, fmt.Errorf("REQBUFS: %w", err)
	}
	if req.Count < 1 {
		return nil, errors.New("el controlador no entregó buffers")
	}

	buffers := make([][]byte, req.Count)
	defer func() {
		for _, b := range buffers {
			if b != nil {
				_ = unix.Munmap(b)
			}
		}
	}()

	for i := uint32(0); i < req.Count; i++ {
		qb := v4l2Buffer{Index: i, Type: bufTypeVideoCapture, Memory: memoryMMAP}
		if err := ioctl(fd, vidiocQuerybuf, unsafe.Pointer(&qb)); err != nil {
			return nil, fmt.Errorf("QUERYBUF %d: %w", i, err)
		}
		mem, err := unix.Mmap(fd, int64(qb.M), int(qb.Length), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
		if err != nil {
			return nil, fmt.Errorf("mmap %d: %w", i, err)
		}
		buffers[i] = mem
		if err := ioctl(fd, vidiocQbuf, unsafe.Pointer(&qb)); err != nil {
			return nil, fmt.Errorf("QBUF %d: %w", i, err)
		}
	}

	tipo := int32(bufTypeVideoCapture)
	if err := ioctl(fd, vidiocStreamon, unsafe.Pointer(&tipo)); err != nil {
		// EBUSY acá es el caso «otro programa tiene la cámara» (una
		// videollamada). Lo distingue quien llama, mirando el errno.
		return nil, fmt.Errorf("STREAMON: %w", err)
	}
	defer func() { _ = ioctl(fd, vidiocStreamoff, unsafe.Pointer(&tipo)) }()

	datos, err := leerCuadroBueno(fd, buffers)
	if err != nil {
		return nil, err
	}
	return codificarCuadro(datos, negociado, ancho, alto)
}

// leerCuadroBueno descarta los cuadros de calentamiento y devuelve el siguiente.
//
// Es el corazón del asunto y la razón por la que esto no es «leer un cuadro»:
// una webcam entrega sus primeros cuadros oscuros o completamente negros
// mientras la exposición automática converge (ver `cuadrosDeCalentamiento` en
// webcam.go). Cada cuadro descartado se devuelve al anillo con QBUF; si no, el
// driver se queda sin buffers en el cuadro número cinco.
func leerCuadroBueno(fd int, buffers [][]byte) ([]byte, error) {
	limite := time.Now().Add(plazoCaptura)

	for n := 0; ; n++ {
		restante := time.Until(limite)
		if restante <= 0 {
			return nil, errors.New("se agotó el plazo esperando cuadros de la cámara")
		}

		// El descriptor está en O_NONBLOCK, así que la espera es explícita: sin
		// esto, DQBUF devolvería EAGAIN en un bucle que quema CPU.
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		if _, err := unix.Poll(fds, int(restante.Milliseconds())); err != nil {
			if err == unix.EINTR {
				continue
			}
			return nil, fmt.Errorf("poll: %w", err)
		}
		if fds[0].Revents&unix.POLLIN == 0 {
			continue
		}

		db := v4l2Buffer{Type: bufTypeVideoCapture, Memory: memoryMMAP}
		if err := ioctl(fd, vidiocDqbuf, unsafe.Pointer(&db)); err != nil {
			if err == unix.EAGAIN {
				continue
			}
			return nil, fmt.Errorf("DQBUF: %w", err)
		}
		if int(db.Index) >= len(buffers) {
			return nil, fmt.Errorf("el driver devolvió el buffer %d, fuera del anillo de %d", db.Index, len(buffers))
		}

		if n >= cuadrosDeCalentamiento {
			// Se COPIA: el buffer es memoria mapeada del driver y se reutiliza en
			// cuanto vuelve al anillo.
			datos := make([]byte, db.BytesUsed)
			copy(datos, buffers[db.Index][:db.BytesUsed])
			if len(datos) == 0 {
				return nil, errors.New("la cámara entregó un cuadro vacío")
			}
			return datos, nil
		}

		if err := ioctl(fd, vidiocQbuf, unsafe.Pointer(&db)); err != nil {
			return nil, fmt.Errorf("QBUF (cuadro descartado): %w", err)
		}
	}
}

// esNodoDeCaptura responde si por este nodo salen imágenes.
func esNodoDeCaptura(c v4l2Capability) bool {
	caps := c.Capabilities
	if caps&capDeviceCaps != 0 {
		caps = c.DeviceCaps
	}
	return caps&capVideoCapture != 0 && caps&capStreaming != 0
}

// elegirFormato prefiere MJPEG y acepta YUYV.
//
// El orden no es estético: en MJPEG el cuadro ya viene comprimido por la cámara
// y el agente no codifica NADA —se escriben los bytes tal cual—, mientras que
// YUYV obliga a recorrer píxel por píxel y codificar un JPEG. Casi todas las
// webcams USB ofrecen los dos.
func elegirFormato(fd int) (uint32, error) {
	elegido := uint32(0)
	for i := uint32(0); ; i++ {
		desc := v4l2Fmtdesc{Index: i, Type: bufTypeVideoCapture}
		if err := ioctl(fd, vidiocEnumFmt, unsafe.Pointer(&desc)); err != nil {
			// EINVAL acá significa «no hay más formatos», que es como V4L2
			// termina la enumeración.
			break
		}
		switch desc.PixelFormat {
		case fmtMJPEG, fmtJPEG:
			return desc.PixelFormat, nil
		case fmtYUYV:
			if elegido == 0 {
				elegido = fmtYUYV
			}
		}
	}
	if elegido == 0 {
		return 0, errors.New("la cámara no ofrece MJPEG ni YUYV")
	}
	return elegido, nil
}

// codificarCuadro deja el cuadro en JPEG.
func codificarCuadro(datos []byte, formato, ancho, alto uint32) ([]byte, error) {
	switch formato {
	case fmtMJPEG, fmtJPEG:
		// El cuadro YA ES un JPEG: acá está el ahorro entero de esta enmienda.
		// Aun así se comprueba la firma, porque escribir un archivo `.jpg` con
		// cualquier cosa adentro es el ok falso más fácil de esta fase.
		if len(datos) < 3 || datos[0] != 0xFF || datos[1] != 0xD8 || datos[2] != 0xFF {
			return nil, errors.New("el cuadro MJPEG no empieza con la firma JPEG")
		}
		return datos, nil

	case fmtYUYV:
		esperado := int(ancho) * int(alto) * 2
		if len(datos) < esperado {
			return nil, fmt.Errorf("cuadro YUYV corto: %d bytes para %dx%d", len(datos), ancho, alto)
		}
		img := yuyvAImagen(datos, int(ancho), int(alto))
		var out bytes.Buffer
		if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 85}); err != nil {
			return nil, fmt.Errorf("codificando el cuadro YUYV: %w", err)
		}
		return out.Bytes(), nil
	}

	return nil, fmt.Errorf("formato de cuadro inesperado: %s", fourcc(formato))
}

// yuyvAImagen arma la imagen desde el crudo YUYV 4:2:2, que es exactamente el
// muestreo de `image.YCbCrSubsampleRatio422`: cada par de píxeles comparte un
// par (Cb, Cr) y trae su propia luminancia.
func yuyvAImagen(datos []byte, ancho, alto int) image.Image {
	img := image.NewYCbCr(image.Rect(0, 0, ancho, alto), image.YCbCrSubsampleRatio422)
	for y := 0; y < alto; y++ {
		for x := 0; x < ancho; x += 2 {
			i := (y*ancho + x) * 2
			img.Y[img.YOffset(x, y)] = datos[i]
			img.Y[img.YOffset(x+1, y)] = datos[i+2]
			o := img.COffset(x, y)
			img.Cb[o] = datos[i+1]
			img.Cr[o] = datos[i+3]
		}
	}
	return img
}

// fourcc devuelve el código de formato como texto, para los registros.
func fourcc(v uint32) string {
	return string([]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)})
}
