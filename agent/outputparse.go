package agent

import (
	"encoding/json"
	"strings"
)

// Parseo y REGLAS de la salida de audio en Linux.
//
// Vive sin etiqueta de plataforma a propósito: la CI corre `go test` en Linux y
// macOS, y una regla enterrada en un archivo `//go:build linux` sí se ejecuta,
// pero el criterio de "cuál es el parlante interno" es lo bastante delicado como
// para tenerlo acá, junto a sus pruebas, y no mezclado con el manejo de procesos.

// sinkPipeWire es un sumidero de audio tal como lo reporta PipeWire.
type sinkPipeWire struct {
	ID          int
	NodeName    string
	Description string
}

// esSalidaInterna decide si un sumidero son los parlantes del propio equipo.
//
// Se mira el `node.name`, que es estable y en inglés, y NO la descripción: la
// descripción está traducida al idioma del sistema ("Audio interno", "Built-in
// Audio") y elegir por ella rompería en cuanto cambie el idioma.
//
// Positivos:
//   - `alsa_output.pci-…`      tarjeta integrada por PCI, el caso del portátil típico
//   - `alsa_output.platform-…` codec integrado en SoC (ARM: Raspberry Pi y similares)
//
// Negativos, y cada uno por su motivo:
//   - `bluez_output.…`/`bluez_sink.…` Bluetooth: son justo los audífonos que motivan todo esto
//   - `alsa_output.usb-…`             parlante o audífono USB: tampoco es el equipo
//   - `hdmi`/`displayport`            sale por el cable de video, o sea a un monitor o televisor
//   - `iec958`/`spdif`                salida digital hacia un amplificador externo
//
// Los cuatro últimos son PCI y por eso hay que descartarlos explícitamente: **no
// hacen sonar el equipo que estamos buscando**, hacen sonar el aparato que tienen
// enchufado al otro lado.
func esSalidaInterna(nodeName string) bool {
	n := strings.ToLower(nodeName)

	for _, externa := range []string{"hdmi", "displayport", "iec958", "spdif"} {
		if strings.Contains(n, externa) {
			return false
		}
	}

	return strings.HasPrefix(n, "alsa_output.pci-") || strings.HasPrefix(n, "alsa_output.platform-")
}

// elegirSalidaInterna devuelve el sumidero interno preferido de la lista.
//
// Cuando hay más de uno —una tarjeta con salida analógica y otra digital, por
// ejemplo— gana el que diga `analog`, que es el que va a los parlantes físicos.
// Si ninguno lo dice, el primero interno sirve.
func elegirSalidaInterna(sinks []sinkPipeWire) (sinkPipeWire, bool) {
	var primero sinkPipeWire
	var hay bool

	for _, s := range sinks {
		if !esSalidaInterna(s.NodeName) {
			continue
		}
		if strings.Contains(strings.ToLower(s.NodeName), "analog") {
			return s, true
		}
		if !hay {
			primero, hay = s, true
		}
	}

	return primero, hay
}

// parsePwDumpSinks saca los sumideros de audio del JSON de `pw-dump`.
//
// Se ignora todo lo que no sea `media.class = "Audio/Sink"`: el volcado trae el
// grafo entero de PipeWire —puertos, enlaces, dispositivos, flujos de cada
// aplicación— y quedarse con los nodos de salida es lo único que interesa acá.
func parsePwDumpSinks(salida []byte) ([]sinkPipeWire, error) {
	var objetos []struct {
		ID   int `json:"id"`
		Info struct {
			Props map[string]interface{} `json:"props"`
		} `json:"info"`
	}

	if err := json.Unmarshal(salida, &objetos); err != nil {
		return nil, err
	}

	var sinks []sinkPipeWire
	for _, o := range objetos {
		if texto(o.Info.Props["media.class"]) != "Audio/Sink" {
			continue
		}
		nombre := texto(o.Info.Props["node.name"])
		if nombre == "" {
			continue
		}
		sinks = append(sinks, sinkPipeWire{
			ID:          o.ID,
			NodeName:    nombre,
			Description: texto(o.Info.Props["node.description"]),
		})
	}

	return sinks, nil
}

// texto lee una propiedad de PipeWire como cadena. Las props vienen como
// `interface{}` porque el volcado mezcla cadenas, números y booleanos en el mismo
// mapa.
func texto(v interface{}) string {
	s, _ := v.(string)
	return s
}

// parseWpctlNodeName saca el `node.name` de la salida de `wpctl inspect`.
//
// El formato es `  * node.name = "alsa_output.pci-0000_00_1f.3.analog-stereo"`,
// con un asterisco delante de las propiedades que el nodo define por sí mismo. Se
// acepta con o sin asterisco porque eso depende de la versión de wireplumber.
func parseWpctlNodeName(salida string) string {
	for _, linea := range strings.Split(salida, "\n") {
		l := strings.TrimSpace(linea)
		l = strings.TrimPrefix(l, "*")
		l = strings.TrimSpace(l)

		if !strings.HasPrefix(l, "node.name") {
			continue
		}

		_, valor, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}

		return strings.Trim(strings.TrimSpace(valor), `"`)
	}

	return ""
}

// parsePactlSinks lee la salida de `pactl list short sinks`, que es tabulada:
// `51\talsa_output.pci-…\tPipeWire\ts16le 2ch 48000Hz\tRUNNING`.
func parsePactlSinks(salida string) []sinkPipeWire {
	var sinks []sinkPipeWire

	for _, linea := range strings.Split(salida, "\n") {
		campos := strings.Fields(strings.TrimSpace(linea))
		if len(campos) < 2 {
			continue
		}
		sinks = append(sinks, sinkPipeWire{NodeName: campos[1]})
	}

	return sinks
}
