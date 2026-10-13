package agent

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	rmm "github.com/braincorp-cl/observer-agent-dist/shared"
)

func TestNormalizarURLSinQueryNiCredenciales(t *testing.T) {
	casos := map[string]string{
		"https://docs.google.com/document/d/abc/edit?usp=sharing#heading=h.1": "docs.google.com/document/d/abc/edit",
		"https://user:clave@Example.COM/a/b/":                                 "example.com/a/b",
		"http://intranet.local:8080/":                                         "intranet.local:8080",
		"chrome://settings/":                                                  "",
		"file:///C:/Users/x/secreto.pdf":                                      "",
		"about:blank":                                                         "",
	}
	for in, want := range casos {
		if got := normalizarURL(in); got != want {
			t.Errorf("normalizarURL(%q) = %q; quiero %q", in, got, want)
		}
	}
}

func TestElegirVisitaPorTitulo(t *testing.T) {
	ahora := time.Now()
	visitas := []visitaNav{
		{"https://mail.zoho.com/zm/#mail", "Zoho Mail (persona@x.cl)", ahora.Add(-1 * time.Minute)},
		{"https://www.youtube.com/watch?v=1", "Video uno - YouTube", ahora.Add(-5 * time.Minute)},
		{"https://docs.google.com/document/d/abc/edit", "Informe - Documentos de Google", ahora.Add(-20 * time.Minute)},
	}
	if got := elegirVisita(visitas, "Informe - Documentos de Google - Google Chrome", ahora); got != "docs.google.com/document/d/abc/edit" {
		t.Fatalf("título de ventana con sufijo del navegador: %q", got)
	}
	if got := elegirVisita(visitas, "(3) Video uno - YouTube - Brave", ahora); got != "www.youtube.com/watch" {
		t.Fatalf("contador de notificaciones al inicio: %q", got)
	}
	if got := elegirVisita(visitas, "Otra cosa - Google Chrome", ahora); got != "" {
		t.Fatalf("sin coincidencia debe dar vacío: %q", got)
	}
	// Sin título (macOS): sólo la visita más reciente y si es reciente.
	if got := elegirVisita(visitas, "", ahora); got != "mail.zoho.com/zm" {
		t.Fatalf("sin título: %q", got)
	}
	viejas := []visitaNav{{"https://a.cl/x", "A", ahora.Add(-2 * time.Hour)}}
	if got := elegirVisita(viejas, "", ahora); got != "" {
		t.Fatalf("sin título y visita vieja debe dar vacío: %q", got)
	}
}

func TestClaveNavegador(t *testing.T) {
	casos := map[string]string{
		"msedge.exe": "edge", "microsoft edge": "edge", "brave browser": "brave", "google chrome": "chrome",
		"chrome": "chrome", "vivaldi-bin": "vivaldi", "firefox-esr": "firefox", "explorer": "",
	}
	for in, want := range casos {
		if got := claveNavegador(in); got != want {
			t.Errorf("claveNavegador(%q) = %q; quiero %q", in, got, want)
		}
	}
}

// Historial con el esquema de Chromium, leído como lo hace el agente (inmutable).
func TestLeerVisitasChromium(t *testing.T) {
	f := filepath.Join(t.TempDir(), "History")
	db, err := sql.Open("sqlite", f)
	if err != nil {
		t.Fatal(err)
	}
	ahora := time.Now()
	vt := func(ts time.Time) int64 { return (ts.Unix() + chromiumEpochSeg) * 1_000_000 }
	for _, q := range []string{
		`CREATE TABLE urls (id INTEGER PRIMARY KEY, url TEXT, title TEXT)`,
		`CREATE TABLE visits (id INTEGER PRIMARY KEY, url INTEGER, visit_time INTEGER)`,
		`INSERT INTO urls VALUES (1,'https://a.cl/uno?x=1','Uno'),(2,'https://b.cl/dos','Dos')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO visits VALUES (1,1,?),(2,2,?),(3,1,?)`, vt(ahora.Add(-30*time.Minute)), vt(ahora.Add(-2*time.Minute)), vt(ahora.Add(-48*time.Hour))); err != nil {
		t.Fatal(err)
	}
	db.Close()

	vs := leerVisitas(f, histChromium, ahora.Add(-urlVentanaHistorial))
	if len(vs) != 2 || vs[0].titulo != "Dos" || vs[1].titulo != "Uno" {
		t.Fatalf("visitas = %+v", vs)
	}
	if got := elegirVisita(vs, "Uno - Google Chrome", ahora); got != "a.cl/uno" {
		t.Fatalf("correlación = %q", got)
	}
}

func TestLectorEntradaDeltasYReinicio(t *testing.T) {
	var l lectorEntrada
	if a, b := l.delta(100, 50, true); a != 0 || b != 0 {
		t.Fatal("la primera lectura sólo fija la base")
	}
	if a, b := l.delta(130, 80, true); a != 30 || b != 30 {
		t.Fatalf("delta = %d %d", a, b)
	}
	// El contador se reinició (ayudante relanzado, nueva sesión).
	if a, b := l.delta(5, 2, true); a != 5 || b != 2 {
		t.Fatalf("tras reinicio = %d %d", a, b)
	}
	if a, b := l.delta(0, 0, false); a != 0 || b != 0 {
		t.Fatal("sin lectura no suma")
	}
	if a, _ := l.delta(10, 0, true); a != 0 {
		t.Fatal("tras perder la fuente, la siguiente lectura es base nueva")
	}
}

func TestParsearLineaEntrada(t *testing.T) {
	if a, b, ok := parsearLineaEntrada("12 345"); !ok || a != 12 || b != 345 {
		t.Fatal("línea válida")
	}
	for _, l := range []string{"", "error sin-hooks", "1 2 3", "x 2"} {
		if _, _, ok := parsearLineaEntrada(l); ok {
			t.Fatalf("línea inválida aceptada: %q", l)
		}
	}
}

func TestClasificarEventoLinuxSinCodigoDeTecla(t *testing.T) {
	if clasificarEvento(0x01, 30, 1) != 1 { // KEY_A pulsada
		t.Fatal("tecla")
	}
	if clasificarEvento(0x01, 30, 2) != 0 || clasificarEvento(0x01, 30, 0) != 0 {
		t.Fatal("repetición o liberación no cuentan")
	}
	if clasificarEvento(0x01, 0x110, 1) != 2 || clasificarEvento(0x02, 0x00, 5) != 2 {
		t.Fatal("botón y movimiento de mouse")
	}
	if clasificarEvento(0x04, 4, 1) != 0 {
		t.Fatal("EV_MSC no cuenta")
	}
}

func TestAcumuladorInactivoPorAppURLYEntrada(t *testing.T) {
	ac := nuevoAcumulador("eq", "linux", "-03:00")
	ts := time.Date(2026, 10, 13, 10, 5, 0, 0, time.Local)
	ac.Agregar(ts, "u", "chrome", "Uno - Google Chrome", true, 60)
	ac.AgregarURL(ts, "u", "a.cl/uno", true, 60)
	ac.Agregar(ts.Add(time.Minute), "u", "chrome", "Uno - Google Chrome", false, 60)
	ac.AgregarURL(ts.Add(time.Minute), "u", "a.cl/uno", false, 60)
	ac.Agregar(ts.Add(2*time.Minute), "u", "", "", false, 60) // bloqueado: sin app
	ac.AgregarEntrada(ts, "u", 40, 10)
	ac.AgregarEntrada(ts.Add(time.Minute), "u", 0, 0)

	// Reclasificar mueve el minuto activo a inactivo en proceso, sitio y URL.
	ac.Reclasificar(ts, "u", "chrome", "Uno - Google Chrome", 60)
	ac.ReclasificarURL(ts, "u", "a.cl/uno", 60)

	res, _ := ac.ResumenDe("2026-10-13", true)
	u := res.Dias[0].Usuarios[0]
	if u.ProcsI["chrome"] != 2 || u.Procs["chrome"] != 0 {
		t.Fatalf("procs=%v procs_i=%v", u.Procs, u.ProcsI)
	}
	if u.UrlsI["a.cl/uno"] != 2 || len(u.Urls) != 0 {
		t.Fatalf("urls=%v urls_i=%v", u.Urls, u.UrlsI)
	}
	if u.SitiosI["Uno"] != 2 {
		t.Fatalf("sitios_i=%v", u.SitiosI)
	}
	if u.Teclas != 40 || u.Mouse != 10 || u.Entrada["60"] != [2]int{40, 10} {
		t.Fatalf("entrada=%d/%d %v", u.Teclas, u.Mouse, u.Entrada)
	}
}

func TestCapturaManualRespetaBloqueoYSesion(t *testing.T) {
	llamada := false
	capturar := func() string { llamada = true; return "ok" }
	if motivoCapturaManual(rmm.ActividadMuestra{Usuario: "u", Bloqueado: true}, capturar) != "bloqueado" || llamada {
		t.Fatal("con la sesión bloqueada no se captura")
	}
	if motivoCapturaManual(rmm.ActividadMuestra{Error: "sin display"}, capturar) != "sin_sesion" || llamada {
		t.Fatal("sin sesión no se captura")
	}
	if motivoCapturaManual(rmm.ActividadMuestra{Usuario: "u"}, capturar) != "ok" || !llamada {
		t.Fatal("con sesión libre se captura")
	}
}
