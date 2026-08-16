//go:build linux
// +build linux

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// Feature 030 · Fase 3 · T021 — captura de pantalla en sesiones Wayland.
//
// ── EL PROBLEMA ───────────────────────────────────────────────────────────────
//
// En X11 cualquier cliente con el `DISPLAY` y la cookie puede leer la pantalla
// entera: por eso `scrot` funciona y por eso X11 es inseguro. Wayland cerró esa
// puerta a propósito. No hay una API universal de captura, y el reemplazo
// oficial —el portal `xdg-desktop-portal`— fue diseñado para PREGUNTARLE a la
// persona. En un equipo robado, preguntar es delatarse.
//
// La 028 dejó la rama en `wayland_no_soportado` justamente para no fingir: una
// captura negra parece evidencia rota, y un motivo honesto es un dato del caso.
// T021 no cambia esa honestidad, le agrega los caminos que sí existen.
//
// ── LA ESCALERA, DE MÁS SILENCIOSO A MENOS ────────────────────────────────────
//
//  1. **`grim`** (compositores wlroots: sway, Hyprland, river). Habla el
//     protocolo privado `wlr-screencopy`: sin diálogo, sin portal, sin rastro.
//     Es el mejor caso y el más simple.
//
//  2. **GNOME, por el bus privado de la Shell**
//     (`org.gnome.Shell.Screenshot`). No pasa por el portal: la Shell la
//     atiende directo. Se pide con `flash=false` — el destello es una
//     animación, y una captura que se ve en pantalla no sirve para esto.
//
//  3. **KDE, por Spectacle** (`-b -n`), que por dentro usa
//     `org.kde.KWin.ScreenShot2`. Ya estaba en la lista de X11; acá se lo llama
//     explícitamente porque en Wayland es el ÚNICO de esa lista que funciona.
//
//  4. **El portal `org.freedesktop.portal.Screenshot`**, y sólo si alguien lo
//     autorizó antes (ver más abajo). Es el universal, y el único que puede
//     mostrar un diálogo.
//
// Si no hay ninguno, se sigue reportando `wayland_no_soportado`, igual que
// antes. La diferencia es que ahora eso significa "este equipo de verdad no
// puede", no "no lo intentamos".
//
// ── POR QUÉ EL PORTAL SE AUTORIZA ANTES Y NO DURANTE ──────────────────────────
//
// 🚫 El portal es exactamente el mismo dilema que TCC en macOS, y se resuelve
// igual: el permiso se pide CUANDO HAY ALGUIEN DELANTE —en la instalación o al
// arrancar el servicio con un binario nuevo— y jamás durante un ciclo de modo
// perdido activo. Durante un caso sólo corren las vías silenciosas y, si el
// portal no quedó autorizado, se reporta `wayland_sin_autorizacion`: un motivo
// accionable para quien opera, que le dice que a ese equipo le faltó un paso de
// enrolamiento, y no un diálogo que le avisa al ladrón.
//
// La marca de que quedó autorizado la guarda `RondaPortalWayland` en
// `wayland_portal.json`, por usuario. Se guarda por usuario y no por binario
// —al revés que TCC— porque el portal no ancla el permiso al hash del
// ejecutable: lo guarda en su propio almacén, contra el escritorio y la sesión
// de esa persona. Actualizar el agente no lo rompe; cambiar de usuario, sí.
//
// ── LO QUE ESTÁ MEDIDO ────────────────────────────────────────────────────────
//
// ✅ Terreno GNOME Shell 50.4 / Wayland (Fedora 44, 2026-08-16): las dos vías
// silenciosas fallan como se documenta (la Shell deniega, grim no habla con
// Mutter) y el portal concede y entrega la captura por la vía nueva de una sola
// conexión (ver capturaPorPortal). El enrolamiento pasó de `concedido:false` a
// `concedido:true`. KDE Wayland ya estaba medido antes. Falta medir wlroots.
// La CI cubre toda la lógica que decide y que parsea, sin bus.

const (
	// viaGrim y compañía son los nombres que aparecen en el log. Sirven para
	// saber, leyendo la línea de tiempo de un caso, POR CUÁL de las cuatro
	// puertas entró cada captura — que es el dato que hará falta el día que una
	// falle en un escritorio concreto.
	viaGrim      = "grim"
	viaGnome     = "gnome-shell"
	viaKde       = "spectacle"
	portalDest         = "org.freedesktop.portal.Desktop"
	portalPath         = "/org/freedesktop/portal/desktop"
	portalMetodo       = "org.freedesktop.portal.Screenshot.Screenshot"
	portalRequestIface = "org.freedesktop.portal.Request"
)

// waylandArchivoEstado guarda si el portal quedó autorizado, y para quién.
// Vive junto al resto del estado del modo perdido: el desinstalador borra ese
// directorio entero, y una marca capaz de habilitar capturas no debe sobrevivir
// a la desinstalación.
const waylandArchivoEstado = "wayland_portal.json"

const (
	// waylandMaxRondas es el tope de veces que se le pide el portal al MISMO
	// usuario. Dos negativas son una respuesta; una tercera es acoso.
	waylandMaxRondas = 2

	// waylandEsperaEntreRondas evita que cada reinicio del equipo sea un
	// diálogo. Mismo criterio y mismo valor que la ronda de TCC.
	waylandEsperaEntreRondas = 24 * time.Hour

	// waylandPlazoPortal acota la espera de la respuesta del portal. Si hay
	// diálogo y nadie contesta, el proceso quedaría colgado para siempre; y un
	// ciclo de captura colgado es un ciclo que no reporta nada.
	waylandPlazoPortal = 45 * time.Second

	// waylandPlazoVia es el plazo de las vías silenciosas, que no preguntan
	// nada y por lo tanto no tienen razón para tardar.
	waylandPlazoVia = 20 * time.Second
)

// intentoPortal es lo que se recuerda de un usuario entre arranques.
type intentoPortal struct {
	Rondas     int       `json:"rondas"`
	Ultima     time.Time `json:"ultima"`
	Concedido  bool      `json:"concedido"`
	Escritorio string    `json:"escritorio"`
}

// estadoPortalWayland es el archivo completo, indexado por nombre de usuario.
type estadoPortalWayland struct {
	Usuarios map[string]intentoPortal `json:"usuarios"`
}

func waylandEstadoPath(dir string) string {
	return filepath.Join(dir, waylandArchivoEstado)
}

// escritorioWayland normaliza `XDG_CURRENT_DESKTOP`, que viene en formatos muy
// distintos según la distribución: `GNOME`, `ubuntu:GNOME`, `KDE`,
// `sway`, `Hyprland`. Se parte por `:` y se compara en minúsculas.
//
// Se devuelve una sola familia porque es lo único que se necesita: elegir la
// puerta. Un escritorio desconocido devuelve cadena vacía y cae al portal, que
// es precisamente el caso para el que el portal existe.
func escritorioWayland(env []string) string {
	var crudo string
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == "XDG_CURRENT_DESKTOP" && v != "" {
			crudo = v
			break
		}
	}

	for _, parte := range strings.Split(crudo, ":") {
		switch strings.ToLower(strings.TrimSpace(parte)) {
		case "gnome", "gnome-classic", "unity":
			return "gnome"
		case "kde", "plasma":
			return "kde"
		case "sway", "hyprland", "river", "wlroots", "labwc", "niri":
			return "wlroots"
		}
	}
	return ""
}

// viaWayland es una puerta concreta: el nombre que se loguea, el binario que
// hace falta y los argumentos, con `%s` por la ruta destino.
type viaWayland struct {
	nombre string
	bin    string
	args   []string
}

// viasSilenciosasWayland arma, en orden, las vías que NO preguntan nada.
//
// El orden depende del escritorio y no es cosmético: en GNOME, `grim` está casi
// siempre ausente y, si estuviera, fallaría (Mutter no habla `wlr-screencopy`).
// Poner primero la vía nativa del escritorio evita gastar un intento y un plazo
// en algo que se sabe que no puede funcionar.
//
// `existe` se inyecta para poder probar esto en la CI, donde no hay ni `grim`
// ni `gdbus` ni `spectacle` — sin la costura, la única prueba posible sería
// "devuelve lista vacía", que no prueba nada.
func viasSilenciosasWayland(escritorio string, existe func(string) (string, bool)) []viaWayland {
	// gdbusGnome se arma acá y no como literal porque los argumentos del bus
	// son largos y repetirlos en dos ramas invita a que se desincronicen.
	gnome := func(ruta string) viaWayland {
		return viaWayland{
			nombre: viaGnome,
			bin:    ruta,
			args: []string{
				"call", "--session",
				"--dest", "org.gnome.Shell",
				"--object-path", "/org/gnome/Shell/Screenshot",
				"--method", "org.gnome.Shell.Screenshot.Screenshot",
				// include_cursor=false: el puntero no aporta a la evidencia y
				// en algunas versiones lo dibuja corrido.
				"false",
				// flash=false: el destello es visible. Ver el encabezado.
				"false",
				"%s",
			},
		}
	}

	grim := func(ruta string) viaWayland {
		return viaWayland{nombre: viaGrim, bin: ruta, args: []string{"%s"}}
	}

	kde := func(ruta string) viaWayland {
		return viaWayland{nombre: viaKde, bin: ruta, args: []string{"-b", "-n", "-o", "%s"}}
	}

	// candidatos en el orden propio de cada escritorio; los que no estén
	// instalados se descartan más abajo.
	type cand struct {
		bin   string
		armar func(string) viaWayland
	}

	var orden []cand
	switch escritorio {
	case "gnome":
		orden = []cand{{"gdbus", gnome}, {"grim", grim}}
	case "kde":
		orden = []cand{{"spectacle", kde}, {"grim", grim}}
	case "wlroots":
		orden = []cand{{"grim", grim}}
	default:
		// Escritorio desconocido: se prueban todas, de la más silenciosa a la
		// menos. Ninguna hace daño si no aplica — fallan y se pasa a la
		// siguiente.
		orden = []cand{{"grim", grim}, {"gdbus", gnome}, {"spectacle", kde}}
	}

	vias := make([]viaWayland, 0, len(orden))
	for _, c := range orden {
		if ruta, ok := existe(c.bin); ok {
			vias = append(vias, c.armar(ruta))
		}
	}
	return vias
}

// respuestaGnomeShellOK lee la salida de `gdbus call` a la Shell.
//
// La Shell devuelve `(true, '/ruta/final')` y también puede devolver
// un `false` con la ruta vacía sin que `gdbus` falle: ahí el `exit 0` no prueba
// absolutamente nada. Por eso se mira el booleano y no el código de salida.
func respuestaGnomeShellOK(salida string) bool {
	return strings.HasPrefix(strings.TrimSpace(salida), "(true,")
}

// rutaPeticionPortal predice el objeto `Request` que el portal creará para
// nuestra petición, para poder suscribirse a su respuesta ANTES de pedir.
//
// El portal la deriva del nombre único del cliente y del `handle_token`: toma el
// nombre (`:1.64`), le quita el `:` inicial y cambia los `.` por `_` (`1_64`), y
// arma `/org/freedesktop/portal/desktop/request/<eso>/<token>`. Es contrato
// público del protocolo `org.freedesktop.portal.Request`.
func rutaPeticionPortal(nombreUnico, token string) dbus.ObjectPath {
	sender := strings.ReplaceAll(strings.TrimPrefix(nombreUnico, ":"), ".", "_")
	return dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + token)
}

// rutaDesdeURI convierte `file:///run/user/1000/doc/ab/x%20y.png` en una ruta.
//
// El portal devuelve el nombre percent-encoded, y un nombre con espacio o con
// acento —el escritorio de cualquier persona los tiene— llegaría roto sin este
// paso. Cualquier esquema que no sea `file` se rechaza: no vamos a ir a buscar
// una captura por la red.
func rutaDesdeURI(uri string) (string, bool) {
	if uri == "" {
		return "", false
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || u.Path == "" {
		return "", false
	}
	return u.Path, true
}

// tokenPortal arma el `handle_token`, que es lo que permite reconocer nuestra
// respuesta entre el tráfico del bus.
//
// D-Bus sólo acepta `[A-Za-z0-9_]` en un token. El pid y el nanosegundo dan
// unicidad más que suficiente para dos capturas del mismo agente, que es el
// único choque posible.
func tokenPortal(pid int, t time.Time) string {
	return fmt.Sprintf("obs%d_%d", pid, t.UnixNano())
}

// capturarPantallaWayland es el reemplazo de la rama que antes devolvía
// `wayland_no_soportado` sin intentar nada.
func (a *Agent) capturarPantallaWayland(sess *userSession, nombre string) ScreenShot {
	destino := filepath.Join(directorioCapturaUsuario(sess), nombre)
	escritorio := escritorioWayland(sess.Env)

	vias := viasSilenciosasWayland(escritorio, buscarBinario)
	for _, via := range vias {
		if a.correrViaWayland(sess, via, destino) {
			a.Logger.Debugln("capturarPantallaWayland: captura obtenida por", via.nombre)
			return ScreenShot{Path: destino}
		}
		a.Logger.Debugln("capturarPantallaWayland: la vía", via.nombre, "no dio imagen, se prueba la siguiente")
	}

	// Sin vía silenciosa queda el portal, y el portal sólo se usa si alguien lo
	// autorizó antes. La comprobación NO es "¿está el portal instalado?" sino
	// "¿quedó concedido para este usuario?", porque la primera no distingue el
	// caso en que llamar abriría un diálogo.
	if !a.portalWaylandAutorizado(sess.Username) {
		if _, hayGdbus := buscarBinario("gdbus"); !hayGdbus {
			a.Logger.Infoln("capturarPantallaWayland: sesión Wayland sin vía silenciosa y sin gdbus; no hay captura posible en este equipo")
			return ScreenShot{Reason: captureWayland}
		}
		a.Logger.Infoln("capturarPantallaWayland: el portal no está autorizado para", sess.Username, "— hace falta el enrolamiento con alguien delante del equipo")
		return ScreenShot{Reason: captureWaylandSinAutorizacion}
	}

	if err := a.capturaPorPortal(sess, destino); err != nil {
		a.Logger.Debugln("capturarPantallaWayland: el portal falló:", err)
		return ScreenShot{Reason: captureFailed}
	}

	a.Logger.Debugln("capturarPantallaWayland: captura obtenida por el portal")
	return ScreenShot{Path: destino}
}

// correrViaWayland ejecuta una vía silenciosa y verifica el EFECTO, no el
// código de salida.
//
// Los tres capturadores tienen la misma mala costumbre: salir con 0 sin haber
// escrito nada. La Shell de GNOME además lo dice en su valor de retorno, así
// que ahí se miran las dos cosas.
func (a *Agent) correrViaWayland(sess *userSession, via viaWayland, destino string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), waylandPlazoVia)
	defer cancel()

	args := make([]string, 0, len(via.args))
	for _, arg := range via.args {
		args = append(args, strings.ReplaceAll(arg, "%s", destino))
	}

	salida, err := a.newSessionCmdFor(ctx, sess, via.bin, args...).CombinedOutput()
	if err != nil {
		a.Logger.Debugln("correrViaWayland:", via.nombre, "falló:", err, "salida:", strings.TrimSpace(string(salida)))
		os.Remove(destino)
		return false
	}

	if via.nombre == viaGnome && !respuestaGnomeShellOK(string(salida)) {
		a.Logger.Debugln("correrViaWayland: la Shell de GNOME respondió que no:", strings.TrimSpace(string(salida)))
		os.Remove(destino)
		return false
	}

	info, err := os.Stat(destino)
	if err != nil || info.Size() == 0 {
		os.Remove(destino)
		return false
	}
	return true
}

// capturaPorPortal pide una captura por el portal, re-ejecutando el agente en la
// sesión del usuario (modo `-m portalscreenshot`).
//
// 🪤 POR QUÉ UN RE-EJECUTABLE Y NO `gdbus` — el defecto que esto corrige. El
// portal contesta con una señal `Request::Response` DIRIGIDA al nombre único del
// cliente que pidió. La versión vieja pedía con `gdbus call` en un proceso y
// escuchaba con `gdbus monitor` en OTRO: la señal dirigida nunca llegaba al
// monitor, así que el portal concedía y sacaba la foto pero el agente reportaba
// "no respondió en 45s". Hay que pedir y escuchar en la MISMA conexión, y eso
// `gdbus` CLI no lo permite. El hijo lo hace con godbus, corriendo además como
// el usuario —el bus de sesión no acepta a root— y de paso resuelve la copia
// desde lugares que root no puede leer. Ver agent/portalshot_linux.go.
func (a *Agent) capturaPorPortal(sess *userSession, destino string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("no se pudo resolver el ejecutable propio: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), waylandPlazoPortal+5*time.Second)
	defer cancel()

	cmd := a.newSessionCmdFor(ctx, sess, self, "-m", "portalscreenshot", destino)
	salida, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("el modo portalscreenshot falló: %w (%s)", err, strings.TrimSpace(string(salida)))
	}

	shot := parsearSalidaCaptura(string(salida))
	if shot.Reason != "" {
		// Una cancelación se guarda como no concedido para que la próxima ronda
		// de enrolamiento lo sepa, en vez de reintentar como si nada.
		if shot.Reason == captureWaylandCancelado {
			a.marcarPortalWayland(sess, false)
			return fmt.Errorf("la petición al portal fue cancelada")
		}
		return fmt.Errorf("el portal no entregó captura: %s", shot.Reason)
	}

	if info, err := os.Stat(destino); err != nil || info.Size() == 0 {
		os.Remove(destino)
		return fmt.Errorf("el portal dijo OK pero no dejó archivo utilizable en %s", destino)
	}
	return nil
}

// ── Estado del enrolamiento ───────────────────────────────────────────────────

func (a *Agent) cargarEstadoPortalWayland() estadoPortalWayland {
	var st estadoPortalWayland

	data, err := os.ReadFile(waylandEstadoPath(a.lostModeDataDir()))
	if err != nil {
		return st
	}
	if err := json.Unmarshal(data, &st); err != nil {
		a.Logger.Debugln("cargarEstadoPortalWayland: archivo ilegible, se empieza de cero:", err)
		return estadoPortalWayland{}
	}
	return st
}

// guardarEstadoPortalWayland escribe temporal + rename, por lo mismo que el
// resto del estado del modo perdido: un JSON truncado se comporta como "no
// había nada", y acá eso significaría volver a mostrar un diálogo ya contestado.
func (a *Agent) guardarEstadoPortalWayland(st estadoPortalWayland) {
	path := waylandEstadoPath(a.lostModeDataDir())

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		a.Logger.Debugln("guardarEstadoPortalWayland:", err)
		return
	}

	data, err := json.Marshal(st)
	if err != nil {
		a.Logger.Debugln("guardarEstadoPortalWayland:", err)
		return
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		a.Logger.Debugln("guardarEstadoPortalWayland:", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		a.Logger.Debugln("guardarEstadoPortalWayland:", err)
		os.Remove(tmp)
	}
}

func (a *Agent) portalWaylandAutorizado(usuario string) bool {
	st := a.cargarEstadoPortalWayland()
	return st.Usuarios[usuario].Concedido
}

// marcarPortalWayland deja registrado el resultado de un intento.
func (a *Agent) marcarPortalWayland(sess *userSession, concedido bool) {
	st := a.cargarEstadoPortalWayland()
	if st.Usuarios == nil {
		st.Usuarios = map[string]intentoPortal{}
	}

	reg := st.Usuarios[sess.Username]
	reg.Rondas++
	reg.Ultima = time.Now().UTC()
	reg.Concedido = concedido
	reg.Escritorio = escritorioWayland(sess.Env)
	st.Usuarios[sess.Username] = reg

	a.guardarEstadoPortalWayland(st)
}

// debeCorrerRondaPortal decide si toca pedirle el portal a este usuario.
//
// Mismo criterio que la ronda de TCC —tope de intentos y espera entre ellos—
// con una diferencia: acá no hay identidad de binario que reinicie el contador,
// porque el portal no ancla nada al ejecutable. El único reinicio posible es
// manual, y ese es justamente el `forzar`.
func debeCorrerRondaPortal(reg intentoPortal, ahora time.Time, forzar bool) bool {
	if reg.Concedido {
		return false
	}
	if forzar {
		return true
	}
	if reg.Rondas >= waylandMaxRondas {
		return false
	}
	if !reg.Ultima.IsZero() && ahora.Sub(reg.Ultima) < waylandEsperaEntreRondas {
		return false
	}
	return true
}

// RondaPortalWayland es el enrolamiento: el único momento en que se acepta que
// el portal muestre un diálogo.
//
// Corre al arrancar el servicio, que es cuando pasan los dos eventos que la
// justifican —la instalación y el arranque tras un update—. No hace nada si no
// hay sesión Wayland, si ya hay una vía silenciosa (no habría por qué molestar
// a nadie) o si el equipo está marcado como perdido.
func (a *Agent) RondaPortalWayland(forzar bool) {
	// 🚫 GUARDIA PRIMERA, la misma que en macOS y por la misma razón: en un
	// equipo marcado como perdido, un diálogo de permiso le avisa al ladrón.
	if activo, _ := a.getLostMode(); activo {
		a.Logger.Infoln("RondaPortalWayland: modo perdido activo, no se pide nada")
		return
	}

	sess, err := a.findUserSession()
	if err != nil {
		a.Logger.Debugln("RondaPortalWayland: no hay sesión gráfica, se posterga:", err)
		return
	}

	if !sess.esWayland() {
		return
	}

	// Si el escritorio tiene vía silenciosa QUE FUNCIONA, el portal sobra:
	// pedirlo sería molestar a una persona por una capacidad que ya tenemos.
	//
	// 🪤 EL DEFECTO QUE ESTO CORRIGE (terreno, GNOME Shell 50.4, 2026-08-15):
	// la guardia miraba si el BINARIO existía. En GNOME el binario es `gdbus`,
	// que viene con glib2 y por lo tanto está SIEMPRE ⇒ la guardia concluía
	// «este escritorio ya captura solo» y el enrolamiento no corría nunca.
	// Pero la Shell **deniega** la llamada a todo cliente que no esté en su
	// lista blanca (`checkInvocation`: `${método} is not allowed`, medido
	// también en `SelectArea` y `FlashArea`, que ni capturan píxeles). O sea
	// que en GNOME la vía existe, no funciona, y el único camino real —el
	// portal— quedaba cerrado con llave: el ciclo reportaba
	// `wayland_sin_autorizacion`, que se anuncia como REPARABLE, y la
	// reparación estaba bloqueada por esta misma guardia.
	//
	// Ahora se decide por el EFECTO, igual que `correrViaWayland`: se intenta
	// una captura de prueba y se borra. Es silenciosa por definición —son las
	// vías que no preguntan nada—, así que no molesta a nadie.
	if a.hayViaSilenciosaQueFunciona(sess) {
		a.Logger.Debugln("RondaPortalWayland: este escritorio tiene captura silenciosa que funciona, no hace falta el portal")
		return
	}

	st := a.cargarEstadoPortalWayland()
	if !debeCorrerRondaPortal(st.Usuarios[sess.Username], time.Now().UTC(), forzar) {
		a.Logger.Debugln("RondaPortalWayland: no toca en este arranque")
		return
	}

	// El enrolamiento es una captura de verdad, que se pide y se descarta: es
	// la única forma de saber si el portal concede, porque no hay una consulta
	// de estado equivalente al preflight de macOS. La imagen se borra en el
	// acto — no es evidencia de nada y no debe quedar en el disco.
	prueba := filepath.Join(directorioCapturaUsuario(sess), "observer-enrolamiento.png")
	defer os.Remove(prueba)

	if err := a.capturaPorPortal(sess, prueba); err != nil {
		a.Logger.Infoln("RondaPortalWayland: el portal no quedó autorizado:", err)
		a.marcarPortalWayland(sess, false)
		return
	}

	a.Logger.Infoln("RondaPortalWayland: el portal quedó autorizado para", sess.Username)
	a.marcarPortalWayland(sess, true)
}

// hayViaSilenciosaQueFunciona prueba las vías silenciosas del escritorio y dice
// si ALGUNA entrega una imagen. La captura de prueba se borra en el acto: no es
// evidencia de nada.
func (a *Agent) hayViaSilenciosaQueFunciona(sess *userSession) bool {
	vias := viasSilenciosasWayland(escritorioWayland(sess.Env), buscarBinario)
	if len(vias) == 0 {
		return false
	}

	prueba := filepath.Join(directorioCapturaUsuario(sess), "observer-sonda-via.png")
	defer os.Remove(prueba)

	return viaSilenciosaFunciona(vias, func(via viaWayland) bool {
		return a.correrViaWayland(sess, via, prueba)
	})
}

// viaSilenciosaFunciona se separó del resto para poder probar la DECISIÓN sin
// un escritorio: el caso que importa es «hay vías y ninguna funciona», que es
// exactamente GNOME 50 y era el que la guardia vieja leía como «ya captura».
func viaSilenciosaFunciona(vias []viaWayland, correr func(viaWayland) bool) bool {
	for _, via := range vias {
		if correr(via) {
			return true
		}
	}
	return false
}

// LanzarRondaPortalWayland la corre en segundo plano tras el arranque del
// servicio, con la misma demora y por el mismo motivo que la de macOS: recién
// arrancado el equipo, la sesión gráfica puede no estar del todo levantada y el
// portal todavía no haberse registrado en el bus.
func (a *Agent) LanzarRondaPortalWayland() {
	go func() {
		time.Sleep(30 * time.Second)
		a.RondaPortalWayland(false)
	}()
}
