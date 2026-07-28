package agent

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Feature 028 · Fase 2 — lectura de la salida de los comandos de volumen.
//
// Estas funciones son puras y viven en un archivo SIN etiqueta de plataforma a
// propósito, aunque cada una interprete la salida de un comando que sólo existe
// en un SO. La razón es que así se pueden probar en cualquier máquina: un
// `parseMacVolume` encerrado tras `//go:build darwin` sólo correría en un Mac, y
// en la práctica no correría nunca —el CI compila macOS pero las pruebas van en
// Linux—, con lo que el parser más frágil de la fase sería el único sin red.
//
// Interpretar mal una de estas salidas no da un error: da un NÚMERO EQUIVOCADO,
// que se guarda como "el volumen que el usuario tenía" y se le devuelve al
// terminar la alarma. Es un fallo silencioso, del mismo tipo que los cinco "ok
// falso" que destapó el E2E de la Fase 1.

// macVolumeScale es la escala de `set volume output volume` de AppleScript, que
// va de 0 a 100 y no de 0 a 1 como el resto del código.
const macVolumeScale = 100

// parseMacVolume interpreta la salida `<nivel>,<silencio>` de
// `osascript -e "…(output volume of s)… & "," & …(output muted of s)…"`.
//
// Los dos campos pueden venir `missing value`, y no significan lo mismo:
//
//   - Nivel ausente ⇒ no hay estado previo utilizable y no se toca nada. Subir
//     el volumen sin poder devolverlo es el caso que la Fase 2 evita.
//   - Silencio ausente ⇒ el dispositivo no expone silencio como estado aparte
//     (volumen 0 ES el silencio). El nivel sigue sirviendo; sólo se marca que la
//     restauración no debe tocar el mute.
func parseMacVolume(out string) (volumeState, error) {
	parts := strings.SplitN(strings.TrimSpace(out), ",", 2)
	if len(parts) != 2 {
		return volumeState{}, fmt.Errorf("salida inesperada de osascript: %q", out)
	}

	rawLevel := strings.TrimSpace(parts[0])
	level, err := strconv.ParseFloat(rawLevel, 64)
	if err != nil {
		return volumeState{}, fmt.Errorf("nivel de volumen ilegible (%q): %w", rawLevel, err)
	}

	st := volumeState{
		Level: clampLevel(level / macVolumeScale),
		Known: true,
	}

	switch strings.TrimSpace(parts[1]) {
	case "true":
		st.Muted, st.MuteKnown = true, true
	case "false":
		st.Muted, st.MuteKnown = false, true
	default:
		// `missing value` o cualquier otra cosa: no se sabe, no se toca.
		st.MuteKnown = false
	}

	return st, nil
}

// macSetVolumeScript arma el `set volume` de AppleScript.
//
// El nivel se interpola como entero ya redondeado y acotado, así que no hay
// texto de origen externo en el script: no es una superficie de inyección de
// AppleScript como sí lo era el `alert` (que por eso pasa el texto del operador
// por variables de entorno y no por el script).
func macSetVolumeScript(v volumeState) string {
	level := int(math.Round(clampLevel(v.Level) * macVolumeScale))

	if !v.MuteKnown {
		return fmt.Sprintf("set volume output volume %d", level)
	}

	if v.Muted {
		return fmt.Sprintf("set volume output volume %d with output muted", level)
	}

	return fmt.Sprintf("set volume output volume %d without output muted", level)
}

// pactlPercent captura el primer porcentaje de
// `Volume: front-left: 39321 /  60% / -13.31 dB,   front-right: …`.
var pactlPercent = regexp.MustCompile(`(\d+)%`)

func parsePactlVolume(out string) (float64, error) {
	m := pactlPercent.FindStringSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("no se encontró el porcentaje en %q", out)
	}

	pct, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, err
	}

	return clampLevel(float64(pct) / 100), nil
}

// parsePactlMute interpreta `Mute: yes` / `Mute: no`.
func parsePactlMute(out string) bool {
	return strings.Contains(strings.ToLower(out), "yes")
}

// wpctlVolume captura `Volume: 0.60`, con `[MUTED]` opcional detrás.
var wpctlVolume = regexp.MustCompile(`Volume:\s*([0-9.]+)`)

func parseWpctlVolume(out string) (volumeState, error) {
	m := wpctlVolume.FindStringSubmatch(out)
	if m == nil {
		return volumeState{}, fmt.Errorf("no se encontró el nivel en %q", out)
	}

	level, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return volumeState{}, err
	}

	// wpctl informa el silencio en la MISMA línea, así que acá sí se sabe
	// siempre: la ausencia de `[MUTED]` es un "no silenciado" positivo y no un
	// dato faltante.
	return volumeState{
		Level:     clampLevel(level),
		Muted:     strings.Contains(strings.ToUpper(out), "[MUTED]"),
		MuteKnown: true,
		Known:     true,
	}, nil
}

// amixerLevel captura `[100%]`; amixerSwitch captura `[on]` / `[off]`.
var (
	amixerLevel  = regexp.MustCompile(`\[(\d+)%\]`)
	amixerSwitch = regexp.MustCompile(`\[(on|off)\]`)
)

func parseAmixerVolume(out string) (volumeState, error) {
	m := amixerLevel.FindStringSubmatch(out)
	if m == nil {
		return volumeState{}, fmt.Errorf("no se encontró el porcentaje en %q", out)
	}

	pct, err := strconv.Atoi(m[1])
	if err != nil {
		return volumeState{}, err
	}

	st := volumeState{Level: clampLevel(float64(pct) / 100), Known: true}

	// El interruptor no existe en todos los controles Master.
	if sw := amixerSwitch.FindStringSubmatch(out); sw != nil {
		st.Muted, st.MuteKnown = sw[1] == "off", true
	}

	return st, nil
}

// percentArg formatea un nivel 0-1 como el `NN%` que esperan pactl, wpctl y
// amixer.
func percentArg(level float64) string {
	return strconv.Itoa(int(math.Round(clampLevel(level)*100))) + "%"
}

// boolArg es el "1/0" que esperan `pactl set-sink-mute` y `wpctl set-mute`.
func boolArg(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
