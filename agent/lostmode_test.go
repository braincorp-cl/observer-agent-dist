package agent

import (
	"testing"
	"time"
)

// Feature 030 · Fase 1 · el contrato del multipart con el servidor.
//
// Los nombres de estos campos no los valida nadie en caliente: si uno se
// escribiera mal, la subida seguiría contestando 200 y la evidencia llegaría
// incompleta —un caso que PARECE completo y no lo está—. Es el peor modo de
// falla posible para esta feature, y es exactamente el que una prueba puede
// atrapar sin equipo, sin red y sin escritorio.

func TestCamposEvidenciaConFix(t *testing.T) {
	loc := Location{
		Lat:        -33.4489,
		Long:       -70.6693,
		AccuracyM:  35,
		Source:     SourceWiFi,
		CapturedAt: time.Date(2026, 8, 12, 15, 4, 5, 0, time.UTC),
	}

	campos := camposEvidencia(loc, ScreenShot{Path: "/tmp/pantalla.png"}, WebcamShot{}, "ana")

	quiere := map[string]string{
		"captured_at":  "1786547045",
		"session_user": "ana",
		"lat":          "-33.4489000",
		"lng":          "-70.6693000",
		"accuracy_m":   "35",
		"source":       "wifi",
	}
	for k, v := range quiere {
		if campos[k] != v {
			t.Errorf("campos[%q] = %q, se esperaba %q", k, campos[k], v)
		}
	}

	// Con captura NO va el motivo: el servidor decide qué fila crear mirando si
	// vino archivo o vino motivo, y mandar los dos sería ambiguo.
	if _, hay := campos["screen_reason"]; hay {
		t.Error("con captura no corresponde mandar screen_reason")
	}
}

func TestCamposEvidenciaSinFixNoInventaCoordenadas(t *testing.T) {
	// `denied` es el caso delicado: hay una respuesta del sensor, pero no hay
	// posición. Mandar (0,0) sería Null Island entrando como si fuera un lugar.
	loc := Location{Source: SourceDenied, CapturedAt: time.Now().UTC()}

	campos := camposEvidencia(loc, ScreenShot{Reason: captureNoSession}, WebcamShot{}, "")

	for _, k := range []string{"lat", "lng", "accuracy_m"} {
		if _, hay := campos[k]; hay {
			t.Errorf("sin fix no puede viajar %q (llegó %q)", k, campos[k])
		}
	}

	// La fuente sí viaja: la pieza de pantalla queda diciendo POR QUÉ no se supo
	// dónde estaba el equipo, que es información del caso.
	if campos["source"] != SourceDenied {
		t.Errorf("source = %q, se esperaba %q", campos["source"], SourceDenied)
	}

	if campos["screen_reason"] != captureNoSession {
		t.Errorf("screen_reason = %q, se esperaba %q", campos["screen_reason"], captureNoSession)
	}
}

func TestCamposEvidenciaSesionVaciaEsUnDato(t *testing.T) {
	// Nadie con sesión abierta es información —el ladrón todavía no entró—, así
	// que el campo viaja vacío en vez de omitirse.
	campos := camposEvidencia(
		Location{Source: SourceUnavailable, CapturedAt: time.Now().UTC()},
		ScreenShot{Reason: captureNoSession},
		WebcamShot{},
		"",
	)

	if v, hay := campos["session_user"]; !hay || v != "" {
		t.Errorf("session_user tenía que estar y vacío, quedó (%q, %v)", v, hay)
	}
}

func TestCamposEvidenciaPrecisionDeCoordenadas(t *testing.T) {
	// 7 decimales ≈ 1 cm. Con menos, dos puntos distintos de un recorrido urbano
	// podrían quedar en la misma coordenada; con notación científica (%v sobre un
	// float64 chico) el servidor tendría que parsear algo que no espera.
	loc := Location{
		Lat:        -0.0000001,
		Long:       -70.6693,
		AccuracyM:  10,
		Source:     SourceNative,
		CapturedAt: time.Now().UTC(),
	}

	campos := camposEvidencia(loc, ScreenShot{Path: "/tmp/x.png"}, WebcamShot{}, "ana")

	if campos["lat"] != "-0.0000001" {
		t.Errorf("lat = %q, se esperaba -0.0000001 sin notación científica", campos["lat"])
	}
}

// ── Feature 030 · Fase 2 · la webcam en los campos del ciclo ─────────────────

func TestCamposEvidenciaWebcamApagadaNoMandaMotivo(t *testing.T) {
	// La distinción que esta prueba protege: "apagada" no es "falló". Si el campo
	// viajara con la webcam apagada, el servidor crearía una fila de webcam por
	// ciclo y la línea de tiempo de una flota que nunca la activó se llenaría de
	// mensajes sobre una cámara que nadie pidió usar.
	campos := camposEvidencia(
		Location{Source: SourceNative, CapturedAt: time.Now().UTC()},
		ScreenShot{Path: "/tmp/x.png"},
		WebcamShot{}, // apagada: ni ruta ni motivo
		"ana",
	)

	if v, hay := campos["webcam_reason"]; hay {
		t.Errorf("con la webcam apagada no tenía que viajar webcam_reason, llegó %q", v)
	}
}

func TestCamposEvidenciaWebcamConMotivoLoManda(t *testing.T) {
	campos := camposEvidencia(
		Location{Source: SourceNative, CapturedAt: time.Now().UTC()},
		ScreenShot{Path: "/tmp/x.png"},
		WebcamShot{Reason: webcamDenied},
		"ana",
	)

	if campos["webcam_reason"] != webcamDenied {
		t.Errorf("webcam_reason = %q, se esperaba %q", campos["webcam_reason"], webcamDenied)
	}
}

func TestCamposEvidenciaWebcamConFotoNoMandaMotivo(t *testing.T) {
	// Con foto no hay motivo: el contrato es "o archivo, o motivo, nunca los dos".
	campos := camposEvidencia(
		Location{Source: SourceNative, CapturedAt: time.Now().UTC()},
		ScreenShot{Path: "/tmp/x.png"},
		WebcamShot{Path: "/tmp/webcam.jpg"},
		"ana",
	)

	if v, hay := campos["webcam_reason"]; hay {
		t.Errorf("con foto no tenía que viajar webcam_reason, llegó %q", v)
	}
}
