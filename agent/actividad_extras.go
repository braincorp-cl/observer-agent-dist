package agent

// Feature 049-C · sitios web por título y uso de red por aplicación.
//
// Sitios: el agente no lee el historial ni intercepta tráfico; deduce el sitio
// del TÍTULO de la ventana activa del navegador, que ya registra cuando la
// consola pide títulos. Los sitios ponen su marca al final ("Recibidos - Gmail",
// "Video - YouTube") y el navegador agrega la suya después ("… - Google Chrome");
// se quita la del navegador y se toma el último segmento. Es una heurística: sin
// títulos no hay sitios, y una página sin marca da su propio título.
//
// Red: conexiones TCP establecidas por proceso (gopsutil, nativo en los 3 SO).
// Mide minutos con red, pico de conexiones simultáneas y destinos distintos. NO
// mide bytes: no hay una vía nativa y uniforme por proceso sin drivers ni
// binarios del sistema.

import (
	"net"
	"regexp"
	"strings"
	"sync"

	psnet "github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

const (
	topSitiosResumen   = 25
	topRedResumen      = 25
	maxDestinosPorApp  = 200
	maxLargoNombreSito = 60
)

var navegadores = map[string]bool{
	"chrome": true, "google chrome": true, "chromium": true, "chromium-browser": true,
	"msedge": true, "microsoft edge": true, "firefox": true, "firefox-esr": true,
	"brave": true, "brave-browser": true, "opera": true, "vivaldi": true,
	"safari": true, "arc": true, "librewolf": true, "waterfox": true, "yandex": true,
}

// Sufijos con que cada navegador termina el título (sin el separador).
var marcasNavegador = []string{
	"google chrome", "chromium", "microsoft edge", "microsoft​ edge",
	"mozilla firefox", "firefox", "brave", "opera", "vivaldi", "safari", "arc",
	"librewolf", "waterfox", "yandex browser",
}

var (
	reContador  = regexp.MustCompile(`^\(\d+\+?\)\s*`)
	reSeparador = regexp.MustCompile(`\s+[-–—|]\s+`)
)

var paginasVacias = map[string]bool{
	"": true, "new tab": true, "nueva pestaña": true, "nueva pestana": true,
	"untitled": true, "sin título": true, "start page": true, "inicio": true,
}

func esNavegador(proceso string) bool {
	p := strings.ToLower(strings.TrimSpace(proceso))
	p = strings.TrimSuffix(p, ".exe")
	return navegadores[p]
}

// sitioDeTitulo devuelve el sitio deducido del título, o "" si no aplica.
func sitioDeTitulo(proceso, titulo string) string {
	if !esNavegador(proceso) {
		return ""
	}
	t := strings.TrimSpace(titulo)
	if t == "" {
		return ""
	}
	partes := reSeparador.Split(t, -1)
	// Quitar la marca del navegador (y el perfil, "Juan - Google Chrome").
	if n := len(partes); n > 0 {
		ult := strings.ToLower(strings.TrimSpace(partes[n-1]))
		for _, m := range marcasNavegador {
			if ult == m {
				partes = partes[:n-1]
				break
			}
		}
	}
	if len(partes) == 0 {
		return ""
	}
	sitio := strings.TrimSpace(partes[len(partes)-1])
	sitio = reContador.ReplaceAllString(sitio, "")
	if paginasVacias[strings.ToLower(sitio)] {
		return ""
	}
	if r := []rune(sitio); len(r) > maxLargoNombreSito {
		sitio = string(r[:maxLargoNombreSito])
	}
	return sitio
}

// appRed es la foto de la red de un proceso en una muestra.
type appRed struct {
	Conex    int
	Destinos []string
}

var (
	nombreProcMu    sync.Mutex
	nombreProcCache = map[int32]string{}
)

func nombreDeProceso(pid int32) string {
	nombreProcMu.Lock()
	defer nombreProcMu.Unlock()
	if n, ok := nombreProcCache[pid]; ok {
		return n
	}
	n := ""
	if p, err := process.NewProcess(pid); err == nil {
		if nn, err := p.Name(); err == nil {
			n = strings.ToLower(nn)
		}
	}
	if len(nombreProcCache) > 4096 {
		nombreProcCache = map[int32]string{}
	}
	nombreProcCache[pid] = n
	return n
}

// destinoExterno filtra loopback, enlaces locales y direcciones vacías.
func destinoExterno(ip string) bool {
	p := net.ParseIP(ip)
	if p == nil {
		return false
	}
	return !(p.IsLoopback() || p.IsUnspecified() || p.IsLinkLocalUnicast() || p.IsMulticast())
}

// agruparConexiones arma la foto por proceso. Pura para poder probarla.
func agruparConexiones(conns []psnet.ConnectionStat, nombre func(int32) string) map[string]appRed {
	out := map[string]appRed{}
	vistos := map[string]map[string]bool{}
	for _, c := range conns {
		if c.Pid <= 0 || c.Status != "ESTABLISHED" || !destinoExterno(c.Raddr.IP) {
			continue
		}
		app := nombre(c.Pid)
		if app == "" {
			continue
		}
		r := out[app]
		r.Conex++
		if vistos[app] == nil {
			vistos[app] = map[string]bool{}
		}
		if !vistos[app][c.Raddr.IP] {
			vistos[app][c.Raddr.IP] = true
			r.Destinos = append(r.Destinos, c.Raddr.IP)
		}
		out[app] = r
	}
	return out
}

// muestraRedPorApp toma la foto de las conexiones del equipo. La corre el
// servicio (root/SYSTEM), que ve los sockets de todos los procesos.
func muestraRedPorApp() map[string]appRed {
	conns, err := psnet.Connections("tcp")
	if err != nil {
		return nil
	}
	return agruparConexiones(conns, nombreDeProceso)
}
