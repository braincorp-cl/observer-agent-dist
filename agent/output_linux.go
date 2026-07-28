//go:build linux

package agent

import (
	"errors"
	"fmt"
	"strconv"
)

// Salida de audio en Linux.
//
// Misma cadena de candidatos que el volumen y por el mismo motivo: `pactl` no
// está garantizado. En `FAZOCAR` (Ubuntu/LXQt) **no existe** y sólo hay `wpctl` +
// `pw-dump`, que es PipeWire nativo — asumir PulseAudio repetiría el hallazgo 3
// de esta feature.
//
// El identificador que se guarda es el `node.name`, no el id numérico de
// PipeWire: los ids **no son estables** —unos audífonos que se reconectan reciben
// uno nuevo— y el estado de la alarma sobrevive al reinicio. El id se resuelve
// recién al momento de fijar la salida.

var errSalidaNoSoportada = errors.New("no hay con qué mover la salida de audio")

// listarSalidas devuelve los sumideros con el primer proveedor que responda.
func (a *Agent) listarSalidas() ([]sinkPipeWire, error) {
	if out, err := a.runUserVolumeCmd("pw-dump"); err == nil {
		if sinks, err := parsePwDumpSinks(out); err == nil && len(sinks) > 0 {
			return sinks, nil
		}
	}

	if out, err := a.runUserVolumeCmd("pactl", "list", "short", "sinks"); err == nil {
		if sinks := parsePactlSinks(string(out)); len(sinks) > 0 {
			return sinks, nil
		}
	}

	return nil, errSalidaNoSoportada
}

func (a *Agent) defaultAudioOutput() (audioOutput, error) {
	// wpctl primero: es el único que existe en un PipeWire sin capa de
	// compatibilidad con PulseAudio.
	if out, err := a.runUserVolumeCmd("wpctl", "inspect", "@DEFAULT_AUDIO_SINK@"); err == nil {
		if nombre := parseWpctlNodeName(string(out)); nombre != "" {
			return audioOutput{ID: nombre, Name: nombre, Known: true}, nil
		}
	}

	if out, err := a.runUserVolumeCmd("pactl", "get-default-sink"); err == nil {
		if nombre := primeraLinea(string(out)); nombre != "" {
			return audioOutput{ID: nombre, Name: nombre, Known: true}, nil
		}
	}

	return audioOutput{}, errSalidaNoSoportada
}

func (a *Agent) internalSpeakerOutput() (audioOutput, error) {
	sinks, err := a.listarSalidas()
	if err != nil {
		return audioOutput{}, err
	}

	elegido, ok := elegirSalidaInterna(sinks)
	if !ok {
		return audioOutput{}, fmt.Errorf("ninguno de los %d sumideros parece un parlante interno", len(sinks))
	}

	nombre := elegido.Description
	if nombre == "" {
		nombre = elegido.NodeName
	}

	return audioOutput{ID: elegido.NodeName, Name: nombre, Known: true}, nil
}

func (a *Agent) setDefaultAudioOutput(o audioOutput) error {
	// pactl acepta el nombre directamente, así que no hay que resolver nada.
	if _, err := a.runUserVolumeCmd("pactl", "set-default-sink", o.ID); err == nil {
		return nil
	}

	// wpctl exige el id numérico, y por eso se resuelve acá y no antes: entre que
	// se guardó el nombre y este momento pudo pasar un reinicio.
	sinks, err := a.listarSalidas()
	if err != nil {
		return err
	}

	for _, s := range sinks {
		if s.NodeName != o.ID {
			continue
		}
		if _, err := a.runUserVolumeCmd("wpctl", "set-default", strconv.Itoa(s.ID)); err != nil {
			return fmt.Errorf("wpctl set-default %d (%s): %w", s.ID, o.ID, err)
		}
		return nil
	}

	return fmt.Errorf("el dispositivo %q ya no está en la lista de salidas", o.ID)
}

func primeraLinea(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' || s[i] == '\r' {
			return s[:i]
		}
	}
	return s
}
