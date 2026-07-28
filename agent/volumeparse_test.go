package agent

import (
	"math"
	"testing"
)

// Feature 028 · Fase 2 — pruebas de los parsers de volumen.
//
// Por qué existen: interpretar mal la salida de estos comandos NO produce un
// error, produce un número equivocado. Ese número se guarda como "el volumen que
// el usuario tenía" y se le devuelve al terminar la alarma, así que un parser
// roto deja el equipo en un volumen que nadie eligió y ninguna prueba de
// extremo a extremo lo notaría: la alarma sonaría, el `DELETE` respondería `ok`
// y el volumen quedaría mal. Es la misma familia de fallo silencioso que los
// cinco "ok falso" de la Fase 1.
//
// Las muestras son salidas reales de cada herramienta, con los casos que de
// verdad rompen: PulseAudio por encima del 100 %, `missing value` de AppleScript
// y el control Master de ALSA sin interruptor.

const epsilon = 1e-9

func casiIgual(a, b float64) bool { return math.Abs(a-b) < epsilon }

func TestParseMacVolume(t *testing.T) {
	casos := []struct {
		nombre    string
		out       string
		level     float64
		muted     bool
		muteKnown bool
		wantErr   bool
	}{
		{nombre: "nivel y silencio legibles", out: "94,false\n", level: 0.94, muteKnown: true},
		{nombre: "silenciado", out: "20,true\n", level: 0.20, muted: true, muteKnown: true},
		{nombre: "volumen cero no es lo mismo que ilegible", out: "0,false\n", level: 0, muteKnown: true},
		{
			// El dispositivo no expone silencio como estado aparte. El nivel sirve;
			// la restauración no debe tocar el mute.
			nombre: "silencio missing value", out: "50,missing value\n", level: 0.50, muteKnown: false,
		},
		{
			// Sin nivel no hay estado previo utilizable: es preferible no subir el
			// volumen a subirlo sin poder devolverlo.
			nombre: "nivel missing value", out: "missing value,false\n", wantErr: true,
		},
		{nombre: "salida vacía", out: "", wantErr: true},
		{nombre: "sin coma", out: "94\n", wantErr: true},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			st, err := parseMacVolume(c.out)

			if c.wantErr {
				if err == nil {
					t.Fatalf("se esperaba error y devolvió %+v", st)
				}
				return
			}
			if err != nil {
				t.Fatalf("error inesperado: %v", err)
			}

			if !st.Known {
				t.Error("Known debería ser true cuando el nivel se pudo leer")
			}
			if !casiIgual(st.Level, c.level) {
				t.Errorf("nivel = %v, se esperaba %v", st.Level, c.level)
			}
			if st.MuteKnown != c.muteKnown {
				t.Errorf("MuteKnown = %v, se esperaba %v", st.MuteKnown, c.muteKnown)
			}
			if st.MuteKnown && st.Muted != c.muted {
				t.Errorf("Muted = %v, se esperaba %v", st.Muted, c.muted)
			}
		})
	}
}

func TestMacSetVolumeScript(t *testing.T) {
	casos := []struct {
		nombre string
		v      volumeState
		want   string
	}{
		{
			nombre: "con silencio conocido y apagado",
			v:      volumeState{Level: 0.94, MuteKnown: true, Known: true},
			want:   "set volume output volume 94 without output muted",
		},
		{
			nombre: "con silencio conocido y encendido",
			v:      volumeState{Level: 0.20, Muted: true, MuteKnown: true, Known: true},
			want:   "set volume output volume 20 with output muted",
		},
		{
			// Sin MuteKnown el script NO menciona el mute: inventar un `without
			// output muted` desilenciaría un equipo que el usuario dejó en silencio.
			nombre: "sin silencio conocido no toca el mute",
			v:      volumeState{Level: 0.50, Known: true},
			want:   "set volume output volume 50",
		},
		{
			nombre: "el máximo va sin silenciar",
			v:      maxVolumeState,
			want:   "set volume output volume 100 without output muted",
		},
		{
			// Redondeo y no truncado: 0.945 → 94 con truncado, 95 con redondeo.
			nombre: "redondea en vez de truncar",
			v:      volumeState{Level: 0.945, MuteKnown: true, Known: true},
			want:   "set volume output volume 95 without output muted",
		},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := macSetVolumeScript(c.v); got != c.want {
				t.Errorf("script = %q, se esperaba %q", got, c.want)
			}
		})
	}
}

func TestParsePactlVolume(t *testing.T) {
	// Salida real de `pactl get-sink-volume @DEFAULT_SINK@`.
	const dosCanales = "Volume: front-left: 39321 /  60% / -13.31 dB,   front-right: 39321 /  60% / -13.31 dB\n" +
		"        balance 0.00\n"

	level, err := parsePactlVolume(dosCanales)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if !casiIgual(level, 0.60) {
		t.Errorf("nivel = %v, se esperaba 0.60", level)
	}

	// PulseAudio permite pasarse del 100 %. Sin acotar, ese valor llega tal cual
	// a `SetMasterVolumeLevelScalar` de Windows, que lo rechaza por fuera de rango.
	sobre100 := "Volume: front-left: 100302 / 153% / 4.00 dB,   front-right: 100302 / 153% / 4.00 dB\n"
	level, err = parsePactlVolume(sobre100)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if !casiIgual(level, 1) {
		t.Errorf("nivel = %v, se esperaba acotado a 1", level)
	}

	if _, err := parsePactlVolume("Failure: No such entity\n"); err == nil {
		t.Error("una salida sin porcentaje debería dar error, no un nivel 0 silencioso")
	}
}

func TestParsePactlMute(t *testing.T) {
	if !parsePactlMute("Mute: yes\n") {
		t.Error("`Mute: yes` debería leerse como silenciado")
	}
	if parsePactlMute("Mute: no\n") {
		t.Error("`Mute: no` debería leerse como no silenciado")
	}
}

func TestParseWpctlVolume(t *testing.T) {
	st, err := parseWpctlVolume("Volume: 0.60\n")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if !casiIgual(st.Level, 0.60) || st.Muted || !st.MuteKnown || !st.Known {
		t.Errorf("estado = %+v, se esperaba 0.60 sin silenciar y con ambos conocidos", st)
	}

	st, err = parseWpctlVolume("Volume: 0.35 [MUTED]\n")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if !casiIgual(st.Level, 0.35) || !st.Muted {
		t.Errorf("estado = %+v, se esperaba 0.35 silenciado", st)
	}

	if _, err := parseWpctlVolume("Node 51 not found\n"); err == nil {
		t.Error("una salida sin nivel debería dar error")
	}
}

func TestParseAmixerVolume(t *testing.T) {
	// Salida real de `amixer -M get Master`.
	const conInterruptor = `Simple mixer control 'Master',0
  Capabilities: pvolume pswitch pswitch-joined
  Playback channels: Front Left - Front Right
  Limits: Playback 0 - 65536
  Mono:
  Front Left: Playback 43690 [67%] [on]
  Front Right: Playback 43690 [67%] [on]
`

	st, err := parseAmixerVolume(conInterruptor)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if !casiIgual(st.Level, 0.67) {
		t.Errorf("nivel = %v, se esperaba 0.67", st.Level)
	}
	if st.Muted || !st.MuteKnown {
		t.Errorf("estado del mute = (%v,%v), se esperaba (false,true)", st.Muted, st.MuteKnown)
	}

	apagado := "  Front Left: Playback 0 [0%] [off]\n"
	st, err = parseAmixerVolume(apagado)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if !st.Muted || !casiIgual(st.Level, 0) {
		t.Errorf("estado = %+v, se esperaba 0 y silenciado", st)
	}

	// Un Master sin interruptor: hay nivel pero no hay mute que restaurar.
	sinInterruptor := "  Front Left: Playback 32768 [50%]\n"
	st, err = parseAmixerVolume(sinInterruptor)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if !casiIgual(st.Level, 0.50) {
		t.Errorf("nivel = %v, se esperaba 0.50", st.Level)
	}
	if st.MuteKnown {
		t.Error("MuteKnown debería ser false cuando la salida no trae [on]/[off]")
	}

	if _, err := parseAmixerVolume("amixer: Unable to find simple control 'Master',0\n"); err == nil {
		t.Error("una salida sin porcentaje debería dar error")
	}
}

func TestPercentArg(t *testing.T) {
	casos := map[float64]string{
		0:     "0%",
		0.6:   "60%",
		0.945: "95%", // redondeo, no truncado
		1:     "100%",
		1.53:  "100%", // acotado
		-1:    "0%",   // acotado
	}

	for level, want := range casos {
		if got := percentArg(level); got != want {
			t.Errorf("percentArg(%v) = %q, se esperaba %q", level, got, want)
		}
	}
}

func TestBoolArg(t *testing.T) {
	if boolArg(true) != "1" || boolArg(false) != "0" {
		t.Error("boolArg debería devolver el 1/0 que esperan pactl y wpctl")
	}
}

// TestNatsBoolFlagFallaCerrado es la prueba de una propiedad de seguridad, no de
// formato: las dos banderas de la Fase 2 son las más peligrosas de la feature
// —sonar para siempre, y al máximo— y un payload que no se entiende NO debe
// activarlas.
func TestNatsBoolFlagFallaCerrado(t *testing.T) {
	verdaderos := []string{"1", "true", "TRUE", "True", "yes", "on", " true "}
	for _, v := range verdaderos {
		if !natsBoolFlag(v) {
			t.Errorf("natsBoolFlag(%q) = false, se esperaba true", v)
		}
	}

	falsos := []string{"", "0", "false", "no", "off", "2", "null", "None", "sí", "verdadero", "t", "y"}
	for _, v := range falsos {
		if natsBoolFlag(v) {
			t.Errorf("natsBoolFlag(%q) = true; cualquier valor no reconocido debe fallar cerrado", v)
		}
	}
}
