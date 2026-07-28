package agent

import (
	"io"
	"testing"

	"github.com/sirupsen/logrus"
)

// loggerMudo evita que los mensajes de las pruebas ensucien la salida.
func loggerMudo() *logrus.Logger {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return l
}

// Los datos de estos tests son REALES: salieron de `FAZOCAR` (Ubuntu/LXQt/PipeWire)
// el 2026-07-28 y del MacBook con AirPods conectados. Inventar la forma de la
// salida de una herramienta es lo que ya costó dos defectos en esta feature.

func TestEsSalidaInterna(t *testing.T) {
	casos := []struct {
		nodeName string
		interna  bool
		porque   string
	}{
		// El sumidero real de FAZOCAR, tal cual lo reporta pw-dump.
		{"alsa_output.pci-0000_00_1f.3.analog-stereo", true, "tarjeta integrada por PCI"},
		{"alsa_output.platform-bcm2835_audio.stereo-fallback", true, "codec integrado en SoC (ARM)"},

		// Los audífonos que motivan toda esta función.
		{"bluez_output.AC_12_2F_00_11_22.1", false, "Bluetooth"},
		{"bluez_sink.00_1A_7D_DA_71_13.a2dp_sink", false, "Bluetooth, nombre viejo de PulseAudio"},
		{"alsa_output.usb-Logitech_USB_Headset-00.analog-stereo", false, "audífono USB"},

		// HDMI es PCI y aun así NO sirve: hace sonar el monitor o el televisor
		// enchufado, no el equipo que estamos buscando.
		{"alsa_output.pci-0000_01_00.1.hdmi-stereo", false, "sale por el cable de video"},
		{"alsa_output.pci-0000_00_1f.3.hdmi-stereo-extra1", false, "HDMI aunque sea PCI"},
		{"alsa_output.pci-0000_00_03.0.displayport-stereo", false, "DisplayPort, mismo caso"},

		{"alsa_output.pci-0000_00_1f.3.iec958-stereo", false, "salida digital a un amplificador externo"},

		{"", false, "vacío"},
	}

	for _, c := range casos {
		t.Run(c.nodeName+" ("+c.porque+")", func(t *testing.T) {
			if got := esSalidaInterna(c.nodeName); got != c.interna {
				t.Errorf("esSalidaInterna(%q) = %v, se esperaba %v — %s", c.nodeName, got, c.interna, c.porque)
			}
		})
	}
}

func TestElegirSalidaInterna(t *testing.T) {
	// ⚠️ La analógica va DESPUÉS de otra interna a propósito: con las dos internas
	// en la lista, quitar la preferencia devuelve la primera y el caso falla. Una
	// versión anterior de este test ponía la analógica como única interna, así que
	// pasaba en verde con la preferencia desconectada — lo destapó la mutación.
	t.Run("prefiere la analógica sobre otra interna", func(t *testing.T) {
		sinks := []sinkPipeWire{
			{ID: 40, NodeName: "bluez_output.AC_12_2F_00_11_22.1"},
			{ID: 44, NodeName: "alsa_output.pci-0000_00_1f.3.hdmi-stereo"},
			{ID: 47, NodeName: "alsa_output.pci-0000_00_1f.3.stereo-fallback"},
			{ID: 51, NodeName: "alsa_output.pci-0000_00_1f.3.analog-stereo"},
		}
		got, ok := elegirSalidaInterna(sinks)
		if !ok || got.ID != 51 {
			t.Errorf("eligió %+v (ok=%v), se esperaba la analógica id=51", got, ok)
		}
	})

	t.Run("sin ninguna interna no elige nada", func(t *testing.T) {
		sinks := []sinkPipeWire{
			{ID: 40, NodeName: "bluez_output.AC_12_2F_00_11_22.1"},
			{ID: 44, NodeName: "alsa_output.pci-0000_00_1f.3.hdmi-stereo"},
		}
		if got, ok := elegirSalidaInterna(sinks); ok {
			t.Errorf("no debía elegir nada y eligió %+v", got)
		}
	})

	t.Run("lista vacía", func(t *testing.T) {
		if _, ok := elegirSalidaInterna(nil); ok {
			t.Error("no debía elegir nada")
		}
	})
}

// El JSON es el que devolvió pw-dump en FAZOCAR, con un nodo que NO es un
// sumidero para comprobar que el filtro por media.class hace su trabajo: el
// volcado real trae el grafo entero de PipeWire.
func TestParsePwDumpSinks(t *testing.T) {
	crudo := []byte(`[
	  {"id": 0, "type": "PipeWire:Interface:Core", "info": {"props": {"core.name": "pipewire-0"}}},
	  {"id": 51, "info": {"props": {"media.class": "Audio/Sink",
	    "node.name": "alsa_output.pci-0000_00_1f.3.analog-stereo",
	    "node.description": "Built-in Audio Analog Stereo"}}},
	  {"id": 62, "info": {"props": {"media.class": "Stream/Output/Audio",
	    "node.name": "pw-play"}}},
	  {"id": 70, "info": {"props": {"media.class": "Audio/Sink",
	    "node.name": "bluez_output.AC_12_2F_00_11_22.1",
	    "node.description": "AirPods"}}},
	  {"id": 71, "info": {"props": {"media.class": "Audio/Source",
	    "node.name": "alsa_input.pci-0000_00_1f.3.analog-stereo"}}}
	]`)

	sinks, err := parsePwDumpSinks(crudo)
	if err != nil {
		t.Fatalf("no parseó: %v", err)
	}
	if len(sinks) != 2 {
		t.Fatalf("se esperaban 2 sumideros y llegaron %d: %+v", len(sinks), sinks)
	}
	if sinks[0].ID != 51 || sinks[0].Description != "Built-in Audio Analog Stereo" {
		t.Errorf("primer sumidero mal leído: %+v", sinks[0])
	}

	elegido, ok := elegirSalidaInterna(sinks)
	if !ok || elegido.ID != 51 {
		t.Errorf("de esa lista tenía que salir el id 51 y salió %+v (ok=%v)", elegido, ok)
	}
}

func TestParsePwDumpSinksJSONInvalido(t *testing.T) {
	if _, err := parsePwDumpSinks([]byte("no soy json")); err == nil {
		t.Error("un volcado ilegible tiene que dar error, no una lista vacía silenciosa")
	}
}

// Salida textual de `wpctl inspect @DEFAULT_AUDIO_SINK@` en FAZOCAR, con el
// asterisco que wireplumber pone en las propiedades propias del nodo.
func TestParseWpctlNodeName(t *testing.T) {
	crudo := `id 51, type PipeWire:Interface:Node
    alsa.card = "0"
    device.api = "alsa"
    factory.name = "api.alsa.pcm.sink"
  * node.description = "Built-in Audio Analog Stereo"
  * node.name = "alsa_output.pci-0000_00_1f.3.analog-stereo"
    priority.session = "1009"
`
	if got := parseWpctlNodeName(crudo); got != "alsa_output.pci-0000_00_1f.3.analog-stereo" {
		t.Errorf("node.name mal leído: %q", got)
	}

	if got := parseWpctlNodeName("id 51, type PipeWire:Interface:Node\n"); got != "" {
		t.Errorf("sin node.name tiene que devolver vacío y devolvió %q", got)
	}
}

func TestParsePactlSinks(t *testing.T) {
	crudo := "51\talsa_output.pci-0000_00_1f.3.analog-stereo\tPipeWire\ts16le 2ch 48000Hz\tRUNNING\n" +
		"70\tbluez_output.AC_12_2F_00_11_22.1\tPipeWire\ts16le 2ch 48000Hz\tSUSPENDED\n"

	sinks := parsePactlSinks(crudo)
	if len(sinks) != 2 {
		t.Fatalf("se esperaban 2 y llegaron %d", len(sinks))
	}

	elegido, ok := elegirSalidaInterna(sinks)
	if !ok || elegido.NodeName != "alsa_output.pci-0000_00_1f.3.analog-stereo" {
		t.Errorf("eligió %+v (ok=%v)", elegido, ok)
	}
}

// switchOutputToInternalSpeakers tiene que devolver un estado NO conocido cuando
// no cambió nada: si devolviera el dispositivo actual, el teardown "restauraría"
// un cambio que nunca ocurrió, y en Windows —donde no hay implementación— eso
// sería un error en el log por cada alarma.
func TestSwitchOutputDevuelveVacioCuandoNoHayQueCambiar(t *testing.T) {
	a := &Agent{Logger: loggerMudo()}

	if got := a.switchOutputToInternalSpeakers(); got.Known {
		t.Errorf("sin poder leer la salida actual debe devolver Known=false y devolvió %+v", got)
	}
}

// restoreAudioOutput sin estado conocido no debe llamar a nada: apuntar la salida
// al cero de Go dejaría el equipo en un dispositivo inexistente.
func TestRestoreAudioOutputIgnoraElEstadoVacio(t *testing.T) {
	a := &Agent{Logger: loggerMudo()}
	a.restoreAudioOutput(audioOutput{})
}
