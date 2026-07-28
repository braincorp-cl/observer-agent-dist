//go:build linux
// +build linux

package agent

import (
	"context"
	"os/exec"
	"time"
)

// Feature 028 · Fase 2 — volumen de salida en Linux.
//
// Cadena de candidatos y NO un comando fijo. Prey asume `pactl`
// (`lib/agent/actions/alarm/linux.js`) y en esta misma feature ya costó una
// medición descubrir que el escritorio Kali de prueba no traía ni `paplay`, sólo
// `pw-play`: dar por sentado el servidor de sonido es el hallazgo 3 de la 028
// repetido. El orden es `pactl` (PulseAudio y pipewire-pulse) → `wpctl`
// (PipeWire nativo, sin capa de compatibilidad) → `amixer` (ALSA a secas, el
// último recurso en equipos sin servidor de sonido).
//
// Todo corre como el usuario del escritorio con el entorno ya cosechado por
// `findUserSession` —que incluye `XDG_RUNTIME_DIR` y `PULSE_SERVER`—, porque el
// socket del servidor de sonido vive en el runtime dir de ese usuario y desde
// root no se alcanza.
//
// El parseo de cada salida vive en `volumeparse.go`, sin etiqueta de plataforma,
// para que se pueda probar.

const volumeCmdTimeout = 10 * time.Second

// linuxVolumeProviders se recorre en orden hasta que una operación funciona. No
// se "elige un proveedor" de una vez: que `pactl` exista no garantiza que
// responda —un `pactl` de PulseAudio anterior a la 15 no tiene `get-sink-volume`—
// así que lo que hace pasar al siguiente es el fallo real y no la mera ausencia
// del binario.
var linuxVolumeProviders = []struct {
	bin  string
	read func(a *Agent, bin string) (volumeState, error)
	set  func(a *Agent, bin string, v volumeState) error
}{
	{"pactl", pactlReadVolume, pactlSetVolume},
	{"wpctl", wpctlReadVolume, wpctlSetVolume},
	{"amixer", amixerReadVolume, amixerSetVolume},
}

func (a *Agent) readVolume() (volumeState, error) {
	var lastErr error = errVolumeUnsupported

	for _, p := range linuxVolumeProviders {
		bin, err := exec.LookPath(p.bin)
		if err != nil {
			continue
		}

		st, err := p.read(a, bin)
		if err == nil {
			a.Logger.Debugln("readVolume: leído con", p.bin)
			return st, nil
		}

		a.Logger.Debugln("readVolume:", p.bin, "falló:", err)
		lastErr = err
	}

	return volumeState{}, lastErr
}

func (a *Agent) setVolume(v volumeState) error {
	var lastErr error = errVolumeUnsupported

	for _, p := range linuxVolumeProviders {
		bin, err := exec.LookPath(p.bin)
		if err != nil {
			continue
		}

		if err := p.set(a, bin, v); err != nil {
			a.Logger.Debugln("setVolume:", p.bin, "falló:", err)
			lastErr = err
			continue
		}

		return nil
	}

	return lastErr
}

// runUserVolumeCmd ejecuta un comando corto en la sesión del usuario y devuelve
// su salida. Es de vida propia y termina solo, así que no agrega ningún nivel
// duradero al árbol de procesos de la alarma.
func (a *Agent) runUserVolumeCmd(bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), volumeCmdTimeout)
	defer cancel()

	cmd, err := a.newUserSessionCmd(ctx, bin, args...)
	if err != nil {
		return nil, err
	}

	return cmd.Output()
}

// --- pactl ---------------------------------------------------------------

func pactlReadVolume(a *Agent, bin string) (volumeState, error) {
	out, err := a.runUserVolumeCmd(bin, "get-sink-volume", "@DEFAULT_SINK@")
	if err != nil {
		return volumeState{}, err
	}

	level, err := parsePactlVolume(string(out))
	if err != nil {
		return volumeState{}, err
	}

	st := volumeState{Level: level, Known: true}

	// El silencio es una consulta aparte en pactl. Que falle no invalida el
	// nivel: se devuelve lo que sí se pudo leer y la restauración no tocará el
	// mute.
	if muteOut, err := a.runUserVolumeCmd(bin, "get-sink-mute", "@DEFAULT_SINK@"); err == nil {
		st.Muted, st.MuteKnown = parsePactlMute(string(muteOut)), true
	}

	return st, nil
}

func pactlSetVolume(a *Agent, bin string, v volumeState) error {
	if _, err := a.runUserVolumeCmd(bin, "set-sink-volume", "@DEFAULT_SINK@", percentArg(v.Level)); err != nil {
		return err
	}

	if !v.MuteKnown {
		return nil
	}

	_, err := a.runUserVolumeCmd(bin, "set-sink-mute", "@DEFAULT_SINK@", boolArg(v.Muted))
	return err
}

// --- wpctl ---------------------------------------------------------------

func wpctlReadVolume(a *Agent, bin string) (volumeState, error) {
	out, err := a.runUserVolumeCmd(bin, "get-volume", "@DEFAULT_AUDIO_SINK@")
	if err != nil {
		return volumeState{}, err
	}

	return parseWpctlVolume(string(out))
}

func wpctlSetVolume(a *Agent, bin string, v volumeState) error {
	if _, err := a.runUserVolumeCmd(bin, "set-volume", "@DEFAULT_AUDIO_SINK@", percentArg(v.Level)); err != nil {
		return err
	}

	if !v.MuteKnown {
		return nil
	}

	_, err := a.runUserVolumeCmd(bin, "set-mute", "@DEFAULT_AUDIO_SINK@", boolArg(v.Muted))
	return err
}

// --- amixer --------------------------------------------------------------

func amixerReadVolume(a *Agent, bin string) (volumeState, error) {
	// -M pide la escala mapeada (percibida), que es la que muestran los
	// escritorios. Sin ella amixer informa en escala lineal de amplitud y un
	// "50 %" leído y reescrito no suena igual que el original.
	out, err := a.runUserVolumeCmd(bin, "-M", "get", "Master")
	if err != nil {
		return volumeState{}, err
	}

	return parseAmixerVolume(string(out))
}

func amixerSetVolume(a *Agent, bin string, v volumeState) error {
	args := []string{"-M", "-q", "sset", "Master", percentArg(v.Level)}

	if v.MuteKnown {
		if v.Muted {
			args = append(args, "mute")
		} else {
			args = append(args, "unmute")
		}
	}

	_, err := a.runUserVolumeCmd(bin, args...)
	return err
}
