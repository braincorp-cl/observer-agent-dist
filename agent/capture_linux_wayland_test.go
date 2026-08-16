//go:build linux
// +build linux

package agent

import (
	"os"
	"testing"
	"time"
)

// Feature 030 · Fase 3 · T021.
//
// 🔴 Nada de la parte que habla con el bus se puede probar acá: la CI no tiene
// compositor, ni portal, ni sesión gráfica. Lo que sí se prueba es TODO lo que
// decide y todo lo que parsea, que es donde vivirían los errores caros:
// elegir la puerta equivocada, quedarse con la respuesta de otro programa del
// bus, o leer "concedido" donde el portal dijo que la persona canceló.

func TestEscritorioWayland(t *testing.T) {
	casos := []struct {
		nombre string
		env    []string
		quiero string
	}{
		{"GNOME pelado", []string{"XDG_CURRENT_DESKTOP=GNOME"}, "gnome"},
		{"GNOME de Ubuntu", []string{"XDG_CURRENT_DESKTOP=ubuntu:GNOME"}, "gnome"},
		{"KDE", []string{"XDG_CURRENT_DESKTOP=KDE"}, "kde"},
		{"Plasma por nombre nuevo", []string{"XDG_CURRENT_DESKTOP=plasma"}, "kde"},
		{"sway", []string{"XDG_CURRENT_DESKTOP=sway"}, "wlroots"},
		{"Hyprland con mayúscula", []string{"XDG_CURRENT_DESKTOP=Hyprland"}, "wlroots"},
		// El desconocido tiene que caer en cadena vacía y NO en una familia
		// inventada: de ahí depende que se use el portal en vez de gastar el
		// intento en una herramienta que ese compositor no atiende.
		{"escritorio desconocido", []string{"XDG_CURRENT_DESKTOP=Enlightenment"}, ""},
		{"sin la variable", []string{"WAYLAND_DISPLAY=wayland-0"}, ""},
		{"variable vacía", []string{"XDG_CURRENT_DESKTOP="}, ""},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := escritorioWayland(c.env); got != c.quiero {
				t.Fatalf("escritorioWayland() = %q, se esperaba %q", got, c.quiero)
			}
		})
	}
}

// instalados arma la costura de `buscarBinario` para las pruebas.
func instalados(bins ...string) func(string) (string, bool) {
	set := map[string]bool{}
	for _, b := range bins {
		set[b] = true
	}
	return func(nombre string) (string, bool) {
		if set[nombre] {
			return "/usr/bin/" + nombre, true
		}
		return "", false
	}
}

func nombresDeVias(vias []viaWayland) []string {
	out := make([]string, 0, len(vias))
	for _, v := range vias {
		out = append(out, v.nombre)
	}
	return out
}

func TestViasSilenciosasWayland(t *testing.T) {
	casos := []struct {
		nombre     string
		escritorio string
		bins       []string
		quiero     []string
	}{
		{
			// El orden importa: en GNOME la Shell va primero aunque `grim` esté
			// instalado, porque Mutter no habla `wlr-screencopy` y ese intento
			// se gastaría entero antes de llegar al que sí funciona.
			nombre:     "GNOME prefiere la Shell aunque haya grim",
			escritorio: "gnome", bins: []string{"grim", "gdbus"},
			quiero: []string{viaGnome, viaGrim},
		},
		{
			nombre:     "KDE prefiere spectacle",
			escritorio: "kde", bins: []string{"grim", "spectacle", "gdbus"},
			quiero: []string{viaKde, viaGrim},
		},
		{
			// En wlroots no se ofrece gdbus: la Shell de GNOME no existe ahí y
			// llamarla sería un fallo garantizado con su plazo incluido.
			nombre:     "wlroots sólo grim",
			escritorio: "wlroots", bins: []string{"grim", "gdbus", "spectacle"},
			quiero: []string{viaGrim},
		},
		{
			nombre:     "escritorio desconocido las prueba todas",
			escritorio: "", bins: []string{"grim", "gdbus", "spectacle"},
			quiero: []string{viaGrim, viaGnome, viaKde},
		},
		{
			// El caso que manda al portal, y el que hace falta que sea vacío
			// exacto: si acá se colara una vía, el enrolamiento se saltaría.
			nombre:     "sin ninguna herramienta no hay vía silenciosa",
			escritorio: "gnome", bins: nil,
			quiero: []string{},
		},
		{
			nombre:     "GNOME sin gdbus cae a grim",
			escritorio: "gnome", bins: []string{"grim"},
			quiero: []string{viaGrim},
		},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			got := nombresDeVias(viasSilenciosasWayland(c.escritorio, instalados(c.bins...)))
			if len(got) != len(c.quiero) {
				t.Fatalf("vías = %v, se esperaba %v", got, c.quiero)
			}
			for i := range got {
				if got[i] != c.quiero[i] {
					t.Fatalf("vías = %v, se esperaba %v (difieren en la posición %d)", got, c.quiero, i)
				}
			}
		})
	}
}

// TestViaGnomePideSinDestello fija los dos booleanos del método de la Shell.
//
// Es una prueba de un literal a propósito: si alguien invierte el orden de los
// argumentos, el `flash` pasa a true y la captura DESTELLA EN PANTALLA de un
// equipo robado. No hay forma de que la CI note eso de otra manera.
func TestViaGnomePideSinDestello(t *testing.T) {
	vias := viasSilenciosasWayland("gnome", instalados("gdbus"))
	if len(vias) != 1 {
		t.Fatalf("se esperaba una sola vía, hay %d", len(vias))
	}

	args := vias[0].args
	if len(args) < 3 {
		t.Fatalf("argumentos insuficientes: %v", args)
	}

	// Los tres últimos son include_cursor, flash y la ruta.
	cursor, flash, ruta := args[len(args)-3], args[len(args)-2], args[len(args)-1]
	if cursor != "false" {
		t.Errorf("include_cursor = %q, tiene que ser false", cursor)
	}
	if flash != "false" {
		t.Errorf("flash = %q, tiene que ser false: el destello se ve en pantalla", flash)
	}
	if ruta != "%s" {
		t.Errorf("el último argumento tiene que ser el marcador de ruta, es %q", ruta)
	}
}

func TestRespuestaGnomeShellOK(t *testing.T) {
	casos := []struct {
		salida string
		quiero bool
	}{
		{"(true, '/run/user/1000/pantalla.png')", true},
		{"  (true, '/tmp/x.png')\n", true},
		// El caso que motiva la función: `gdbus` sale con 0 y la Shell dice que
		// no. Sin mirar el booleano, esto se tomaría como captura buena.
		{"(false, '')", false},
		{"(false, '')\n", false},
		{"", false},
		{"error: no such interface", false},
	}

	for _, c := range casos {
		if got := respuestaGnomeShellOK(c.salida); got != c.quiero {
			t.Errorf("respuestaGnomeShellOK(%q) = %v, se esperaba %v", c.salida, got, c.quiero)
		}
	}
}

// TestRutaPeticionPortal fija la derivación del objeto Request. Suscribirse al
// path equivocado ANTES de pedir es exactamente el defecto que cerró T021: la
// respuesta viaja dirigida a ese objeto y, si no coincide, no se recibe nunca
// —el portal concede y saca la foto, pero el agente reporta "no respondió".
func TestRutaPeticionPortal(t *testing.T) {
	const token = "obs4242_1723600000000000000"

	casos := []struct {
		nombre string
		unico  string
		quiero string
	}{
		{"nombre único normal", ":1.64", "/org/freedesktop/portal/desktop/request/1_64/" + token},
		// El `.` va a `_` y el `:` inicial se cae; sin esto el path queda
		// malformado y el AddMatch no engancha nada.
		{"varios puntos", ":1.222.7", "/org/freedesktop/portal/desktop/request/1_222_7/" + token},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := string(rutaPeticionPortal(c.unico, token)); got != c.quiero {
				t.Fatalf("rutaPeticionPortal(%q) = %q, se esperaba %q", c.unico, got, c.quiero)
			}
		})
	}
}

func TestRutaDesdeURI(t *testing.T) {
	casos := []struct {
		uri    string
		ruta   string
		quiero bool
	}{
		{"file:///run/user/1000/doc/ab/captura.png", "/run/user/1000/doc/ab/captura.png", true},
		// El escritorio de cualquier persona tiene nombres con espacio o con
		// acento; sin decodificar, la copia buscaría un archivo inexistente.
		{"file:///home/ana/Captura%20de%20pantalla.png", "/home/ana/Captura de pantalla.png", true},
		{"file:///home/ana/informe%20a%C3%B1o.png", "/home/ana/informe año.png", true},
		// No vamos a ir a buscar una captura por la red.
		{"http://ejemplo.cl/x.png", "", false},
		{"", "", false},
		{"file://", "", false},
	}

	for _, c := range casos {
		ruta, ok := rutaDesdeURI(c.uri)
		if ok != c.quiero {
			t.Errorf("rutaDesdeURI(%q) ok = %v, se esperaba %v", c.uri, ok, c.quiero)
			continue
		}
		if ok && ruta != c.ruta {
			t.Errorf("rutaDesdeURI(%q) = %q, se esperaba %q", c.uri, ruta, c.ruta)
		}
	}
}

// TestTokenPortalEsValidoParaDBus cubre la restricción del bus: un token con un
// carácter fuera de [A-Za-z0-9_] hace que el portal rechace la petición entera.
func TestTokenPortalEsValidoParaDBus(t *testing.T) {
	tok := tokenPortal(4242, time.Unix(1723600000, 123456789))

	if tok == "" {
		t.Fatal("token vacío")
	}
	for _, r := range tok {
		valido := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
		if !valido {
			t.Fatalf("el token %q tiene el carácter %q, que D-Bus no acepta", tok, r)
		}
	}

	// Dos capturas del mismo proceso no pueden compartir token: si lo hicieran,
	// la segunda podría quedarse con la respuesta de la primera.
	otro := tokenPortal(4242, time.Unix(1723600000, 987654321))
	if tok == otro {
		t.Fatal("dos instantes distintos dieron el mismo token")
	}
}

func TestDebeCorrerRondaPortal(t *testing.T) {
	ahora := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	casos := []struct {
		nombre string
		reg    intentoPortal
		forzar bool
		quiero bool
	}{
		{"usuario nuevo", intentoPortal{}, false, true},
		// El que ya concedió no se vuelve a molestar NUNCA, ni forzando: no hay
		// nada que pedir.
		{"ya concedido", intentoPortal{Concedido: true, Rondas: 1}, false, false},
		{"ya concedido, ni forzando", intentoPortal{Concedido: true}, true, false},
		// Dos negativas son una respuesta.
		{"tope de rondas alcanzado", intentoPortal{Rondas: 2, Ultima: ahora.Add(-72 * time.Hour)}, false, false},
		{"tope alcanzado pero forzado a mano", intentoPortal{Rondas: 5, Ultima: ahora.Add(-time.Minute)}, true, true},
		// El equipo que se reinicia tres veces en una mañana no puede dar tres
		// diálogos.
		{"dentro de la espera", intentoPortal{Rondas: 1, Ultima: ahora.Add(-2 * time.Hour)}, false, false},
		{"pasada la espera", intentoPortal{Rondas: 1, Ultima: ahora.Add(-25 * time.Hour)}, false, true},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := debeCorrerRondaPortal(c.reg, ahora, c.forzar); got != c.quiero {
				t.Fatalf("debeCorrerRondaPortal() = %v, se esperaba %v", got, c.quiero)
			}
		})
	}
}

// TestEstadoPortalWaylandPersiste cubre el enrolamiento de punta a punta sin
// tocar el bus: que lo concedido sobreviva al reinicio del servicio es
// exactamente lo que evita que el portal vuelva a preguntar.
func TestEstadoPortalWaylandPersiste(t *testing.T) {
	a := agenteDePrueba(t)
	sess := &userSession{Username: "ana", Env: []string{"XDG_CURRENT_DESKTOP=KDE"}}

	if a.portalWaylandAutorizado("ana") {
		t.Fatal("un usuario sin estado no puede figurar como autorizado")
	}

	a.marcarPortalWayland(sess, false)
	if a.portalWaylandAutorizado("ana") {
		t.Fatal("una negativa quedó guardada como autorización")
	}

	a.marcarPortalWayland(sess, true)
	if !a.portalWaylandAutorizado("ana") {
		t.Fatal("la autorización no se persistió")
	}

	// El estado es por usuario: el permiso de una persona no habilita al resto.
	if a.portalWaylandAutorizado("beto") {
		t.Fatal("la autorización de ana alcanzó a beto")
	}

	st := a.cargarEstadoPortalWayland()
	reg := st.Usuarios["ana"]
	if reg.Rondas != 2 {
		t.Errorf("rondas = %d, se esperaban 2", reg.Rondas)
	}
	if reg.Escritorio != "kde" {
		t.Errorf("escritorio = %q, se esperaba kde", reg.Escritorio)
	}
}

// TestEstadoPortalWaylandIlegible: un JSON truncado se tiene que comportar como
// "no hay nada", nunca como "estaba autorizado".
func TestEstadoPortalWaylandIlegible(t *testing.T) {
	a := agenteDePrueba(t)

	truncado := []byte(`{"usuarios": {"ana": {"conce`)
	if err := os.WriteFile(waylandEstadoPath(a.lostModeDataDir()), truncado, 0600); err != nil {
		t.Fatal(err)
	}

	if a.portalWaylandAutorizado("ana") {
		t.Fatal("un archivo truncado se leyó como autorización concedida")
	}
}

// TestViaSilenciosaFuncionaDecidePorEfecto fija el defecto medido el 2026-08-15
// en GNOME Shell 50.4: la guardia del enrolamiento decidía por PRESENCIA del
// binario, y en GNOME el binario es `gdbus` —que viene con glib2 y está
// siempre— mientras la Shell deniega la llamada a todo cliente fuera de su
// lista blanca. Resultado: el portal, único camino real en GNOME, no se
// enrolaba nunca, y el motivo `wayland_sin_autorizacion` se anunciaba como
// reparable sin que nada pudiera repararlo.
func TestViaSilenciosaFuncionaDecidePorEfecto(t *testing.T) {
	gnome := viaWayland{nombre: viaGnome, bin: "/usr/bin/gdbus"}
	grim := viaWayland{nombre: viaGrim, bin: "/usr/bin/grim"}

	casos := []struct {
		nombre   string
		vias     []viaWayland
		funciona map[string]bool
		quiere   bool
	}{
		// EL caso. En GNOME 50 las dos vías existen y las dos fallan:
		// `gdbus` con AccessDenied y `grim` porque Mutter no habla
		// `wlr-screencopy`. La guardia vieja devolvía "sí hay vía" y mataba
		// el enrolamiento.
		{"GNOME 50: dos vías presentes y ninguna funciona", []viaWayland{gnome, grim}, nil, false},
		{"alguna funciona", []viaWayland{gnome, grim}, map[string]bool{viaGrim: true}, true},
		{"la primera funciona", []viaWayland{gnome, grim}, map[string]bool{viaGnome: true}, true},
		{"sin vías", nil, nil, false},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			got := viaSilenciosaFunciona(c.vias, func(v viaWayland) bool {
				return c.funciona[v.nombre]
			})
			if got != c.quiere {
				t.Errorf("viaSilenciosaFunciona(...) = %v, se esperaba %v", got, c.quiere)
			}
		})
	}
}

// La sonda no puede parar en la primera vía: si la primera falla y la segunda
// sirve, dar por bueno el fallo pediría el portal sin necesidad — justo lo que
// la guardia existe para evitar.
func TestViaSilenciosaFuncionaSigueTrasUnFallo(t *testing.T) {
	vias := []viaWayland{{nombre: viaGnome}, {nombre: viaGrim}}

	var probadas []string
	viaSilenciosaFunciona(vias, func(v viaWayland) bool {
		probadas = append(probadas, v.nombre)
		return v.nombre == viaGrim
	})

	if len(probadas) != 2 {
		t.Errorf("se probaron %v, se esperaban las dos vías", probadas)
	}
}
