//go:build linux
// +build linux

package agent

// Primitiva de actividad en Linux, nativa y SIN binarios del SO (el canario de la
// 048 mostró que en sitios con egress filtrado no se puede instalar xprintidle/
// xdotool). Dos fuentes:
//
//   - Inactividad y bloqueo: D-Bus de sesión (reutiliza godbus, ya presente). Sirve
//     en X11 y en Wayland sobre GNOME/KDE. Fallback a la extensión XScreenSaver por
//     el protocolo X11 (jezek/xgb, Go puro, sin cgo).
//   - Ventana/proceso activo: protocolo X11 puro (xgb), leyendo _NET_ACTIVE_WINDOW.
//     En Wayland NO hay API genérica para clientes (GNOME no la expone): se marca
//     "no disponible" y se devuelve igual idle + bloqueo.

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/godbus/dbus/v5"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/screensaver"
	"github.com/jezek/xgb/xproto"

	rmm "github.com/braincorp-cl/observer-agent-dist/shared"
)

func muestraActividadImpl(conTitulo bool) rmm.ActividadMuestra {
	m := rmm.ActividadMuestra{SO: "linux", IdleSeg: -1}
	m.Sesion = strings.ToLower(os.Getenv("XDG_SESSION_TYPE"))
	if m.Sesion == "" {
		// XDG_SESSION_TYPE puede faltar en el env cosechado; inferir por los
		// sockets del servidor gráfico para no tratar una sesión Wayland como X11.
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			m.Sesion = "wayland"
		} else if os.Getenv("DISPLAY") != "" {
			m.Sesion = "x11"
		}
	}

	if idle, ok := idleDBusLinux(); ok {
		m.IdleSeg = idle
	}
	m.Bloqueado = bloqueadoDBusLinux()

	if m.Sesion == "wayland" {
		m.Nota = "wayland: ventana activa no disponible"
		return m
	}

	if err := ventanaActivaX11(&m, conTitulo); err != nil && m.Error == "" {
		m.Error = err.Error()
	}
	if m.IdleSeg < 0 {
		if idle, ok := idleXScreenSaverLinux(); ok {
			m.IdleSeg = idle
		}
	}
	return m
}

// idleDBusLinux pregunta la inactividad por D-Bus. Primero GNOME (Mutter, ms),
// luego el estándar freedesktop ScreenSaver (segundos).
func idleDBusLinux() (int, bool) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return 0, false
	}
	defer conn.Close()

	var ms uint64
	mutter := conn.Object("org.gnome.Mutter.IdleMonitor", dbus.ObjectPath("/org/gnome/Mutter/IdleMonitor/Core"))
	if err := mutter.Call("org.gnome.Mutter.IdleMonitor.GetIdletime", 0).Store(&ms); err == nil {
		return int(ms / 1000), true
	}

	var secs uint32
	ss := conn.Object("org.freedesktop.ScreenSaver", dbus.ObjectPath("/org/freedesktop/ScreenSaver"))
	if err := ss.Call("org.freedesktop.ScreenSaver.GetSessionIdleTime", 0).Store(&secs); err == nil {
		return int(secs), true
	}
	return 0, false
}

func bloqueadoDBusLinux() bool {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return false
	}
	defer conn.Close()
	ss := conn.Object("org.freedesktop.ScreenSaver", dbus.ObjectPath("/org/freedesktop/ScreenSaver"))
	var activo bool
	if err := ss.Call("org.freedesktop.ScreenSaver.GetActive", 0).Store(&activo); err == nil {
		return activo
	}
	return false
}

// idleXScreenSaverLinux es el fallback X11 puro cuando no hay idle por D-Bus.
func idleXScreenSaverLinux() (int, bool) {
	X, err := xgb.NewConn()
	if err != nil {
		return 0, false
	}
	defer X.Close()
	if err := screensaver.Init(X); err != nil {
		return 0, false
	}
	root := xproto.Setup(X).DefaultScreen(X).Root
	info, err := screensaver.QueryInfo(X, xproto.Drawable(root)).Reply()
	if err != nil || info == nil {
		return 0, false
	}
	return int(info.MsSinceUserInput / 1000), true
}

func ventanaActivaX11(m *rmm.ActividadMuestra, conTitulo bool) error {
	X, err := xgb.NewConn()
	if err != nil {
		return fmt.Errorf("x11: %w", err)
	}
	defer X.Close()
	root := xproto.Setup(X).DefaultScreen(X).Root

	atom := func(name string) xproto.Atom {
		r, err := xproto.InternAtom(X, true, uint16(len(name)), name).Reply()
		if err != nil || r == nil {
			return 0
		}
		return r.Atom
	}

	aActive := atom("_NET_ACTIVE_WINDOW")
	if aActive == 0 {
		return fmt.Errorf("x11: sin _NET_ACTIVE_WINDOW")
	}
	pr, err := xproto.GetProperty(X, false, root, aActive, xproto.AtomWindow, 0, 1).Reply()
	if err != nil || pr == nil || len(pr.Value) < 4 {
		return fmt.Errorf("x11: no se pudo leer la ventana activa")
	}
	win := xproto.Window(xgb.Get32(pr.Value))
	if win == 0 {
		m.Proceso = "(escritorio)"
		return nil
	}

	if aPID := atom("_NET_WM_PID"); aPID != 0 {
		if r, e := xproto.GetProperty(X, false, win, aPID, xproto.AtomCardinal, 0, 1).Reply(); e == nil && r != nil && len(r.Value) >= 4 {
			m.PID = int(xgb.Get32(r.Value))
			if n := procComm(m.PID); n != "" {
				m.Proceso = n
			}
		}
	}

	if conTitulo {
		if aName := atom("_NET_WM_NAME"); aName != 0 {
			aUTF8 := atom("UTF8_STRING")
			if r, e := xproto.GetProperty(X, false, win, aName, aUTF8, 0, 256).Reply(); e == nil && r != nil && len(r.Value) > 0 {
				m.Titulo = limpiarTituloActividad(string(r.Value))
			}
		}
		if m.Titulo == "" {
			if r, e := xproto.GetProperty(X, false, win, xproto.AtomWmName, xproto.AtomString, 0, 256).Reply(); e == nil && r != nil && len(r.Value) > 0 {
				m.Titulo = limpiarTituloActividad(string(r.Value))
			}
		}
	}

	if m.Proceso == "" {
		m.Proceso = "(desconocido)"
	}
	return nil
}

// procComm devuelve el nombre del ejecutable de un PID, en minúsculas, leyendo
// /proc/<pid>/comm. Sin binarios externos.
func procComm(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(string(b)))
}
