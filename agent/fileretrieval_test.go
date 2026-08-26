package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sirupsen/logrus"
)

// Feature 042 · el contrato de la expansión de rutas: archivos sueltos y
// directorios recursivos, respetando el tope de nº de archivos (RF-08).

func nuevoAgentDePrueba() *Agent {
	l := logrus.New()
	l.SetOutput(os.NewFile(0, os.DevNull))
	return &Agent{Logger: l}
}

func TestExpandirRutasArchivosYDirectorios(t *testing.T) {
	dir := t.TempDir()

	// Un archivo suelto y un subdirectorio con dos archivos.
	suelto := filepath.Join(dir, "suelto.txt")
	if err := os.WriteFile(suelto, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(sub, n), []byte("y"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	a := nuevoAgentDePrueba()
	got := a.expandirRutasFileRetrieval([]string{suelto, sub}, 0)
	if len(got) != 3 {
		t.Fatalf("esperaba 3 archivos, obtuve %d: %v", len(got), got)
	}
}

func TestExpandirRutasRespetaTope(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"1.txt", "2.txt", "3.txt", "4.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("z"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	a := nuevoAgentDePrueba()
	got := a.expandirRutasFileRetrieval([]string{dir}, 2)
	if len(got) != 2 {
		t.Fatalf("el tope de 2 no se respetó: %d", len(got))
	}
}

func TestExpandirRutasIgnoraInexistente(t *testing.T) {
	a := nuevoAgentDePrueba()
	got := a.expandirRutasFileRetrieval([]string{"/no/existe/ruta"}, 0)
	if len(got) != 0 {
		t.Fatalf("una ruta inexistente no debe aportar archivos: %v", got)
	}
}
