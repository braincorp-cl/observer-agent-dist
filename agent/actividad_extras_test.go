package agent

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
	"time"

	psnet "github.com/shirou/gopsutil/v3/net"
)

func TestSitioDeTitulo(t *testing.T) {
	casos := []struct{ proc, titulo, quiero string }{
		{"brave.exe", "Recibidos (940) - jptamayo@gmail.com - Gmail - Brave", "Gmail"},
		{"chrome", "Keep Active Employee Monitoring Software - Google Chrome", "Keep Active Employee Monitoring Software"},
		{"chrome.exe", "(21) Flova AI tutorial - YouTube - Google Chrome", "YouTube"},
		{"firefox", "Pull requests — Mozilla Firefox", "Pull requests"},
		{"msedge.exe", "Outlook - Microsoft​ Edge", "Outlook"},
		{"chrome", "Nueva pestaña - Google Chrome", ""},
		{"mstsc.exe", "10.20.0.247 - Conexión a Escritorio remoto", ""},
		{"chrome", "", ""},
	}
	for _, c := range casos {
		if got := sitioDeTitulo(c.proc, c.titulo); got != c.quiero {
			t.Errorf("sitioDeTitulo(%q, %q) = %q, quiero %q", c.proc, c.titulo, got, c.quiero)
		}
	}
}

func TestAgruparConexiones(t *testing.T) {
	conns := []psnet.ConnectionStat{
		{Pid: 10, Status: "ESTABLISHED", Raddr: psnet.Addr{IP: "142.250.0.1", Port: 443}},
		{Pid: 10, Status: "ESTABLISHED", Raddr: psnet.Addr{IP: "142.250.0.1", Port: 443}},
		{Pid: 10, Status: "ESTABLISHED", Raddr: psnet.Addr{IP: "1.1.1.1", Port: 443}},
		{Pid: 10, Status: "LISTEN", Raddr: psnet.Addr{IP: ""}},
		{Pid: 11, Status: "ESTABLISHED", Raddr: psnet.Addr{IP: "127.0.0.1", Port: 80}},
		{Pid: 0, Status: "ESTABLISHED", Raddr: psnet.Addr{IP: "8.8.8.8", Port: 53}},
	}
	nombres := map[int32]string{10: "chrome", 11: "local"}
	got := agruparConexiones(conns, func(p int32) string { return nombres[p] })
	if len(got) != 1 {
		t.Fatalf("solo chrome tiene conexiones externas: %+v", got)
	}
	if got["chrome"].Conex != 3 || len(got["chrome"].Destinos) != 2 {
		t.Fatalf("chrome: %+v", got["chrome"])
	}
}

func TestAcumuladorSitiosYRed(t *testing.T) {
	ac := nuevoAcumulador("eq", "linux", "-03:00")
	ts := time.Date(2026, 10, 11, 10, 0, 0, 0, time.Local)
	for i := 0; i < 4; i++ {
		ac.Agregar(ts.Add(time.Duration(i)*time.Minute), "ana", "chrome", "Bandeja - Gmail - Google Chrome", true, 60)
		ac.AgregarRed(ts.Add(time.Duration(i)*time.Minute), "ana", map[string]appRed{
			"chrome": {Conex: 2 + i, Destinos: []string{"1.1.1.1", "8.8.8.8"}},
		}, 60)
	}
	res, ok := ac.ResumenDe("2026-10-11", true)
	if !ok {
		t.Fatal("sin resumen")
	}
	u := res.Dias[0].Usuarios[0]
	if u.Sitios["Gmail"] != 4 {
		t.Fatalf("sitios: %+v", u.Sitios)
	}
	r := u.Red["chrome"]
	if r.Min != 4 || r.ConexMax != 5 || r.Destinos != 2 {
		t.Fatalf("red: %+v", r)
	}
	// El JSON lleva las claves nuevas con su nombre de contrato.
	b, _ := json.Marshal(u)
	if !bytes.Contains(b, []byte(`"sitios"`)) || !bytes.Contains(b, []byte(`"conex_max"`)) {
		t.Fatalf("json sin claves 049-C: %s", b)
	}
}

func TestAJPEGReducido(t *testing.T) {
	// Contenido "fotográfico" (ruido), que es donde el JPEG gana de verdad.
	img := image.NewRGBA(image.Rect(0, 0, 2732, 1536))
	seed := uint32(7)
	for y := 0; y < 1536; y++ {
		for x := 0; x < 2732; x++ {
			seed = seed*1664525 + 1013904223
			img.Set(x, y, color.RGBA{uint8(seed >> 24), uint8(seed >> 16), uint8(x), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	out, ext := aJPEGReducido(buf.Bytes())
	if ext != "jpg" {
		t.Fatalf("ext %s", ext)
	}
	dec, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if dec.Bounds().Dx() != capturaActAnchoMax || dec.Bounds().Dy() != 1536*capturaActAnchoMax/2732 {
		t.Fatalf("tamaño %v", dec.Bounds())
	}
	if len(out) >= buf.Len() {
		t.Fatalf("el JPEG (%d) no es más liviano que el PNG (%d)", len(out), buf.Len())
	}
	// Pantalla plana: el PNG original es más liviano y se conserva.
	plano := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	var pb bytes.Buffer
	_ = png.Encode(&pb, plano)
	if o, e := aJPEGReducido(pb.Bytes()); e != "png" || len(o) != pb.Len() {
		t.Fatalf("pantalla plana: debía quedar el PNG (%s, %d)", e, len(o))
	}
	// No-PNG pasa intacto.
	if o, e := aJPEGReducido([]byte("xx")); e != "png" || string(o) != "xx" {
		t.Fatal("un no-PNG debe pasar intacto")
	}
}

func TestExtrasDeConfigYPersistencia(t *testing.T) {
	a := agenteActividadDePrueba(t)
	actMu.Lock()
	actCapturasMin, actRed = 0, false
	actMu.Unlock()
	t.Cleanup(func() { actMu.Lock(); actCapturasMin, actRed = 0, false; actMu.Unlock() })

	var conf AgentCheckInConfig
	raw := `{"actividad_active":true,"actividad_interval_seg":60,"actividad_capturas_min":10,"actividad_red":true}`
	if err := json.Unmarshal([]byte(raw), &conf); err != nil {
		t.Fatal(err)
	}
	a.aplicarActividadDeConfig(conf)
	if c, r := a.getActividadExtras(); c != 10 || !r {
		t.Fatalf("extras: %d %v", c, r)
	}
	st, ok := a.loadActividadCfg()
	if !ok || st.CapturasMin != 10 || !st.Red {
		t.Fatalf("persistido: %+v", st)
	}
	// Servidor viejo: sin claves => capturas y red apagadas.
	var viejo AgentCheckInConfig
	_ = json.Unmarshal([]byte(`{"actividad_active":true}`), &viejo)
	a.aplicarActividadDeConfig(viejo)
	if c, r := a.getActividadExtras(); c != 0 || r {
		t.Fatalf("servidor viejo encendió extras: %d %v", c, r)
	}
}
