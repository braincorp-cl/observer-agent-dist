package agent

// Feature 049-E · URL de la pestaña activa, sin extensión de navegador.
//
// Corre dentro del self-exec de la muestra (sesión del usuario): lee en modo
// sólo-lectura e inmutable el historial del navegador que está al frente
// (Chromium y derivados, Firefox) y elige la visita más reciente cuyo título
// coincide con el de la ventana. Safari no: su historial exige Acceso total al
// disco. Se guarda sólo dominio + ruta (sin ?query ni #fragmento ni credenciales):
// decisión de privacidad del 12-oct-2026.

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	urlVentanaHistorial = 12 * time.Hour
	urlSinTituloMax     = 30 * time.Minute // macOS sin título: sólo una visita reciente
	urlLargoMax         = 200
	urlPerfilesMax      = 3
)

type visitaNav struct {
	url    string
	titulo string
	ts     time.Time
}

type tipoHistorial int

const (
	histChromium tipoHistorial = iota
	histFirefox
)

type navegadorHist struct {
	tipo tipoHistorial
	// carpetas raíz relativas por SO; los perfiles cuelgan de ellas.
	win, mac, linux []string
}

// Clave = nombre de proceso normalizado (ver claveNavegador).
var historiales = map[string]navegadorHist{
	"chrome":   {histChromium, []string{`Google\Chrome\User Data`}, []string{"Google/Chrome"}, []string{"google-chrome"}},
	"edge":     {histChromium, []string{`Microsoft\Edge\User Data`}, []string{"Microsoft Edge"}, []string{"microsoft-edge"}},
	"brave":    {histChromium, []string{`BraveSoftware\Brave-Browser\User Data`}, []string{"BraveSoftware/Brave-Browser"}, []string{"BraveSoftware/Brave-Browser"}},
	"vivaldi":  {histChromium, []string{`Vivaldi\User Data`}, []string{"Vivaldi"}, []string{"vivaldi"}},
	"opera":    {histChromium, []string{`Opera Software\Opera Stable`}, []string{"com.operasoftware.Opera"}, []string{"opera"}},
	"chromium": {histChromium, []string{`Chromium\User Data`}, []string{"Chromium"}, []string{"chromium"}},
	"firefox":  {histFirefox, []string{`Mozilla\Firefox\Profiles`}, []string{"Firefox/Profiles"}, []string{".mozilla/firefox"}},
}

// claveNavegador lleva el proceso al navegador de la tabla (cada SO lo nombra
// distinto: msedge / microsoft edge, brave / brave browser, vivaldi-bin...).
func claveNavegador(proceso string) string {
	p := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(proceso)), ".exe")
	switch {
	case strings.Contains(p, "edge"):
		return "edge"
	case strings.Contains(p, "brave"):
		return "brave"
	case strings.Contains(p, "vivaldi"):
		return "vivaldi"
	case strings.Contains(p, "opera"):
		return "opera"
	case strings.Contains(p, "chromium"):
		return "chromium"
	case strings.Contains(p, "chrome"):
		return "chrome"
	case strings.Contains(p, "firefox"), p == "librewolf", p == "waterfox":
		return "firefox"
	}
	return ""
}

// normalizarURL deja dominio + ruta. Sólo http(s); nada de query, fragmento,
// usuario/clave ni páginas internas del navegador o archivos locales.
func normalizarURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	s := strings.ToLower(u.Host) + u.EscapedPath()
	s = strings.TrimSuffix(s, "/")
	if r := []rune(s); len(r) > urlLargoMax {
		s = string(r[:urlLargoMax])
	}
	return s
}

func normTitulo(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// elegirVisita aplica la correlación título de ventana ↔ historial. Las visitas
// vienen de la más reciente a la más antigua.
func elegirVisita(visitas []visitaNav, tituloVentana string, ahora time.Time) string {
	tv := normTitulo(reContador.ReplaceAllString(strings.TrimSpace(tituloVentana), ""))
	if tv == "" {
		// Sin título (macOS sin Accesibilidad): sólo si la última visita es reciente.
		for _, v := range visitas {
			if ahora.Sub(v.ts) <= urlSinTituloMax {
				if u := normalizarURL(v.url); u != "" {
					return u
				}
			}
			break
		}
		return ""
	}
	for _, v := range visitas {
		th := normTitulo(v.titulo)
		if th == "" {
			continue
		}
		// La ventana es "título - Navegador" (y se recorta a 200 caracteres).
		if strings.HasPrefix(tv, th) || (len(tv) >= 150 && strings.HasPrefix(th, tv)) {
			if u := normalizarURL(v.url); u != "" {
				return u
			}
		}
	}
	return ""
}

// carpetasBase del usuario actual según el SO.
func carpetasBase(n navegadorHist) []string {
	home, _ := os.UserHomeDir()
	var out []string
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		for _, r := range n.win {
			b := base
			if n.tipo == histFirefox || strings.HasPrefix(r, "Opera") {
				b = os.Getenv("APPDATA")
			}
			if b != "" {
				out = append(out, filepath.Join(b, r))
			}
		}
	case "darwin":
		for _, r := range n.mac {
			out = append(out, filepath.Join(home, "Library", "Application Support", r))
		}
	default:
		for _, r := range n.linux {
			if strings.HasPrefix(r, ".") {
				out = append(out, filepath.Join(home, r))
			} else {
				out = append(out, filepath.Join(home, ".config", r))
			}
		}
	}
	return out
}

// archivosHistorial devuelve los historiales de los perfiles del navegador,
// del modificado más recientemente al más antiguo (el perfil en uso primero).
func archivosHistorial(n navegadorHist, base string) []string {
	var cand []string
	if n.tipo == histFirefox {
		cand, _ = filepath.Glob(filepath.Join(base, "*", "places.sqlite"))
	} else {
		cand = append(cand, filepath.Join(base, "History")) // Opera: el perfil es la raíz
		for _, pat := range []string{"Default", "Profile *", "Guest Profile"} {
			m, _ := filepath.Glob(filepath.Join(base, pat, "History"))
			cand = append(cand, m...)
		}
	}
	type fm struct {
		p string
		t time.Time
	}
	var arr []fm
	for _, p := range cand {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			arr = append(arr, fm{p, st.ModTime()})
		}
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].t.After(arr[j].t) })
	var out []string
	for i := 0; i < len(arr) && i < urlPerfilesMax; i++ {
		out = append(out, arr[i].p)
	}
	return out
}

// urlActiva devuelve la URL (dominio + ruta) de la pestaña al frente, o "".
func urlActiva(proceso, titulo string) string {
	n, ok := historiales[claveNavegador(proceso)]
	if !ok {
		return ""
	}
	ahora := time.Now()
	desde := ahora.Add(-urlVentanaHistorial)
	for _, base := range carpetasBase(n) {
		for _, f := range archivosHistorial(n, base) {
			if u := elegirVisita(leerVisitas(f, n.tipo, desde), titulo, ahora); u != "" {
				return u
			}
		}
	}
	return ""
}
