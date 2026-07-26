//go:build linux
// +build linux

package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// Feature 028 · pruebas del descubrimiento de sesión gráfica en Linux.
//
// Se prueban las funciones puras (parseo). El descubrimiento en sí depende de que
// la máquina tenga un escritorio vivo, así que su verificación es el E2E en
// staging, no un test unitario que sería verde o rojo según dónde corra.

func TestParseLoginctlProps(t *testing.T) {
	// Salida real de `loginctl show-session` con las propiedades que pedimos.
	out := []byte("Active=yes\nState=active\nType=wayland\nName=jptamayo\nUser=1000\nDisplay=\n")

	props := parseLoginctlProps(out)

	cases := map[string]string{
		"Active": "yes",
		"State":  "active",
		"Type":   "wayland",
		"Name":   "jptamayo",
		"User":   "1000",
	}
	for k, want := range cases {
		if got := props[k]; got != want {
			t.Errorf("props[%q] = %q, se esperaba %q", k, got, want)
		}
	}

	// Una propiedad vacía tiene que existir como clave vacía, no faltar: la
	// diferencia importa porque `Display=` es normal en Wayland.
	if v, ok := props["Display"]; !ok || v != "" {
		t.Errorf("Display debía existir y estar vacía, quedó (%q, %v)", v, ok)
	}
}

func TestParseLoginctlPropsBasura(t *testing.T) {
	// Líneas sin `=` se ignoran en vez de romper el parseo.
	props := parseLoginctlProps([]byte("esto no es una propiedad\nActive=yes\n\n"))

	if props["Active"] != "yes" {
		t.Errorf("una línea inválida hizo perder las válidas: %v", props)
	}
	if len(props) != 1 {
		t.Errorf("se esperaba 1 propiedad, quedaron %d: %v", len(props), props)
	}
}

func TestReadProcEnviron(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "environ")

	// /proc/<pid>/environ separa con NUL, no con salto de línea.
	content := "DISPLAY=:0\x00XAUTHORITY=/run/user/1000/.mutter-Xwaylandauth.ABC123\x00" +
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus\x00"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	env, err := readProcEnviron(path)
	if err != nil {
		t.Fatal(err)
	}

	if env["DISPLAY"] != ":0" {
		t.Errorf("DISPLAY = %q", env["DISPLAY"])
	}
	if env["DBUS_SESSION_BUS_ADDRESS"] != "unix:path=/run/user/1000/bus" {
		t.Errorf("DBUS_SESSION_BUS_ADDRESS = %q", env["DBUS_SESSION_BUS_ADDRESS"])
	}
	// El valor contiene un `=`? No, pero la ruta de XAUTHORITY sí lleva puntos y
	// mayúsculas: se verifica que no se recorte.
	if env["XAUTHORITY"] != "/run/user/1000/.mutter-Xwaylandauth.ABC123" {
		t.Errorf("XAUTHORITY = %q", env["XAUTHORITY"])
	}
}

func TestReadProcEnvironValorConIgual(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "environ")

	// Caso que rompería un `strings.Split(kv, "=")`: el valor lleva `=` adentro.
	// Por eso el código usa strings.Cut, que parte sólo en el primero.
	if err := os.WriteFile(path, []byte("DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus,guid=abc\x00"), 0600); err != nil {
		t.Fatal(err)
	}

	env, err := readProcEnviron(path)
	if err != nil {
		t.Fatal(err)
	}

	want := "unix:path=/run/user/1000/bus,guid=abc"
	if env["DBUS_SESSION_BUS_ADDRESS"] != want {
		t.Errorf("el valor se recortó en el primer `=`: %q", env["DBUS_SESSION_BUS_ADDRESS"])
	}
}

func TestReadProcEnvironInexistente(t *testing.T) {
	// Un pid que desaparece entre el ReadDir y el ReadFile es lo normal al
	// recorrer /proc; tiene que devolver error y no entrar en pánico.
	if _, err := readProcEnviron("/proc/999999999/environ"); err == nil {
		t.Error("se esperaba error leyendo un pid inexistente")
	}
}
