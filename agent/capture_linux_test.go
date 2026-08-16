//go:build linux
// +build linux

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

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

// fakeLoginctl deja en PATH un `loginctl` de mentira que imprime `salida` y sale
// con `rc`. Es la única forma de cubrir `sesionBloqueada` sin un logind real.
//
// 🪤 DOS TRAMPAS QUE COSTÓ VER, las dos del andamiaje y no del código:
//
//  1. `printf '%s' "...\n"` NO sirve: /bin/sh no interpreta los escapes del
//     argumento, así que el salto llegaba como barra literal. Por eso la salida
//     va entre comillas simples, con el salto de línea real adentro.
//  2. El script NO puede llamar a `cat` ni a nada externo: el test deja en PATH
//     sólo este directorio, así que cualquier binario de fuera falla — y el
//     `exit 0` de la última línea tapaba la falla dejando la salida vacía.
//     `printf` es builtin de sh y no tiene ese problema.
func fakeLoginctl(t *testing.T, salida string, rc int) {
	t.Helper()

	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s' " + comillaSh(salida) + "\nexit " + strconv.Itoa(rc) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "loginctl"), []byte(script), 0o755); err != nil {
		t.Fatalf("no se pudo escribir el loginctl de prueba: %v", err)
	}

	t.Setenv("PATH", dir)
}

// comillaSh envuelve un texto en comillas simples de shell, escapando las que
// ya trae. Preserva los bytes tal cual, saltos de línea incluidos.
func comillaSh(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestSesionBloqueada(t *testing.T) {
	casos := []struct {
		nombre    string
		sessionID string
		salida    string
		rc        int
		quiere    bool
	}{
		// Salida real de `loginctl show-session 2 --property=LockedHint` en el
		// Fedora 44 KDE Wayland donde se midió esto (2026-08-15).
		{"bloqueada", "2", "LockedHint=yes\n", 0, true},
		{"desbloqueada", "2", "LockedHint=no\n", 0, false},
		// Sin ID de sesión no hay a quién preguntarle. Devolver true acá sería
		// inventar el diagnóstico más útil en vez del verdadero.
		{"sin ID de sesión", "", "LockedHint=yes\n", 0, false},
		{"loginctl falla", "2", "", 1, false},
		{"salida sin la propiedad", "2", "Id=2\nActive=yes\n", 0, false},
		{"salida vacía", "2", "", 0, false},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			fakeLoginctl(t, c.salida, c.rc)

			a := &Agent{Logger: loggerMudo()}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if got := a.sesionBloqueada(ctx, c.sessionID); got != c.quiere {
				t.Errorf("sesionBloqueada(%q) = %v, se esperaba %v", c.sessionID, got, c.quiere)
			}
		})
	}
}

// Sin `loginctl` en PATH la consulta no puede hacerse, y el diagnóstico tiene que
// seguir por las otras vías en vez de afirmar nada.
func TestSesionBloqueadaSinLoginctl(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	a := &Agent{Logger: loggerMudo()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if a.sesionBloqueada(ctx, "2") {
		t.Error("sin loginctl, sesionBloqueada debe devolver false")
	}
}

// TestEsWaylandLePrefiereALogind fija la regresión medida el 2026-08-15 en un
// Fedora 44 KDE Plasma 6: la sesión era Wayland pura y el agente se iba por la
// rama X11 porque XWayland exporta `DISPLAY=:0`. El caso "wayland con DISPLAY de
// XWayland" es EL caso — si vuelve a dar false, la flota KDE y GNOME captura por
// una vía que ahí no entrega nada.
func TestEsWaylandLePrefiereALogind(t *testing.T) {
	// Entorno real de `plasmashell` en esa máquina.
	plasma6 := []string{
		"DISPLAY=:0",
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_SESSION_TYPE=wayland",
		"XDG_CURRENT_DESKTOP=KDE",
	}

	casos := []struct {
		nombre string
		tipo   string
		env    []string
		quiere bool
	}{
		{"logind dice wayland y XWayland puso DISPLAY", "wayland", plasma6, true},
		{"logind dice wayland aunque el entorno cosechado venga pelado", "wayland", nil, true},
		// Al revés también manda logind: un WAYLAND_DISPLAY colgado en el entorno
		// de una sesión X11 no debe desviar la captura.
		{"logind dice x11", "x11", []string{"DISPLAY=:0", "WAYLAND_DISPLAY=wayland-0"}, false},
		{"logind dice x11 con el entorno pelado", "x11", nil, false},
		// Mayúsculas y espacios: la salida de loginctl es texto y no vale
		// confiarse. Ver la regla de comparaciones invariantes de la flota.
		{"logind contesta con mayúsculas", "Wayland", plasma6, true},
		// Sin dato de logind (equipo sin systemd, o vino del escaneo de /proc)
		// manda la heurística de siempre.
		{"sin tipo, entorno X11", "", []string{"DISPLAY=:0"}, false},
		{"sin tipo, entorno Wayland", "", []string{"WAYLAND_DISPLAY=wayland-0"}, true},
		{"sin tipo, entorno vacío", "", nil, false},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			s := &userSession{SessionType: c.tipo, Env: c.env}
			if got := s.esWayland(); got != c.quiere {
				t.Errorf("esWayland() con Type=%q = %v, se esperaba %v", c.tipo, got, c.quiere)
			}
		})
	}
}
