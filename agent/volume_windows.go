//go:build windows
// +build windows

package agent

import (
	"math"
	"runtime"
	"syscall"
	"unsafe"

	ole "github.com/go-ole/go-ole"
)

// Feature 028 · Fase 2 — volumen de salida en Windows.
//
// `IAudioEndpointVolume` por COM directo desde Go, con el `go-ole` que el agente
// ya tiene como dependencia. Prey empaqueta dos ejecutables para esto
// (`unmuter.exe` + `voladjust.exe 100`); acá no hace falta ninguno, ni tampoco el
// shim en C# que se había supuesto necesario.
//
// ⚠️ CORRE EN LA SESIÓN 0, y eso es lo único de esta fase que NO está medido.
// El endpoint de audio de Windows es por sesión; la sospecha —y la razón por la
// que Prey empaqueta ejecutables— es que un proceso del servicio no lo alcance.
// Si la medición lo confirma, esto mismo se mueve a un modo nuevo del binario
// lanzado con el token interactivo (patrón `-m geoprobe`/`-m geolocation`), y el
// código COM de este archivo viaja tal cual: cambia el proceso anfitrión, no la
// implementación.
//
// 🚫 Lo que NO se puede hacer al mover eso: dejar que ese modo nuevo lance el
// audio como proceso aparte. Windows es inmune al bug del stop huérfano por
// TOPOLOGÍA —el sonido vive dentro del propio `powershell.exe`, sin nietos— y su
// matador es `KillProc`, que baja UN solo nivel y se rinde si el padre ya murió.
// macOS y Linux no dependen de eso desde `e192d3c`: `Setpgid` + `kill(-pgid)`
// alcanza el árbol completo a cualquier profundidad. O sea que Windows es el
// único de los tres donde agregar un nivel al árbol resucita el bug, y ahí sí
// haría falta un Job Object con `KILL_ON_JOB_CLOSE`. El invariante que lo evita:
// todo proceso de LARGA VIDA de la alarma debe ser hijo directo del agente y no
// engendrar nada él mismo.

var (
	clsidMMDeviceEnumerator = ole.NewGUID("{BCDE0395-E52F-467C-8E3D-C4579291692E}")
	iidIMMDeviceEnumerator  = ole.NewGUID("{A95664D2-9614-4F35-A746-DE8DB63617E6}")
	iidIAudioEndpointVolume = ole.NewGUID("{5CDF2C82-841E-4546-9722-0CF74078229A}")
)

const (
	// eRender / eConsole: el dispositivo de SALIDA por omisión para el rol de
	// consola, que es el que mueve el control de volumen de la bandeja.
	eRender  = 0
	eConsole = 0

	// clsctxAll es el CLSCTX_ALL del SDK de Windows (0x17). Se define acá porque
	// el `ole.CLSCTX_ALL` de go-ole vale 0x7 —le falta REMOTE_SERVER— y se usa
	// para `IMMDevice::Activate`, donde conviene el valor del SDK.
	clsctxAll = 0x17

	// RPC_E_CHANGED_MODE: alguien ya inicializó COM en este hilo con otro modelo
	// de apartamento. No es un fallo: se puede seguir usando COM, sólo que no nos
	// toca a nosotros desinicializarlo.
	rpcEChangedMode = 0x80010106
)

// --- vtables -------------------------------------------------------------

type immDeviceEnumeratorVtbl struct {
	ole.IUnknownVtbl
	EnumAudioEndpoints                     uintptr
	GetDefaultAudioEndpoint                uintptr
	GetDevice                              uintptr
	RegisterEndpointNotificationCallback   uintptr
	UnregisterEndpointNotificationCallback uintptr
}

type immDeviceVtbl struct {
	ole.IUnknownVtbl
	Activate          uintptr
	OpenPropertyStore uintptr
	GetId             uintptr
	GetState          uintptr
}

type audioEndpointVolumeVtbl struct {
	ole.IUnknownVtbl
	RegisterControlChangeNotify   uintptr
	UnregisterControlChangeNotify uintptr
	GetChannelCount               uintptr
	SetMasterVolumeLevel          uintptr
	SetMasterVolumeLevelScalar    uintptr
	GetMasterVolumeLevel          uintptr
	GetMasterVolumeLevelScalar    uintptr
	SetChannelVolumeLevel         uintptr
	SetChannelVolumeLevelScalar   uintptr
	GetChannelVolumeLevel         uintptr
	GetChannelVolumeLevelScalar   uintptr
	SetMute                       uintptr
	GetMute                       uintptr
	GetVolumeStepInfo             uintptr
	VolumeStepUp                  uintptr
	VolumeStepDown                uintptr
	QueryHardwareSupport          uintptr
	GetVolumeRange                uintptr
}

type immDeviceEnumerator struct{ ole.IUnknown }
type immDevice struct{ ole.IUnknown }
type audioEndpointVolume struct{ ole.IUnknown }

func (v *immDeviceEnumerator) vtbl() *immDeviceEnumeratorVtbl {
	return (*immDeviceEnumeratorVtbl)(unsafe.Pointer(v.RawVTable))
}

func (v *immDevice) vtbl() *immDeviceVtbl {
	return (*immDeviceVtbl)(unsafe.Pointer(v.RawVTable))
}

func (v *audioEndpointVolume) vtbl() *audioEndpointVolumeVtbl {
	return (*audioEndpointVolumeVtbl)(unsafe.Pointer(v.RawVTable))
}

// --- puente COM ----------------------------------------------------------

// withEndpointVolume abre el control de volumen del dispositivo de salida por
// omisión, se lo pasa a `fn` y lo cierra.
//
// `runtime.LockOSThread` no es opcional: COM es por hilo, y sin fijar el hilo
// el planificador de Go puede mover la goroutine a otro que no tiene el
// apartamento inicializado. Con la alarma reafirmando el volumen una vez por
// segundo, eso sería un fallo intermitente y dificilísimo de leer en un log.
func withEndpointVolume(fn func(*audioEndpointVolume) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Modelo multihilo: el agente es un servicio sin bomba de mensajes, así que
	// un apartamento STA no tendría quién despache sus llamadas.
	uninit := true
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		oleErr, ok := err.(*ole.OleError)
		if !ok || oleErr.Code() != rpcEChangedMode {
			return err
		}
		// Ya venía inicializado con otro modelo: se usa, pero no se cierra.
		uninit = false
	}
	if uninit {
		defer ole.CoUninitialize()
	}

	unk, err := ole.CreateInstance(clsidMMDeviceEnumerator, iidIMMDeviceEnumerator)
	if err != nil {
		return err
	}
	enumerator := (*immDeviceEnumerator)(unsafe.Pointer(unk))
	defer enumerator.Release()

	var device *immDevice
	hr, _, _ := syscall.SyscallN(enumerator.vtbl().GetDefaultAudioEndpoint,
		uintptr(unsafe.Pointer(enumerator)),
		uintptr(eRender),
		uintptr(eConsole),
		uintptr(unsafe.Pointer(&device)))
	if comFailed(hr) {
		return ole.NewError(hr)
	}
	defer device.Release()

	var endpoint *audioEndpointVolume
	hr, _, _ = syscall.SyscallN(device.vtbl().Activate,
		uintptr(unsafe.Pointer(device)),
		uintptr(unsafe.Pointer(iidIAudioEndpointVolume)),
		uintptr(clsctxAll),
		0,
		uintptr(unsafe.Pointer(&endpoint)))
	if comFailed(hr) {
		return ole.NewError(hr)
	}
	defer endpoint.Release()

	return fn(endpoint)
}

// --- primitivas ----------------------------------------------------------

func (a *Agent) readVolume() (volumeState, error) {
	var st volumeState

	err := withEndpointVolume(func(e *audioEndpointVolume) error {
		var level float32
		hr, _, _ := syscall.SyscallN(e.vtbl().GetMasterVolumeLevelScalar,
			uintptr(unsafe.Pointer(e)),
			uintptr(unsafe.Pointer(&level)))
		if comFailed(hr) {
			return ole.NewError(hr)
		}

		// BOOL de Windows: 4 bytes, no el bool de Go.
		var muted int32
		hr, _, _ = syscall.SyscallN(e.vtbl().GetMute,
			uintptr(unsafe.Pointer(e)),
			uintptr(unsafe.Pointer(&muted)))
		if comFailed(hr) {
			return ole.NewError(hr)
		}

		st = volumeState{
			Level:     clampLevel(float64(level)),
			Muted:     muted != 0,
			MuteKnown: true,
			Known:     true,
		}
		return nil
	})

	if err != nil {
		return volumeState{}, err
	}

	return st, nil
}

func (a *Agent) setVolume(v volumeState) error {
	return withEndpointVolume(func(e *audioEndpointVolume) error {
		// El nivel es un float32 y viaja por `math.Float32bits`. Funciona porque
		// el `asmstdcall` de Go duplica los primeros cuatro argumentos enteros en
		// XMM0-XMM3, que es donde la convención x64 de Windows espera los flotantes.
		hr, _, _ := syscall.SyscallN(e.vtbl().SetMasterVolumeLevelScalar,
			uintptr(unsafe.Pointer(e)),
			uintptr(math.Float32bits(float32(clampLevel(v.Level)))),
			0) // pguidEventContext = NULL
		if comFailed(hr) {
			return ole.NewError(hr)
		}

		if !v.MuteKnown {
			return nil
		}

		var muted int32
		if v.Muted {
			muted = 1
		}

		// ⚠️ `SetMute` devuelve **S_FALSE** —no S_OK— cuando el estado de
		// silencio ya era el pedido, que es el caso más común de todos: la
		// alarma manda `SetMute(false)` sobre un equipo que no está
		// silenciado. Medido en `LAPTOP-DSHQMP1O` el 2026-07-28. Por eso acá
		// se pregunta por `comFailed` y no por "distinto de cero": la
		// diferencia era un equipo que se quedaba al 100 % para siempre.
		hr, _, _ = syscall.SyscallN(e.vtbl().SetMute,
			uintptr(unsafe.Pointer(e)),
			uintptr(muted),
			0)
		if comFailed(hr) {
			return ole.NewError(hr)
		}

		return nil
	})
}
