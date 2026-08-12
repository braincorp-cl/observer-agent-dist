//go:build linux
// +build linux

package agent

import "testing"

// Feature 030 · Fase 1 · la decisión que evita la pantalla negra en Linux.
//
// Si `sesionEsWayland` se equivoca hacia el "no", el ciclo corre `scrot` contra
// una sesión Wayland, la captura sale NEGRA y el caso se llena de imágenes
// vacías que parecen evidencia. Si se equivoca hacia el "sí", se pierden
// capturas que sí se podían tomar bajo XWayland. Las dos direcciones tienen
// costo, y ninguna se puede probar sin un escritorio — salvo acá, con los
// entornos tal como los cosecha `harvestSessionEnv` de equipos reales.

func TestSesionEsWayland(t *testing.T) {
	casos := []struct {
		nombre string
		env    []string
		quiere bool
	}{
		{
			// Sesión X11 clásica. Medido en un equipo con LightDM: el display era
			// `:10`, no `:0`.
			nombre: "X11 puro",
			env:    []string{"HOME=/home/ana", "DISPLAY=:10", "XAUTHORITY=/home/ana/.Xauthority"},
			quiere: false,
		},
		{
			nombre: "Wayland puro",
			env:    []string{"HOME=/home/ana", "WAYLAND_DISPLAY=wayland-0", "XDG_SESSION_TYPE=wayland"},
			quiere: true,
		},
		{
			// XWayland: hay las dos variables. Se elige X11 porque `scrot` SÍ
			// captura las ventanas X11 ahí, y algo es mejor que nada.
			nombre: "XWayland (las dos variables)",
			env:    []string{"DISPLAY=:0", "WAYLAND_DISPLAY=wayland-0", "XDG_SESSION_TYPE=wayland"},
			quiere: false,
		},
		{
			// El respaldo `DISPLAY=:0` que pone findUserSession cuando no cosechó
			// nada: sin DISPLAY real pero con el tipo declarado por logind.
			nombre: "sólo XDG_SESSION_TYPE",
			env:    []string{"HOME=/home/ana", "XDG_SESSION_TYPE=Wayland"},
			quiere: true,
		},
		{
			// Variables presentes pero VACÍAS: pasa cuando el proceso del que se
			// cosechó tenía la variable declarada sin valor. No es una sesión
			// Wayland.
			nombre: "variables vacías",
			env:    []string{"DISPLAY=", "WAYLAND_DISPLAY=", "XDG_SESSION_TYPE="},
			quiere: false,
		},
		{
			nombre: "entorno sin nada gráfico",
			env:    []string{"HOME=/root", "PATH=/usr/bin"},
			quiere: false,
		},
	}

	for _, c := range casos {
		if got := sesionEsWayland(c.env); got != c.quiere {
			t.Errorf("%s: sesionEsWayland(%v) = %v, se esperaba %v", c.nombre, c.env, got, c.quiere)
		}
	}
}

func TestDirectorioCapturaUsuarioPrefiereElRuntimeDir(t *testing.T) {
	// XDG_RUNTIME_DIR sólo se acepta si EXISTE: apuntar la captura a un
	// directorio que no está haría fallar todas las capturas del equipo con un
	// "no such file", que se reportaría como `fallo_captura` sin decir por qué.
	sess := &userSession{
		UID:  1000,
		Home: t.TempDir(),
		Env:  []string{"XDG_RUNTIME_DIR=/run/user/999999-que-no-existe"},
	}

	if got := directorioCapturaUsuario(sess); got != sess.Home {
		t.Errorf("con un runtime dir inexistente hay que caer al home; se eligió %q", got)
	}

	runtimeDir := t.TempDir()
	sess.Env = []string{"XDG_RUNTIME_DIR=" + runtimeDir}
	if got := directorioCapturaUsuario(sess); got != runtimeDir {
		t.Errorf("con un runtime dir existente hay que usarlo; se eligió %q", got)
	}
}

// Salidas REALES de `xset q` tomadas del equipo de terreno (cliente-rmm,
// 2026-08-12). Se guardan textuales a propósito: el formato de xset es lo único
// que sostiene esta detección, y una prueba con un texto inventado no protegería
// contra un cambio de formato.
const xsetPantallaEncendida = `Keyboard Control:
  auto repeat:  on    key click percent:  0  LED mask:  00000000
Screen Saver:
  prefer blanking:  no    allow exposures:  no
  timeout:  300    cycle:  300
Colors:
  default colormap:  0x20    BlackPixel:  0    WhitePixel:  16777215
DPMS (Display Power Management Signaling):
  Standby: 600    Suspend: 0    Off: 900
  DPMS is Enabled
  Monitor is On
`

const xsetPantallaApagada = `DPMS (Display Power Management Signaling):
  Standby: 600    Suspend: 0    Off: 900
  DPMS is Enabled
  Monitor is Off
`

const xsetPantallaEnStandby = `DPMS (Display Power Management Signaling):
  DPMS is Enabled
  Monitor is in Standby
`

func TestPantallaApagada(t *testing.T) {
	casos := []struct {
		nombre string
		salida string
		quiere bool
	}{
		{"monitor encendido", xsetPantallaEncendida, false},
		{"monitor apagado", xsetPantallaApagada, true},
		{"monitor en standby", xsetPantallaEnStandby, true},
		// Sin DPMS no hay nada que afirmar: el motivo genérico sigue siendo el
		// honesto. Equivocarse hacia "apagada" sería inventar una explicación.
		{"salida sin DPMS", "Keyboard Control:\n  auto repeat:  on\n", false},
		{"salida vacía", "", false},
	}

	for _, c := range casos {
		if got := pantallaApagada(c.salida); got != c.quiere {
			t.Errorf("%s: pantallaApagada(...) = %v, se esperaba %v", c.nombre, got, c.quiere)
		}
	}
}
