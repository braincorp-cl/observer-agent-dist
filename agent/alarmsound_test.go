package agent

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// Feature 028 · pruebas de la sirena generada.
//
// El WAV se puede verificar por completo sin dispositivo de audio: la cabecera se lee
// campo por campo y las muestras se miden. Lo que NO prueba nada de esto es cómo
// suena — eso va en el E2E con oídos.

// wav es la cabecera leída de vuelta.
type wav struct {
	riffSize      uint32
	format        uint16
	channels      uint16
	sampleRate    uint32
	byteRate      uint32
	blockAlign    uint16
	bitsPerSample uint16
	dataSize      uint32
	samples       []int16
}

func parseWav(t *testing.T, raw []byte) wav {
	t.Helper()

	if len(raw) < 44 {
		t.Fatalf("el archivo tiene %d bytes; una cabecera WAV son 44", len(raw))
	}

	r := bytes.NewReader(raw)
	leer := func(v any) {
		if err := binary.Read(r, binary.LittleEndian, v); err != nil {
			t.Fatalf("cabecera truncada: %v", err)
		}
	}
	marca := func(esperada string) {
		b := make([]byte, 4)
		if _, err := r.Read(b); err != nil {
			t.Fatalf("no se pudo leer la marca %q: %v", esperada, err)
		}
		if string(b) != esperada {
			t.Fatalf("se esperaba la marca %q y vino %q", esperada, string(b))
		}
	}

	var w wav
	var fmtSize uint32

	marca("RIFF")
	leer(&w.riffSize)
	marca("WAVE")
	marca("fmt ")
	leer(&fmtSize)
	if fmtSize != 16 {
		t.Fatalf("el bloque fmt mide %d y para PCM son 16", fmtSize)
	}
	leer(&w.format)
	leer(&w.channels)
	leer(&w.sampleRate)
	leer(&w.byteRate)
	leer(&w.blockAlign)
	leer(&w.bitsPerSample)
	marca("data")
	leer(&w.dataSize)

	w.samples = make([]int16, w.dataSize/2)
	for i := range w.samples {
		leer(&w.samples[i])
	}

	return w
}

// La cabecera tiene que ser válida y COHERENTE con los datos. Un tamaño declarado que
// no calza es la forma clásica de que `System.Media.SoundPlayer` rechace el archivo en
// silencio: la alarma respondería `ok` y el equipo no sonaría.
func TestLaSirenaEsUnWavPcmValidoYCoherente(t *testing.T) {
	raw := buildAlarmSirenWav()
	w := parseWav(t, raw)

	if w.format != 1 {
		t.Errorf("formato %d; SoundPlayer de Windows sólo acepta PCM (1)", w.format)
	}
	if w.channels != 1 {
		t.Errorf("canales = %d, se esperaba mono", w.channels)
	}
	if w.bitsPerSample != 16 {
		t.Errorf("bits por muestra = %d, se esperaban 16", w.bitsPerSample)
	}
	if w.sampleRate != alarmWavSampleRate {
		t.Errorf("frecuencia de muestreo = %d, se esperaba %d", w.sampleRate, alarmWavSampleRate)
	}

	// Los tres campos derivados. Si alguno miente, hay reproductores que se cuelgan y
	// otros que reproducen a la velocidad equivocada.
	if esperado := uint32(alarmWavSampleRate * 2); w.byteRate != esperado {
		t.Errorf("byteRate = %d, se esperaba %d", w.byteRate, esperado)
	}
	if w.blockAlign != 2 {
		t.Errorf("blockAlign = %d, se esperaba 2", w.blockAlign)
	}
	if int(w.dataSize) != len(raw)-44 {
		t.Errorf("data declara %d bytes y quedan %d después de la cabecera", w.dataSize, len(raw)-44)
	}
	if int(w.riffSize) != len(raw)-8 {
		t.Errorf("RIFF declara %d y el archivo mide %d (deberían ser %d)",
			w.riffSize, len(raw), len(raw)-8)
	}
}

func TestLaSirenaDuraLoQueDiceDurar(t *testing.T) {
	w := parseWav(t, buildAlarmSirenWav())

	esperadas := alarmWavSampleRate * alarmWavToneMillis / 1000 * 2 * alarmWavCycles
	if len(w.samples) != esperadas {
		t.Errorf("la sirena tiene %d muestras y se esperaban %d", len(w.samples), esperadas)
	}
}

// Saturar no es sonar más fuerte: suena a parlante roto. Y quedarse muy por debajo del
// tope desperdicia justamente lo que la Fase 2 fue a buscar subiendo el volumen.
func TestLaSirenaNoSaturaPeroLlegaFuerte(t *testing.T) {
	w := parseWav(t, buildAlarmSirenWav())

	pico := 0
	for _, s := range w.samples {
		v := int(s)
		if v < 0 {
			v = -v
		}
		if v > pico {
			pico = v
		}
	}

	// math.Trunc y no una conversión directa: `alarmWavAmplitude * math.MaxInt16` es
	// una constante con parte fraccionaria (29490.3) y Go no deja convertirla a int.
	// Truncar además es exactamente lo que hace el generador al pasar a int16.
	techo := int(math.Trunc(alarmWavAmplitude * math.MaxInt16))
	if pico > techo {
		t.Errorf("pico %d por encima del tope previsto %d: hay saturación", pico, techo)
	}
	if pico < techo*95/100 {
		t.Errorf("pico %d: la sirena quedó %d%% por debajo de su propia amplitud",
			pico, 100-pico*100/techo)
	}
}

// Los extremos de cada tono tienen que estar desvanecidos. Sin esto, el salto de
// amplitud en cada cambio se oye como un chasquido periódico, que suena a equipo con
// falla y no a alarma.
func TestLosTonosEntranYSalenDesvanecidos(t *testing.T) {
	w := parseWav(t, buildAlarmSirenWav())
	toneSamples := alarmWavSampleRate * alarmWavToneMillis / 1000

	if w.samples[0] != 0 {
		t.Errorf("la primera muestra es %d y debería arrancar en silencio", w.samples[0])
	}

	// Frontera entre el primer tono y el segundo: la última muestra del primero y la
	// primera del segundo tienen que estar cerca de cero.
	const cerca = 2000 // ~6 % del rango; el desvanecido es lineal sobre 5 ms

	for _, i := range []int{toneSamples - 1, toneSamples, len(w.samples) - 1} {
		v := int(w.samples[i])
		if v < 0 {
			v = -v
		}
		if v > cerca {
			t.Errorf("la muestra %d vale %d: la frontera del tono no está desvanecida", i, v)
		}
	}
}

// Y que de verdad sean DOS tonos distintos: una sirena de un solo tono es un zumbido.
// Se cuentan los cruces por cero de cada mitad, que es proporcional a la frecuencia.
func TestLaSirenaAlternaDosTonosDistintos(t *testing.T) {
	w := parseWav(t, buildAlarmSirenWav())
	toneSamples := alarmWavSampleRate * alarmWavToneMillis / 1000

	cruces := func(s []int16) int {
		n := 0
		for i := 1; i < len(s); i++ {
			if (s[i-1] < 0) != (s[i] < 0) {
				n++
			}
		}
		return n
	}

	bajo := cruces(w.samples[:toneSamples])
	alto := cruces(w.samples[toneSamples : toneSamples*2])

	if bajo == 0 || alto == 0 {
		t.Fatalf("uno de los tonos es silencio (cruces: %d y %d)", bajo, alto)
	}
	if alto <= bajo {
		t.Errorf("el segundo tono no es más agudo que el primero (cruces: %d vs %d)", bajo, alto)
	}

	// Los cruces por cero de una senoidal son 2 por período: 2*f*duración.
	for _, c := range []struct {
		nombre   string
		cruces   int
		esperada float64
	}{
		{"bajo", bajo, alarmWavLowHz},
		{"alto", alto, alarmWavHighHz},
	} {
		medida := float64(c.cruces) / 2 * 1000 / float64(alarmWavToneMillis)
		if math.Abs(medida-c.esperada) > c.esperada*0.05 {
			t.Errorf("el tono %s midió ~%.0f Hz y se declaró %.0f Hz", c.nombre, medida, c.esperada)
		}
	}
}
