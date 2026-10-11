package agent

// Feature 049 · Comprehensive Employee Monitoring — muestra instantánea de actividad.
//
// La primitiva corre en el CONTEXTO de la sesión del usuario: el agente (servicio)
// se re-ejecuta como el usuario con `-m actividad-muestra` (mismo patrón que la
// captura de pantalla, ver capture_windows.go / capture_linux_wayland.go), porque
// leer la ventana activa y la inactividad exige el DISPLAY / bus de sesión del
// usuario. Cada plataforma implementa `muestraActividadImpl`; acá va sólo lo común.

import (
	"encoding/json"
	"fmt"
	"os/user"
	"runtime"
	"strings"
	"time"

	rmm "github.com/braincorp-cl/observer-agent-dist/shared"
)

// MuestraActividad toma una muestra instantánea en el contexto actual.
func MuestraActividad(conTitulo bool) rmm.ActividadMuestra {
	m := muestraActividadImpl(conTitulo)
	m.TS = time.Now().Format(time.RFC3339)
	if m.SO == "" {
		m.SO = soActividad()
	}
	if m.Usuario == "" {
		if u, err := user.Current(); err == nil {
			m.Usuario = u.Username
		}
	}
	return m
}

func soActividad() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "macos"
	default:
		return "linux"
	}
}

// ModoActividadMuestra es el cuerpo del modo `-m actividad-muestra`: imprime la
// muestra como UNA línea JSON y nada más. Mismo contrato de "una línea parseable,
// sin i18n" que la sonda de geolocalización (025) y la captura (030).
func ModoActividadMuestra(conTitulo bool) {
	b, err := json.Marshal(MuestraActividad(conTitulo))
	if err != nil {
		fmt.Printf("{\"error\":%q}\n", err.Error())
		return
	}
	fmt.Println(string(b))
}

// limpiarTituloActividad normaliza el título de la ventana activa: sin tabs ni
// saltos, recortado. Común a todas las plataformas.
func limpiarTituloActividad(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
