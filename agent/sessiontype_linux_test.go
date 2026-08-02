//go:build linux
// +build linux

package agent

import "testing"

// pickSessionType es lo que decide qué se ve en la consola al lado del SO, y con
// eso si alguien va a intentar «Tomar control» contra una pantalla negra. Lo que
// este test fija es la política de elección, no el formato.
func TestPickSessionType(t *testing.T) {
	activa := func(tipo string) map[string]string {
		return map[string]string{"Type": tipo, "Class": "user", "Active": "yes", "State": "active"}
	}
	inactiva := func(tipo string) map[string]string {
		return map[string]string{"Type": tipo, "Class": "user", "Active": "no", "State": "online"}
	}

	casos := []struct {
		nombre string
		in     []map[string]string
		want   string
	}{
		{
			nombre: "sin sesiones: no se reporta nada",
			in:     nil,
			want:   "",
		},
		{
			nombre: "el caso del Fedora 44 KDE: wayland activa",
			in:     []map[string]string{activa("wayland")},
			want:   "wayland",
		},
		{
			nombre: "x11 activa",
			in:     []map[string]string{activa("x11")},
			want:   "x11",
		},
		{
			nombre: "servidor sin escritorio: sólo tty, no se reporta",
			in: []map[string]string{
				{"Type": "tty", "Class": "user", "Active": "yes", "State": "active"},
			},
			want: "",
		},
		{
			nombre: "gana la ACTIVA, no la primera de la lista",
			in:     []map[string]string{inactiva("x11"), activa("wayland")},
			want:   "wayland",
		},
		{
			nombre: "y al revés: una wayland olvidada no le gana a la x11 del asiento",
			in:     []map[string]string{inactiva("wayland"), activa("x11")},
			want:   "x11",
		},
		{
			nombre: "sin ninguna activa se usa la primera utilizable (mejor que nada)",
			in:     []map[string]string{inactiva("wayland"), inactiva("x11")},
			want:   "wayland",
		},
		{
			nombre: "la pantalla de login (Class=greeter) no cuenta",
			in: []map[string]string{
				{"Type": "wayland", "Class": "greeter", "Active": "yes", "State": "active"},
			},
			want: "",
		},
		{
			nombre: "el tty conviviendo con la sesión gráfica no la tapa",
			in: []map[string]string{
				{"Type": "tty", "Class": "user", "Active": "yes", "State": "active"},
				activa("wayland"),
			},
			want: "wayland",
		},
		{
			nombre: "props vacíos o basura no rompen ni inventan",
			in:     []map[string]string{{}, {"Type": ""}, {"Class": "user"}},
			want:   "",
		},
	}

	for _, c := range casos {
		if got := pickSessionType(c.in); got != c.want {
			t.Errorf("%s: pickSessionType = %q, se esperaba %q", c.nombre, got, c.want)
		}
	}
}
