package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegado048BorraProgramaAutostartYDatos(t *testing.T) {
	dir := t.TempDir()
	l := legado048{
		base:      filepath.Join(dir, "opt", "observer-actividad"),
		autostart: filepath.Join(dir, "autostart", "observer-actividad.desktop"),
		datos:     []string{filepath.Join(dir, "home", "*", ".local", "share", "observer-actividad")},
	}
	if l.instalado() {
		t.Fatal("sin archivos no debe considerarse instalado")
	}
	datos := filepath.Join(dir, "home", "ana", ".local", "share", "observer-actividad")
	ajeno := filepath.Join(dir, "home", "ana", ".local", "share", "otra-app")
	for _, d := range []string{l.base, datos, ajeno, filepath.Dir(l.autostart)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(l.autostart, []byte("[Desktop Entry]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !l.instalado() {
		t.Fatal("con programa y autostart debe considerarse instalado")
	}
	if got := len(l.borrar()); got != 3 {
		t.Fatalf("esperaba 3 rutas borradas, fueron %d", got)
	}
	for _, p := range []string{l.base, l.autostart, datos} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s debió borrarse", p)
		}
	}
	if _, err := os.Stat(ajeno); err != nil {
		t.Fatal("no debe tocar carpetas de otras aplicaciones")
	}
	if l.instalado() {
		t.Fatal("tras borrar no debe quedar instalado")
	}
}

func TestRutasLegado048CubrenLosTresSO(t *testing.T) {
	for _, so := range []string{"windows", "linux", "darwin"} {
		l := rutasLegado048(so)
		if l.base == "" || l.marca == "" || len(l.datos) == 0 {
			t.Fatalf("%s: rutas incompletas %+v", so, l)
		}
	}
}
