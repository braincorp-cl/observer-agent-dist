//go:build darwin && cgo

package agent

// Salida de audio en macOS, por CoreAudio.
//
// 📌 La técnica de este archivo se **midió antes de escribirla**: se compiló el
// mismo recorrido en C con `clang` en el MacBook real (2026-07-28) y ahí se
// verificó que enumera, distingue el transporte y —lo que importa— que
// `AudioObjectSetPropertyData` sobre `kAudioHardwarePropertyDefaultOutputDevice`
// **cambia la salida y se puede devolver**: `107 (AirPods, transporte "blue")` →
// `93 (Bocinas, transporte "bltn")` → `107`, con `st=0` en las dos direcciones.
// Se transcribió recién después. Escribir cgo a ciegas y descubrirlo en la CI era
// el otro camino, y en esta feature ya salió caro deducir en vez de medir.
//
// El discriminador es `kAudioDeviceTransportTypeBuiltIn` ('bltn'), no el nombre:
// los nombres están traducidos ("Bocinas de MacBook Pro") y cambian con el idioma
// del sistema.

/*
#cgo LDFLAGS: -framework CoreAudio -framework CoreFoundation
#include <CoreAudio/CoreAudio.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>

// obsOutputName copia el nombre del dispositivo. Devuelve el OSStatus.
static int obsOutputName(AudioDeviceID d, char *buf, int n) {
    AudioObjectPropertyAddress a = {kAudioObjectPropertyName,
                                    kAudioObjectPropertyScopeGlobal,
                                    kAudioObjectPropertyElementMain};
    CFStringRef s = NULL;
    UInt32 sz = sizeof(s);
    OSStatus st = AudioObjectGetPropertyData(d, &a, 0, NULL, &sz, &s);
    if (st != noErr) return (int)st;
    if (!CFStringGetCString(s, buf, n, kCFStringEncodingUTF8)) buf[0] = 0;
    CFRelease(s);
    return 0;
}

// obsIsOutput: tiene al menos un canal en el scope de salida. Sin esto entran
// micrófonos y dispositivos virtuales de sólo entrada.
static int obsIsOutput(AudioDeviceID d) {
    AudioObjectPropertyAddress a = {kAudioDevicePropertyStreamConfiguration,
                                    kAudioObjectPropertyScopeOutput,
                                    kAudioObjectPropertyElementMain};
    UInt32 sz = 0;
    if (AudioObjectGetPropertyDataSize(d, &a, 0, NULL, &sz) != noErr || sz == 0) return 0;
    AudioBufferList *bl = (AudioBufferList *)malloc(sz);
    if (bl == NULL) return 0;
    int ch = 0;
    if (AudioObjectGetPropertyData(d, &a, 0, NULL, &sz, bl) == noErr) {
        for (UInt32 i = 0; i < bl->mNumberBuffers; i++) ch += bl->mBuffers[i].mNumberChannels;
    }
    free(bl);
    return ch > 0;
}

static UInt32 obsTransport(AudioDeviceID d) {
    AudioObjectPropertyAddress a = {kAudioDevicePropertyTransportType,
                                    kAudioObjectPropertyScopeGlobal,
                                    kAudioObjectPropertyElementMain};
    UInt32 t = 0, sz = sizeof(t);
    if (AudioObjectGetPropertyData(d, &a, 0, NULL, &sz, &t) != noErr) return 0;
    return t;
}

static int obsDefaultOutput(AudioDeviceID *out) {
    AudioObjectPropertyAddress a = {kAudioHardwarePropertyDefaultOutputDevice,
                                    kAudioObjectPropertyScopeGlobal,
                                    kAudioObjectPropertyElementMain};
    UInt32 sz = sizeof(*out);
    return (int)AudioObjectGetPropertyData(kAudioObjectSystemObject, &a, 0, NULL, &sz, out);
}

static int obsSetDefaultOutput(AudioDeviceID d) {
    AudioObjectPropertyAddress a = {kAudioHardwarePropertyDefaultOutputDevice,
                                    kAudioObjectPropertyScopeGlobal,
                                    kAudioObjectPropertyElementMain};
    return (int)AudioObjectSetPropertyData(kAudioObjectSystemObject, &a, 0, NULL, sizeof(d), &d);
}

// obsInternalOutput devuelve el primer dispositivo de SALIDA cuyo transporte sea
// el interno. Devuelve 0 si lo encontró, 1 si no hay ninguno, negativo si falló
// la enumeración.
static int obsInternalOutput(AudioDeviceID *out) {
    AudioObjectPropertyAddress a = {kAudioHardwarePropertyDevices,
                                    kAudioObjectPropertyScopeGlobal,
                                    kAudioObjectPropertyElementMain};
    UInt32 sz = 0;
    OSStatus st = AudioObjectGetPropertyDataSize(kAudioObjectSystemObject, &a, 0, NULL, &sz);
    if (st != noErr || sz == 0) return -1;

    AudioDeviceID *ids = (AudioDeviceID *)malloc(sz);
    if (ids == NULL) return -1;
    st = AudioObjectGetPropertyData(kAudioObjectSystemObject, &a, 0, NULL, &sz, ids);
    if (st != noErr) { free(ids); return -1; }

    int n = (int)(sz / sizeof(AudioDeviceID));
    int found = 1;
    for (int i = 0; i < n; i++) {
        if (!obsIsOutput(ids[i])) continue;
        if (obsTransport(ids[i]) == kAudioDeviceTransportTypeBuiltIn) {
            *out = ids[i];
            found = 0;
            break;
        }
    }
    free(ids);
    return found;
}
*/
import "C"

import (
	"fmt"
	"strconv"
)

// nombreDeSalida no es crítico: si falla, el dispositivo se identifica por su id.
// Un nombre vacío nunca debe impedir cambiar o restaurar la salida.
func nombreDeSalida(id C.AudioDeviceID) string {
	buf := make([]C.char, 256)
	if st := C.obsOutputName(id, &buf[0], C.int(len(buf))); st != 0 {
		return ""
	}
	return C.GoString(&buf[0])
}

func (a *Agent) defaultAudioOutput() (audioOutput, error) {
	var id C.AudioDeviceID
	if st := C.obsDefaultOutput(&id); st != 0 {
		return audioOutput{}, fmt.Errorf("kAudioHardwarePropertyDefaultOutputDevice: OSStatus %d", int(st))
	}

	return audioOutput{
		ID:    strconv.FormatUint(uint64(id), 10),
		Name:  nombreDeSalida(id),
		Known: true,
	}, nil
}

func (a *Agent) internalSpeakerOutput() (audioOutput, error) {
	var id C.AudioDeviceID
	switch st := C.obsInternalOutput(&id); {
	case st == 1:
		return audioOutput{}, fmt.Errorf("el equipo no tiene una salida de transporte interno")
	case st != 0:
		return audioOutput{}, fmt.Errorf("no se pudieron enumerar los dispositivos de audio (%d)", int(st))
	}

	return audioOutput{
		ID:    strconv.FormatUint(uint64(id), 10),
		Name:  nombreDeSalida(id),
		Known: true,
	}, nil
}

func (a *Agent) setDefaultAudioOutput(o audioOutput) error {
	id, err := strconv.ParseUint(o.ID, 10, 32)
	if err != nil {
		return fmt.Errorf("id de dispositivo ilegible %q: %w", o.ID, err)
	}

	if st := C.obsSetDefaultOutput(C.AudioDeviceID(id)); st != 0 {
		return fmt.Errorf("no se pudo fijar la salida %q: OSStatus %d", o.Name, int(st))
	}

	return nil
}
