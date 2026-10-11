package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLimpiarTituloActividad(t *testing.T) {
	if got := limpiarTituloActividad("  hola\tmundo\n "); got != "hola mundo" {
		t.Fatalf("limpieza: %q", got)
	}
	largo := strings.Repeat("x", 300)
	if got := limpiarTituloActividad(largo); len(got) != 200 {
		t.Fatalf("recorte: len=%d", len(got))
	}
}

func TestSoActividadConocido(t *testing.T) {
	switch soActividad() {
	case "windows", "macos", "linux":
	default:
		t.Fatalf("so inesperado: %q", soActividad())
	}
}

// La muestra siempre debe serializar a una sola línea JSON (contrato del modo).
func TestMuestraSerializaUnaLinea(t *testing.T) {
	b, err := json.Marshal(MuestraActividad(false))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "\n") {
		t.Fatal("la muestra no debe contener saltos de línea")
	}
}
