//go:build windows
// +build windows

package agent

import (
	"fmt"
	"image"
	"syscall"
	"unsafe"

	ole "github.com/go-ole/go-ole"
)

// Feature 030 · Fase 2 · la maquinaria de Media Foundation para la foto de
// webcam en Windows (T014).
//
// POR QUÉ MEDIA FOUNDATION Y NO UNA HERRAMIENTA EXTERNA: en Linux la foto la
// saca `fswebcam` o `ffmpeg`, y el 2026-08-13 se midió lo que eso cuesta —un
// equipo de cliente real no traía ninguna de las dos y su red bloqueaba los
// repositorios, así que la feature entera se reducía a `sin_herramienta`. Media
// Foundation viene con Windows desde Vista: no hay nada que instalar y no hay
// nada que la red pueda bloquear.
//
// El precio es este archivo: COM crudo por vtable, sin biblioteca que lo
// envuelva. Se sigue el mismo patrón que `volume_windows.go` (la 028), que ya
// hace esto para el control de volumen — mismas convenciones, mismos nombres,
// misma forma de declarar las tablas.

var (
	modMfplat      = syscall.NewLazyDLL("mfplat.dll")
	modMf          = syscall.NewLazyDLL("mf.dll")
	modMfreadwrite = syscall.NewLazyDLL("mfreadwrite.dll")

	procMFStartup                        = modMfplat.NewProc("MFStartup")
	procMFShutdown                       = modMfplat.NewProc("MFShutdown")
	procMFCreateAttributes               = modMfplat.NewProc("MFCreateAttributes")
	procMFCreateMediaType                = modMfplat.NewProc("MFCreateMediaType")
	procMFEnumDeviceSources              = modMf.NewProc("MFEnumDeviceSources")
	procMFCreateSourceReaderFromMediaSrc = modMfreadwrite.NewProc("MFCreateSourceReaderFromMediaSource")
)

// GUIDs de Media Foundation. Están escritos a mano porque no hay paquete Go que
// los traiga; cada uno lleva el nombre con el que aparece en la documentación de
// Microsoft para poder buscarlo.
var (
	mfDevsourceAttributeSourceType       = ole.NewGUID("{C60AC5FE-252A-478F-A0EF-BC8FA5F7CAD3}")
	mfDevsourceAttributeSourceTypeVidcap = ole.NewGUID("{8AC3587A-4AE7-42D8-99E0-0A6013EEF90F}")
	iidIMFMediaSource                    = ole.NewGUID("{279A808D-AEC7-40C8-9C6B-A6B492C78A66}")
	mfMTMajorType                        = ole.NewGUID("{48EBA18E-F8C9-4687-BF11-0A74C9F96A8F}")
	mfMTSubtype                          = ole.NewGUID("{F7E34C9A-42E8-4714-B74B-CB29D72C35E5}")
	mfMediaTypeVideo                     = ole.NewGUID("{73646976-0000-0010-8000-00AA00389B71}")
	mfVideoFormatRGB32                   = ole.NewGUID("{00000016-0000-0010-8000-00AA00389B71}")
	mfMTFrameSize                        = ole.NewGUID("{1652C33D-D6B2-4012-B834-72030849A37D}")
	mfMTDefaultStride                    = ole.NewGUID("{644B4E48-1E02-4516-B0EB-C01CA9D49AC6}")
	// Sin este atributo el SourceReader no inserta el conversor de formato y
	// pedirle RGB32 a una cámara que entrega NV12 o MJPG —o sea, casi todas—
	// falla con MF_E_INVALIDMEDIATYPE.
	mfSourceReaderEnableVideoProcessing = ole.NewGUID("{0F81DA2C-B537-4672-A8B2-A681B17307A3}")
)

const (
	mfVersion                        = 0x00020070
	mfstartupLite                    = 1
	firstVideoStream                 = 0xFFFFFFFC
	mfSourceReaderMediaTypeStreamOff = 0
)

// Códigos de error de Media Foundation que hay que distinguir del resto: son la
// diferencia entre "la cámara está ocupada" y "esto no funciona", que en la
// línea de tiempo de un caso son dos cosas muy distintas.
const (
	mfEVideoRecordingDeviceInvalidated = 0xC00D3EA2 // el dispositivo se fue
	mfEVideoRecordingDeviceLocked      = 0xC00D3EA3 // otra aplicación la tiene
	eAccessDenied                      = 0x80070005
	hrErrorBusy                        = 0x800700AA // ERROR_BUSY
)

type mfAttributesVtbl struct {
	ole.IUnknownVtbl
	GetItem            uintptr
	GetItemType        uintptr
	CompareItem        uintptr
	Compare            uintptr
	GetUINT32          uintptr
	GetUINT64          uintptr
	GetDouble          uintptr
	GetGUID            uintptr
	GetStringLength    uintptr
	GetString          uintptr
	GetAllocatedString uintptr
	GetBlobSize        uintptr
	GetBlob            uintptr
	GetAllocatedBlob   uintptr
	GetUnknown         uintptr
	SetItem            uintptr
	DeleteItem         uintptr
	DeleteAllItems     uintptr
	SetUINT32          uintptr
	SetUINT64          uintptr
	SetDouble          uintptr
	SetGUID            uintptr
	SetString          uintptr
	SetBlob            uintptr
	SetUnknown         uintptr
	LockStore          uintptr
	UnlockStore        uintptr
	GetCount           uintptr
	GetItemByIndex     uintptr
	CopyAllItems       uintptr
}

// mfActivateVtbl: IMFActivate hereda de IMFAttributes y agrega tres métodos.
type mfActivateVtbl struct {
	mfAttributesVtbl
	ActivateObject uintptr
	ShutdownObject uintptr
	DetachObject   uintptr
}

type mfSourceReaderVtbl struct {
	ole.IUnknownVtbl
	GetStreamSelection       uintptr
	SetStreamSelection       uintptr
	GetNativeMediaType       uintptr
	GetCurrentMediaType      uintptr
	SetCurrentMediaType      uintptr
	SetCurrentPosition       uintptr
	ReadSample               uintptr
	Flush                    uintptr
	GetServiceForStream      uintptr
	GetPresentationAttribute uintptr
}

// mfSampleVtbl: IMFSample también hereda de IMFAttributes.
type mfSampleVtbl struct {
	mfAttributesVtbl
	GetSampleFlags            uintptr
	SetSampleFlags            uintptr
	GetSampleTime             uintptr
	SetSampleTime             uintptr
	GetSampleDuration         uintptr
	SetSampleDuration         uintptr
	GetBufferCount            uintptr
	GetBufferByIndex          uintptr
	ConvertToContiguousBuffer uintptr
	AddBuffer                 uintptr
	RemoveBufferByIndex       uintptr
	RemoveAllBuffers          uintptr
	GetTotalLength            uintptr
	CopyToBuffer              uintptr
}

type mfMediaBufferVtbl struct {
	ole.IUnknownVtbl
	Lock             uintptr
	Unlock           uintptr
	GetCurrentLength uintptr
	SetCurrentLength uintptr
	GetMaxLength     uintptr
}

type comObj struct {
	vtbl *uintptr
}

func (o *comObj) attrs() *mfAttributesVtbl    { return (*mfAttributesVtbl)(unsafe.Pointer(o.vtbl)) }
func (o *comObj) activate() *mfActivateVtbl   { return (*mfActivateVtbl)(unsafe.Pointer(o.vtbl)) }
func (o *comObj) reader() *mfSourceReaderVtbl { return (*mfSourceReaderVtbl)(unsafe.Pointer(o.vtbl)) }
func (o *comObj) sample() *mfSampleVtbl       { return (*mfSampleVtbl)(unsafe.Pointer(o.vtbl)) }
func (o *comObj) buffer() *mfMediaBufferVtbl  { return (*mfMediaBufferVtbl)(unsafe.Pointer(o.vtbl)) }
func (o *comObj) unknown() *ole.IUnknownVtbl  { return (*ole.IUnknownVtbl)(unsafe.Pointer(o.vtbl)) }

func (o *comObj) release() {
	if o != nil && o.vtbl != nil {
		syscall.SyscallN(o.unknown().Release, uintptr(unsafe.Pointer(o)))
	}
}

// mfError traduce un HRESULT a un motivo del catálogo de la feature.
//
// Traducir acá y no en el llamador es deliberado: los códigos de Media
// Foundation son la única fuente que distingue "otra aplicación tiene la
// cámara" —que es información del caso, alguien está en una videollamada— de un
// fallo de verdad.
func mfError(hr uintptr) string {
	switch uint32(hr) {
	case mfEVideoRecordingDeviceLocked, hrErrorBusy:
		return webcamBusy
	case eAccessDenied:
		return webcamDenied
	case mfEVideoRecordingDeviceInvalidated:
		return webcamNoDevice
	}
	return webcamFailed
}

func hrOK(hr uintptr) bool { return int32(hr) >= 0 }

// capturarConMediaFoundation saca una foto con la primera cámara que responda.
//
// Devuelve la imagen ya decodificada —no un archivo— para que la capa común
// decida qué hacer con ella: así la verificación de imagen en negro y el guardado
// viven en un solo lugar para las tres plataformas.
func capturarConMediaFoundation(descartar int) (image.Image, string, error) {
	if hr, _, _ := procMFStartup.Call(mfVersion, mfstartupLite); !hrOK(hr) {
		return nil, webcamFailed, fmt.Errorf("MFStartup: 0x%X", hr)
	}
	defer procMFShutdown.Call()

	var atributos *comObj
	if hr, _, _ := procMFCreateAttributes.Call(uintptr(unsafe.Pointer(&atributos)), 1); !hrOK(hr) {
		return nil, webcamFailed, fmt.Errorf("MFCreateAttributes: 0x%X", hr)
	}
	defer atributos.release()

	hr, _, _ := syscall.SyscallN(atributos.attrs().SetGUID,
		uintptr(unsafe.Pointer(atributos)),
		uintptr(unsafe.Pointer(mfDevsourceAttributeSourceType)),
		uintptr(unsafe.Pointer(mfDevsourceAttributeSourceTypeVidcap)))
	if !hrOK(hr) {
		return nil, webcamFailed, fmt.Errorf("SetGUID(SOURCE_TYPE): 0x%X", hr)
	}

	var dispositivos **comObj
	var cuantos uint32
	if hr, _, _ := procMFEnumDeviceSources.Call(
		uintptr(unsafe.Pointer(atributos)),
		uintptr(unsafe.Pointer(&dispositivos)),
		uintptr(unsafe.Pointer(&cuantos))); !hrOK(hr) {
		return nil, webcamFailed, fmt.Errorf("MFEnumDeviceSources: 0x%X", hr)
	}
	if cuantos == 0 {
		return nil, webcamNoDevice, nil
	}
	defer coTaskMemFree(uintptr(unsafe.Pointer(dispositivos)))

	lista := unsafe.Slice(dispositivos, cuantos)

	// Se itera por la misma razón que en Linux: un equipo puede publicar más de
	// un dispositivo de captura —una webcam integrada más una virtual de
	// Teams/OBS, o una cámara infrarroja de Windows Hello junto a la normal— y el
	// primero de la lista no siempre es el que da imagen. La infrarroja es el
	// caso interesante: existe, abre y entrega cuadros, sólo que son negros para
	// un ojo humano.
	motivo := webcamFailed
	for i := range lista {
		dev := lista[i]
		img, m, err := capturarDeDispositivo(dev, descartar)
		dev.release()
		if err == nil && img != nil {
			// Se liberan los que queden por recorrer: MFEnumDeviceSources deja
			// una referencia por dispositivo y salir temprano las filtraría.
			for j := i + 1; j < len(lista); j++ {
				lista[j].release()
			}
			return img, "", nil
		}
		if m != "" && m != webcamFailed {
			motivo = m
		}
	}
	return nil, motivo, nil
}

func capturarDeDispositivo(dev *comObj, descartar int) (image.Image, string, error) {
	var fuente *comObj
	hr, _, _ := syscall.SyscallN(dev.activate().ActivateObject,
		uintptr(unsafe.Pointer(dev)),
		uintptr(unsafe.Pointer(iidIMFMediaSource)),
		uintptr(unsafe.Pointer(&fuente)))
	if !hrOK(hr) {
		return nil, mfError(hr), fmt.Errorf("ActivateObject: 0x%X", hr)
	}
	defer func() {
		syscall.SyscallN(dev.activate().ShutdownObject, uintptr(unsafe.Pointer(dev)))
		fuente.release()
	}()

	// Atributos del reader: hay que encender el procesamiento de video ANTES de
	// crearlo, si no la conversión a RGB32 no está disponible.
	var attrsReader *comObj
	if hr, _, _ := procMFCreateAttributes.Call(uintptr(unsafe.Pointer(&attrsReader)), 1); !hrOK(hr) {
		return nil, webcamFailed, fmt.Errorf("MFCreateAttributes(reader): 0x%X", hr)
	}
	defer attrsReader.release()
	syscall.SyscallN(attrsReader.attrs().SetUINT32,
		uintptr(unsafe.Pointer(attrsReader)),
		uintptr(unsafe.Pointer(mfSourceReaderEnableVideoProcessing)),
		1)

	var lector *comObj
	hr, _, _ = procMFCreateSourceReaderFromMediaSrc.Call(
		uintptr(unsafe.Pointer(fuente)),
		uintptr(unsafe.Pointer(attrsReader)),
		uintptr(unsafe.Pointer(&lector)))
	if !hrOK(hr) {
		return nil, mfError(hr), fmt.Errorf("MFCreateSourceReaderFromMediaSource: 0x%X", hr)
	}
	defer lector.release()

	if motivo, err := pedirRGB32(lector); err != nil {
		return nil, motivo, err
	}

	ancho, alto, stride, err := geometriaActual(lector)
	if err != nil {
		return nil, webcamFailed, err
	}

	// El calentamiento: se leen y se tiran N cuadros. Es el mismo motivo que en
	// Linux y macOS —la exposición automática arranca sin converger y los
	// primeros cuadros salen negros—, sólo que acá se ve más claro porque cada
	// cuadro es una llamada explícita.
	var ultimo *comObj
	for i := 0; i <= descartar; i++ {
		if ultimo != nil {
			ultimo.release()
			ultimo = nil
		}
		muestra, motivo, err := leerCuadro(lector)
		if err != nil {
			return nil, motivo, err
		}
		ultimo = muestra
	}
	if ultimo == nil {
		return nil, webcamFailed, fmt.Errorf("la cámara no entregó ningún cuadro")
	}
	defer ultimo.release()

	return imagenDeMuestra(ultimo, ancho, alto, stride)
}

func pedirRGB32(lector *comObj) (string, error) {
	var tipo *comObj
	if hr, _, _ := procMFCreateMediaType.Call(uintptr(unsafe.Pointer(&tipo))); !hrOK(hr) {
		return webcamFailed, fmt.Errorf("MFCreateMediaType: 0x%X", hr)
	}
	defer tipo.release()

	if hr, _, _ := syscall.SyscallN(tipo.attrs().SetGUID,
		uintptr(unsafe.Pointer(tipo)),
		uintptr(unsafe.Pointer(mfMTMajorType)),
		uintptr(unsafe.Pointer(mfMediaTypeVideo))); !hrOK(hr) {
		return webcamFailed, fmt.Errorf("SetGUID(MAJOR_TYPE): 0x%X", hr)
	}
	if hr, _, _ := syscall.SyscallN(tipo.attrs().SetGUID,
		uintptr(unsafe.Pointer(tipo)),
		uintptr(unsafe.Pointer(mfMTSubtype)),
		uintptr(unsafe.Pointer(mfVideoFormatRGB32))); !hrOK(hr) {
		return webcamFailed, fmt.Errorf("SetGUID(SUBTYPE): 0x%X", hr)
	}

	hr, _, _ := syscall.SyscallN(lector.reader().SetCurrentMediaType,
		uintptr(unsafe.Pointer(lector)),
		uintptr(firstVideoStream),
		0,
		uintptr(unsafe.Pointer(tipo)))
	if !hrOK(hr) {
		return mfError(hr), fmt.Errorf("SetCurrentMediaType(RGB32): 0x%X", hr)
	}
	return "", nil
}

// geometriaActual lee el tamaño y el stride del formato ya negociado.
//
// El stride puede venir NEGATIVO y eso no es un error: es como Windows dice que
// la imagen está guardada de abajo hacia arriba, la convención heredada de los
// mapas de bits de GDI. Ignorarlo entrega la foto dada vuelta — que es
// exactamente el tipo de fallo que pasa una revisión de código y no pasa una
// mirada a la evidencia.
func geometriaActual(lector *comObj) (int, int, int, error) {
	var tipo *comObj
	hr, _, _ := syscall.SyscallN(lector.reader().GetCurrentMediaType,
		uintptr(unsafe.Pointer(lector)),
		uintptr(firstVideoStream),
		uintptr(unsafe.Pointer(&tipo)))
	if !hrOK(hr) {
		return 0, 0, 0, fmt.Errorf("GetCurrentMediaType: 0x%X", hr)
	}
	defer tipo.release()

	var tamano uint64
	hr, _, _ = syscall.SyscallN(tipo.attrs().GetUINT64,
		uintptr(unsafe.Pointer(tipo)),
		uintptr(unsafe.Pointer(mfMTFrameSize)),
		uintptr(unsafe.Pointer(&tamano)))
	if !hrOK(hr) {
		return 0, 0, 0, fmt.Errorf("GetUINT64(FRAME_SIZE): 0x%X", hr)
	}
	ancho := int(tamano >> 32)
	alto := int(tamano & 0xFFFFFFFF)
	if ancho <= 0 || alto <= 0 {
		return 0, 0, 0, fmt.Errorf("geometría inválida: %dx%d", ancho, alto)
	}

	stride := ancho * 4
	var declarado uint32
	if hr, _, _ := syscall.SyscallN(tipo.attrs().GetUINT32,
		uintptr(unsafe.Pointer(tipo)),
		uintptr(unsafe.Pointer(mfMTDefaultStride)),
		uintptr(unsafe.Pointer(&declarado))); hrOK(hr) && declarado != 0 {
		stride = int(int32(declarado))
	}
	return ancho, alto, stride, nil
}

func leerCuadro(lector *comObj) (*comObj, string, error) {
	var flujo, banderas uint32
	var tiempo int64
	var muestra *comObj

	hr, _, _ := syscall.SyscallN(lector.reader().ReadSample,
		uintptr(unsafe.Pointer(lector)),
		uintptr(firstVideoStream),
		0,
		uintptr(unsafe.Pointer(&flujo)),
		uintptr(unsafe.Pointer(&banderas)),
		uintptr(unsafe.Pointer(&tiempo)),
		uintptr(unsafe.Pointer(&muestra)))
	if !hrOK(hr) {
		return nil, mfError(hr), fmt.Errorf("ReadSample: 0x%X", hr)
	}
	// Un ReadSample puede volver con éxito y SIN muestra: pasa mientras la cámara
	// arranca. No es un error y no cuenta como cuadro descartado.
	if muestra == nil {
		return nil, "", nil
	}
	return muestra, "", nil
}

// imagenDeMuestra copia los píxeles del buffer de Media Foundation a una imagen
// de Go, corrigiendo el orden de los canales y el sentido de las filas.
func imagenDeMuestra(muestra *comObj, ancho, alto, stride int) (image.Image, string, error) {
	var buf *comObj
	hr, _, _ := syscall.SyscallN(muestra.sample().ConvertToContiguousBuffer,
		uintptr(unsafe.Pointer(muestra)),
		uintptr(unsafe.Pointer(&buf)))
	if !hrOK(hr) {
		return nil, webcamFailed, fmt.Errorf("ConvertToContiguousBuffer: 0x%X", hr)
	}
	defer buf.release()

	var datos *byte
	var maxLen, largo uint32
	hr, _, _ = syscall.SyscallN(buf.buffer().Lock,
		uintptr(unsafe.Pointer(buf)),
		uintptr(unsafe.Pointer(&datos)),
		uintptr(unsafe.Pointer(&maxLen)),
		uintptr(unsafe.Pointer(&largo)))
	if !hrOK(hr) {
		return nil, webcamFailed, fmt.Errorf("Lock: 0x%X", hr)
	}
	defer syscall.SyscallN(buf.buffer().Unlock, uintptr(unsafe.Pointer(buf)))

	if largo == 0 || datos == nil {
		return nil, webcamFailed, fmt.Errorf("el buffer de la cámara vino vacío")
	}

	crudo := unsafe.Slice(datos, largo)
	img := image.NewRGBA(image.Rect(0, 0, ancho, alto))

	abajoArriba := stride < 0
	paso := stride
	if paso < 0 {
		paso = -paso
	}

	for y := 0; y < alto; y++ {
		origen := y
		if abajoArriba {
			origen = alto - 1 - y
		}
		inicio := origen * paso
		if inicio+ancho*4 > len(crudo) {
			break
		}
		fila := crudo[inicio : inicio+ancho*4]
		for x := 0; x < ancho; x++ {
			// RGB32 de Media Foundation es BGRA en memoria; Go espera RGBA.
			b := fila[x*4+0]
			g := fila[x*4+1]
			r := fila[x*4+2]
			p := img.PixOffset(x, y)
			img.Pix[p+0] = r
			img.Pix[p+1] = g
			img.Pix[p+2] = b
			img.Pix[p+3] = 0xFF
		}
	}
	return img, "", nil
}

var procCoTaskMemFree = syscall.NewLazyDLL("ole32.dll").NewProc("CoTaskMemFree")

func coTaskMemFree(p uintptr) {
	if p != 0 {
		procCoTaskMemFree.Call(p)
	}
}
