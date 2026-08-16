//go:build linux
// +build linux

package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Feature 030 · Fase 1 · captura de pantalla en Linux (T009).
//
// El agente es un servicio del sistema: no tiene DISPLAY, ni cookie de X11, ni
// forma de dibujar o de leer el escritorio de nadie. El puente que resuelve eso
// ya existe desde la 028 (`usersession_linux.go`): cosecha el entorno de un
// proceso vivo del usuario y baja privilegios con `SysProcAttr.Credential`.
// Acá sólo se lo usa para correr una herramienta de captura.
//
// DOS DECISIONES QUE SE VEN EN EL TERRENO:
//
//  1. **Wayland tiene su propio archivo.** `scrot`/`import` hablan X11 puro; en
//     una sesión Wayland conectan y devuelven NEGRO —medido el 2026-08-02 en un
//     Fedora 44 KDE, con una captura reducida a 4×4 en gris que dio 16 ceros—.
//     Por eso la rama Wayland no comparte NADA con la de abajo: se va entera a
//     `capture_linux_wayland.go` (T021), que prueba las vías nativas de cada
//     compositor y, sólo si alguien lo autorizó antes, el portal. Cuando no hay
//     ninguna, se sigue reportando un motivo honesto —`wayland_no_soportado` o
//     `wayland_sin_autorizacion`— en vez de una imagen negra que parece
//     evidencia.
//
//  2. **El archivo cae en el directorio del usuario, no en el del agente.** La
//     herramienta corre con el uid del usuario y `/opt/observeragent` es de root
//     con 0755: escribir ahí daría "permiso denegado" en todas las capturas. Se
//     usa `XDG_RUNTIME_DIR` (/run/user/<uid>, 0700 y del usuario), que además es
//     lo más cerrado que hay disponible.

// herramientasCaptura son los programas que sabemos manejar, en orden de
// preferencia. Todos escriben PNG cuando el archivo destino termina en `.png`.
//
// El orden no es arbitrario: primero los dedicados y livianos (`scrot`, `maim`),
// después ImageMagick —que casi siempre está pero arrastra medio mundo—, y al
// final los de escritorio, que en algunas versiones muestran una notificación al
// terminar. Una captura que anuncia que se tomó no sirve para esto.
//
// `%s` se sustituye por la ruta destino. Ningún argumento viene del operador ni
// del servidor: la ruta la arma el agente, así que no hay superficie de inyección.
var herramientasCaptura = []struct {
	bin  string
	args []string
}{
	{"scrot", []string{"%s"}},
	{"maim", []string{"%s"}},
	{"import", []string{"-window", "root", "%s"}},
	{"spectacle", []string{"-b", "-n", "-o", "%s"}},
	{"gnome-screenshot", []string{"-f", "%s"}},
}

// rutasHerramientas son los directorios donde se busca cuando `exec.LookPath`
// no encuentra nada. El PATH del servicio lo fija systemd y en algunas unidades
// viene recortado, así que no alcanza con confiar en él.
var rutasHerramientas = []string{"/usr/bin", "/usr/local/bin", "/bin", "/usr/X11R6/bin"}

func (a *Agent) capturarPantalla(nombre string) ScreenShot {
	sess, err := a.findUserSession()
	if err != nil {
		a.Logger.Debugln("capturarPantalla: no hay sesión gráfica de usuario:", err)
		return ScreenShot{Reason: captureNoSession}
	}

	if sess.esWayland() {
		// T021 (Fase 3): Wayland dejó de ser un callejón sin salida. La escalera
		// de vías —y por qué el portal no se usa durante un caso activo— está en
		// capture_linux_wayland.go. Las herramientas de abajo no se prueban acá:
		// `scrot` y compañía hablan X11 puro y en Wayland conectan y devuelven
		// NEGRO (medido el 2026-08-02 en un Fedora 44 KDE).
		return a.capturarPantallaWayland(sess, nombre)
	}

	bin, args, ok := elegirHerramientaCaptura()
	if !ok {
		a.Logger.Infoln("capturarPantalla: no hay ninguna herramienta de captura instalada (se buscó scrot, maim, import, spectacle, gnome-screenshot)")
		return ScreenShot{Reason: captureNoTool}
	}

	destino := filepath.Join(directorioCapturaUsuario(sess), nombre)

	// Con plazo: una herramienta gráfica colgada esperando algo que nadie va a
	// contestar no puede dejar clavado el ciclo de captura. 30 s es holgado — una
	// captura de pantalla tarda menos de un segundo.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	finales := make([]string, 0, len(args))
	for _, arg := range args {
		finales = append(finales, strings.ReplaceAll(arg, "%s", destino))
	}

	cmd := a.newSessionCmdFor(ctx, sess, bin, finales...)
	salida, err := cmd.CombinedOutput()
	if err != nil {
		// La salida va al log porque los errores de X11 son diagnósticos de
		// verdad ("Can't open display", "Authorization required"), y sin ellos el
		// motivo `fallo_captura` no le dice nada a nadie.
		a.Logger.Debugln("capturarPantalla:", bin, "falló:", err, "salida:", strings.TrimSpace(string(salida)))
		_ = os.Remove(destino)
		return ScreenShot{Reason: captureFailed}
	}

	// El `exit 0` NO prueba el efecto: `gnome-screenshot` sale con 0 aunque no
	// haya escrito nada cuando el portal le niega el acceso. Lo que prueba la
	// captura es que exista un archivo con contenido.
	info, err := os.Stat(destino)
	if err != nil || info.Size() == 0 {
		a.Logger.Debugln("capturarPantalla:", bin, "salió con 0 pero no dejó archivo utilizable")
		_ = os.Remove(destino)
		return ScreenShot{Reason: captureFailed}
	}

	return ScreenShot{Path: destino}
}

// esWayland decide por qué rama va la captura, y le cree PRIMERO a logind.
//
// EL HALLAZGO QUE LA MOTIVA (terreno, Fedora 44 KDE Plasma 6, 2026-08-15): la
// heurística de abajo daba `false` en una sesión Wayland pura, así que el ciclo
// se iba entero por la rama X11. La causa es que Plasma 6 —y GNOME moderno—
// levantan XWayland SIEMPRE y exportan `DISPLAY=:0`, y la regla "si hay DISPLAY
// es X11" se lo tragaba.
//
// La premisa que justificaba esa regla era que bajo XWayland al menos se
// capturan las ventanas X11, "mejor que nada". Medido en esa máquina: NO hay
// nada. `import -window root` sale con 1 y sin archivo, y `scrot`/`maim` ni
// están; la captura terminaba en `fallo_captura` sin haber probado nunca
// `spectacle`, que sí funciona por la vía Wayland. La premisa era falsa.
//
// `Type` de logind no se deduce del entorno de ningún proceso: lo publica el
// gestor de sesiones. La heurística sobre el entorno queda como respaldo para
// cuando logind no contestó (equipos sin systemd, o el escaneo de /proc).
func (s *userSession) esWayland() bool {
	switch strings.ToLower(strings.TrimSpace(s.SessionType)) {
	case "wayland":
		return true
	case "x11":
		return false
	}

	return sesionEsWayland(s.Env)
}

// sesionEsWayland decide si el entorno cosechado corresponde a una sesión
// Wayland. Vive separada de la captura para poder probarla en la CI con entornos
// reales de los tres escritorios que hay en la flota, sin necesitar un servidor
// gráfico.
//
// El criterio es POSITIVO sobre X11 y no negativo sobre Wayland: si hay DISPLAY
// se asume X11 aunque también haya WAYLAND_DISPLAY, porque ese par es
// exactamente lo que publica XWayland — y bajo XWayland `scrot` sí captura las
// ventanas X11, que es mejor que nada. Al revés (sin DISPLAY y con
// WAYLAND_DISPLAY o XDG_SESSION_TYPE=wayland) no hay nada que intentar.
func sesionEsWayland(env []string) bool {
	var display, wayland, tipo string

	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || v == "" {
			continue
		}
		switch k {
		case "DISPLAY":
			display = v
		case "WAYLAND_DISPLAY":
			wayland = v
		case "XDG_SESSION_TYPE":
			tipo = v
		}
	}

	if display != "" {
		return false
	}

	return wayland != "" || strings.EqualFold(tipo, "wayland")
}

// elegirHerramientaCaptura devuelve el primer capturador disponible, con su ruta
// absoluta ya resuelta.
//
// La ruta se resuelve acá y no se deja en manos del hijo porque el PATH del
// proceso del usuario lo arma `findUserSession` con tres directorios fijos: un
// capturador instalado fuera de ellos existiría y no se encontraría.
func elegirHerramientaCaptura() (string, []string, bool) {
	for _, h := range herramientasCaptura {
		if ruta, ok := buscarBinario(h.bin); ok {
			return ruta, h.args, true
		}
	}
	return "", nil, false
}

func buscarBinario(nombre string) (string, bool) {
	if ruta, err := exec.LookPath(nombre); err == nil {
		return ruta, true
	}

	for _, dir := range rutasHerramientas {
		ruta := filepath.Join(dir, nombre)
		if info, err := os.Stat(ruta); err == nil && !info.IsDir() {
			return ruta, true
		}
	}

	return "", false
}

// directorioCapturaUsuario elige dónde escribe la herramienta.
//
// Preferencia: `XDG_RUNTIME_DIR` (/run/user/<uid>), que es del usuario, tiene
// 0700 y vive en tmpfs — la imagen no toca el disco. Si no está, el home; y
// como último recurso `/tmp`, que es escribible por cualquiera. En los tres
// casos el archivo se cierra a 0600 apenas existe (ver `CaptureScreen`) y se
// borra en cuanto se sube: la ventana en que otro usuario del equipo podría
// leerlo es de milisegundos, pero no es cero y por eso el orden es este.
func directorioCapturaUsuario(sess *userSession) string {
	for _, kv := range sess.Env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == "XDG_RUNTIME_DIR" && v != "" {
			if info, err := os.Stat(v); err == nil && info.IsDir() {
				return v
			}
		}
	}

	if sess.Home != "" {
		if info, err := os.Stat(sess.Home); err == nil && info.IsDir() {
			return sess.Home
		}
	}

	return os.TempDir()
}

// motivoDePantallaNegra distingue "la pantalla está apagada" de "la captura
// salió negra y no sé por qué".
//
// EL HALLAZGO QUE LA MOTIVA (terreno, 2026-08-12): en un equipo real, 25 s
// después de arrancar la sesión gráfica la captura salía legible; unos minutos
// más tarde, completamente negra. No era un fallo de la herramienta ni del
// puente de sesión —una ventana brillante recién creada también salía negra—:
// era el protector de pantalla del propio X. Sin esta consulta, la línea de
// tiempo de un equipo que nadie está usando se llena de "la captura salió en
// negro", que se lee como una avería y en realidad es un dato del caso.
//
// `xset q` es parte de x11-xserver-utils y puede no estar instalado; ante
// cualquier duda se devuelve el motivo genérico, que sigue siendo honesto.
func (a *Agent) motivoDePantallaNegra() string {
	sess, err := a.findUserSession()
	if err != nil {
		return captureBlank
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// El bloqueo se pregunta PRIMERO, y a logind, no a X. Dos razones medidas en
	// terreno (Fedora 44 KDE sobre Wayland, 2026-08-15):
	//
	//   1. `xset` es de x11-xserver-utils y en esa máquina NO ESTÁ INSTALADO, así
	//      que la rama de abajo devolvía el motivo genérico. En una sesión
	//      Wayland pura tampoco tendría nada útil que informar aunque estuviera.
	//   2. `LockedHint` la publica systemd-logind: no necesita bus de sesión ni
	//      el puente del usuario, la lee el servicio como root, y vale igual en
	//      GNOME, en KDE y en X11.
	//
	// La medición que lo motiva: 8 capturas seguidas con la sesión bloqueada
	// dieron PNG válidos de 1366x768, `exit 0`, y un solo color — negro puro.
	// Nada falla; simplemente no hay nada que ver. Sin esta consulta eso subía
	// como `pantalla_en_negro`, indistinguible de una avería real.
	if a.sesionBloqueada(ctx, sess.SessionID) {
		return captureScreenLocked
	}

	bin, ok := buscarBinario("xset")
	if !ok {
		a.Logger.Debugln("motivoDePantallaNegra: no está xset, no se puede saber si la pantalla está apagada")
		return captureBlank
	}

	salida, err := a.newSessionCmdFor(ctx, sess, bin, "q").Output()
	if err != nil {
		a.Logger.Debugln("motivoDePantallaNegra: xset falló:", err)
		return captureBlank
	}

	if pantallaApagada(string(salida)) {
		return captureScreenOff
	}
	return captureBlank
}

// sesionBloqueada pregunta a systemd-logind si la sesión gráfica está bloqueada.
//
// Es una LECTURA ÚNICA a propósito. `lockedHintConfirmed` (response_linux.go)
// consulta lo mismo en un bucle corto porque allá se acaba de mandar la señal de
// bloqueo y hay que esperar a que el escritorio la dibuje. Acá no se mandó nada:
// se está diagnosticando un estado que ya existe, y reintentar sólo retrasaría
// el ciclo de captura.
//
// Ante cualquier duda —sin ID de sesión, sin `loginctl`, fallo de la consulta—
// devuelve false y el diagnóstico sigue por las otras vías. Un motivo genérico
// es honesto; uno inventado, no.
func (a *Agent) sesionBloqueada(ctx context.Context, sessionID string) bool {
	if sessionID == "" {
		return false
	}

	loginctl, err := exec.LookPath("loginctl")
	if err != nil {
		a.Logger.Debugln("sesionBloqueada: no está loginctl, no se puede saber si la sesión está bloqueada")
		return false
	}

	out, err := exec.CommandContext(ctx, loginctl, "show-session", sessionID,
		"--property=LockedHint").Output()
	if err != nil {
		a.Logger.Debugln("sesionBloqueada: loginctl falló:", err)
		return false
	}

	return parseLoginctlProps(out)["LockedHint"] == "yes"
}

// pantallaApagada lee la salida de `xset q`. Separada para poder probarla con
// salidas reales, que es la única forma de cubrirla sin un servidor X.
//
// Se miran DOS cosas, porque son estados distintos y cualquiera de los dos deja
// la pantalla negra:
//
//   - DPMS: `Monitor is Off | Standby | Suspend` — el monitor está en ahorro.
//   - El protector de pantalla clásico: `prefer blanking: yes` no alcanza (es la
//     preferencia, no el estado), así que se mira si el servidor declara el
//     screensaver ACTIVO.
func pantallaApagada(salida string) bool {
	for _, linea := range strings.Split(salida, "\n") {
		l := strings.TrimSpace(linea)

		if strings.HasPrefix(l, "Monitor is ") {
			estado := strings.TrimSpace(strings.TrimPrefix(l, "Monitor is "))
			if !strings.EqualFold(estado, "On") {
				return true
			}
		}

		// `xset q` lo reporta así cuando el protector está corriendo.
		if strings.Contains(l, "screen saver:") && strings.Contains(strings.ToLower(l), "active") {
			return true
		}
	}

	return false
}

// usuarioDeLaSesionGrafica devuelve el dueño de la sesión de escritorio, que es
// de quien es la pantalla que se capturó.
func (a *Agent) usuarioDeLaSesionGrafica() string {
	sess, err := a.findUserSession()
	if err != nil {
		return ""
	}
	return sess.Username
}
