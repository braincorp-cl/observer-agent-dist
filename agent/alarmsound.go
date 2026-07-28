package agent

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"runtime"
)

// Feature 028 · el sonido de la alarma.
//
// Hasta acá la alarma usaba **sólo** sonidos del propio sistema, y las listas de
// respaldo terminaban en cosas como `Windows Notify System Generic.wav` o
// `Ping.aiff`: dings de notificación de dos décimas de segundo. Para lo que esta
// feature existe —encontrar físicamente un equipo— un ding cada segundo, incluso al
// 100 % de volumen, no alcanza. Es la diferencia entre "el equipo hace un ruidito" y
// "el equipo delata dónde está".
//
// Prey lo resuelve empaquetando cuatro mp3 (`alarm`, `siren`, `ring`, `modem`) y
// dejando que el servidor elija. Nosotros no podemos copiar los suyos —son GPL— y
// salir a buscar un archivo de audio libre traería una pregunta de licencia que
// habría que sostener por cada equipo de la flota.
//
// Así que la sirena se **genera**: dos tonos alternados, escritos como WAV PCM por el
// propio agente. Sin archivo de terceros, sin licencia que revisar, sin engordar el
// binario, y el mismo sonido en los tres SO. WAV y no algo comprimido porque es el
// único formato que los tres reproductores manejan sin depender de códecs:
// `System.Media.SoundPlayer` en Windows (que además SÓLO acepta PCM), `afplay` en
// macOS y `paplay`/`pw-play`/`aplay` en Linux — `aplay` es ALSA puro y no habría
// tocado un .oga.
//
// Si algo falla, se sigue con los sonidos del sistema. Que no haya sirena no es razón
// para no hacer sonar un equipo que se está buscando.

const (
	// 22050 Hz alcanza de sobra para tonos de ~1 kHz y deja el archivo en la mitad
	// que a 44100. Mono por lo mismo: una sirena no tiene nada que estereofonizar.
	alarmWavSampleRate = 22050

	// Dos tonos alternados de 500 ms — el patrón de sirena de dos tonos, que se
	// reconoce como alarma y no como notificación. Las frecuencias están en la zona
	// donde el oído es más sensible y donde un parlante de notebook todavía rinde:
	// más abajo se pierde en el mueble, más arriba se vuelve chillido fino y molesto
	// de ubicar.
	alarmWavToneMillis = 500
	alarmWavLowHz      = 700
	alarmWavHighHz     = 1100

	// Dos vueltas ⇒ 2 s de archivo. El bucle del reproductor lo repite; el archivo
	// sólo tiene que ser lo bastante largo para que el corte no se note.
	alarmWavCycles = 2

	// 0,9 y no 1,0: al tope, cualquier redondeo satura y la saturación suena a
	// parlante roto, no a más volumen.
	alarmWavAmplitude = 0.9

	// Desvanecido en los extremos de cada tono. Sin él, el salto de amplitud en cada
	// cambio se oye como un chasquido —y un chasquido periódico suena a equipo con
	// falla, no a alarma.
	alarmWavFadeMillis = 5

	alarmSirenFileName = "alarm-siren.wav"
)

// buildAlarmSirenWav arma el WAV completo en memoria.
//
// Es pura y determinista a propósito: así se puede probar el archivo entero —cabecera
// y muestras— sin tocar el disco ni necesitar un dispositivo de audio.
func buildAlarmSirenWav() []byte {
	toneSamples := alarmWavSampleRate * alarmWavToneMillis / 1000
	fadeSamples := alarmWavSampleRate * alarmWavFadeMillis / 1000

	pcm := new(bytes.Buffer)
	pcm.Grow(toneSamples * 2 * alarmWavCycles * 2)

	for cycle := 0; cycle < alarmWavCycles; cycle++ {
		for _, freq := range [2]float64{alarmWavLowHz, alarmWavHighHz} {
			for i := 0; i < toneSamples; i++ {
				t := float64(i) / float64(alarmWavSampleRate)
				v := math.Sin(2 * math.Pi * freq * t)

				switch {
				case i < fadeSamples:
					v *= float64(i) / float64(fadeSamples)
				case i >= toneSamples-fadeSamples:
					v *= float64(toneSamples-i) / float64(fadeSamples)
				}

				_ = binary.Write(pcm, binary.LittleEndian,
					int16(v*alarmWavAmplitude*math.MaxInt16))
			}
		}
	}

	data := pcm.Bytes()

	out := new(bytes.Buffer)
	out.Grow(44 + len(data))
	put := func(v any) { _ = binary.Write(out, binary.LittleEndian, v) }

	out.WriteString("RIFF")
	put(uint32(36 + len(data))) // tamaño total menos los 8 bytes de "RIFF" + este campo
	out.WriteString("WAVE")

	out.WriteString("fmt ")
	put(uint32(16))                              // largo del bloque fmt para PCM
	put(uint16(1))                               // 1 = PCM sin comprimir
	put(uint16(1))                               // canales
	put(uint32(alarmWavSampleRate))              //
	put(uint32(alarmWavSampleRate * 1 * 16 / 8)) // bytes por segundo
	put(uint16(1 * 16 / 8))                      // alineación de bloque
	put(uint16(16))                              // bits por muestra

	out.WriteString("data")
	put(uint32(len(data)))
	out.Write(data)

	return out.Bytes()
}

func (a *Agent) alarmSirenPath() string {
	return filepath.Join(a.alarmDataDir(), alarmSirenFileName)
}

// ensureAlarmSiren deja la sirena escrita y devuelve su ruta. Devuelve "" cuando hay
// que caer a los sonidos del sistema, y el motivo queda en el log.
func (a *Agent) ensureAlarmSiren() string {
	path := a.alarmSirenPath()
	want := buildAlarmSirenWav()

	// Se reescribe si falta o si el tamaño no coincide. Lo segundo cubre dos casos con
	// una sola comprobación: un archivo truncado por un corte a mitad de escritura
	// —que dejaría un WAV inválido para siempre— y un cambio del generador en una
	// versión nueva del agente, que se aplica solo sin necesidad de migración.
	if st, err := os.Stat(path); err != nil || st.Size() != int64(len(want)) {
		if err := writeAlarmSiren(path, want); err != nil {
			a.Logger.Debugln("ensureAlarmSiren: no se pudo escribir la sirena:", err)
			return ""
		}
		a.Logger.Debugln("ensureAlarmSiren: sirena generada en", path)
	}

	if !alarmSirenReachableByUser(path) {
		a.Logger.Debugln("ensureAlarmSiren: el usuario del escritorio no podría leer",
			path, "— se usan los sonidos del sistema")
		return ""
	}

	return path
}

// writeAlarmSiren escribe el archivo de forma atómica.
//
// Temporal + rename por lo mismo que el archivo de estado: una escritura directa
// interrumpida deja un WAV a medias, y un WAV a medias no falla al reproducirse —
// suena cortado, o no suena y el reproductor sale con error mientras el operador ve
// `ok`.
func writeAlarmSiren(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}

	// Chmod explícito: el modo que se le pasa a WriteFile lo recorta la umask del
	// servicio, y con una umask 077 el archivo quedaría en 0600 — ilegible para la
	// persona, que es justamente quien lo reproduce.
	if err := os.Chmod(tmp, 0644); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	return nil
}

// alarmSirenReachableByUser comprueba que el usuario del escritorio pueda llegar al
// archivo.
//
// El reproductor NO corre como root: corre como la persona —`Credential` en Linux,
// `launchctl asuser` + `sudo -u` en macOS—. Un WAV perfecto dentro de un directorio
// que el usuario no puede atravesar da un reproductor que falla, y la alarma se
// quedaría sin sonar teniendo con qué.
//
// Deliberadamente NO se corrigen los permisos del directorio del agente: ahí viven el
// binario y la configuración, y aflojarle los permisos para poder hacer ruido es un
// intercambio que no me corresponde hacer solo. Si el usuario no llega, se renuncia a
// la sirena y se deja dicho.
//
// En Windows los bits de modo de Go no describen la ACL real y `%PROGRAMDATA%` es
// legible por los usuarios por diseño, así que no hay nada que comprobar.
func alarmSirenReachableByUser(path string) bool {
	if runtime.GOOS == "windows" {
		return true
	}

	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm()&0004 == 0 {
		return false
	}

	// Sólo el directorio que contiene el archivo. Si alguno de los padres tampoco
	// fuera atravesable el agente no habría podido instalarse ahí.
	di, err := os.Stat(filepath.Dir(path))
	if err != nil || di.Mode().Perm()&0001 == 0 {
		return false
	}

	return true
}
