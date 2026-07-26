//go:build linux
// +build linux

package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Feature 028 · puente hacia la sesión del usuario en Linux.
//
// Este es el único de los tres SO donde había que escribir el plumbing desde
// cero. Windows ya lo tenía (`wintoken`, usado por el `runasuser` de los scripts)
// y macOS lo heredó de la feature 025 (`launchctl asuser`). En Linux, en cambio,
// `CMDShell` es un stub que devuelve vacío y `RunScript` ignora `runasuser`.
//
// El problema es más grande que "cambiar de uid". Un proceso lanzado por una unit
// de systemd del sistema no tiene ninguna de las variables que hacen falta para
// hablar con el escritorio del usuario:
//
//	DISPLAY / WAYLAND_DISPLAY   a qué servidor gráfico dibujar
//	XAUTHORITY                  la cookie que autoriza a dibujar en X11
//	DBUS_SESSION_BUS_ADDRESS    el bus del usuario (zenity, notificaciones)
//	XDG_RUNTIME_DIR             donde vive el socket de PulseAudio/PipeWire
//
// Y no se pueden inventar: `XAUTHORITY` cambia según el display manager (GDM,
// SDDM, LightDM) y el socket de audio depende del uid. La forma que funciona de
// verdad es **cosechar el entorno de un proceso vivo del usuario** leyendo
// `/proc/<pid>/environ`. Eso además prueba que la sesión existe, en vez de
// suponerlo.

// userSession describe la sesión gráfica encontrada.
type userSession struct {
	UID       uint32
	GID       uint32
	Groups    []uint32
	Username  string
	Home      string
	SessionID string // ID de sesión de systemd-logind, para `loginctl lock-session`
	Env       []string
}

// sessionEnvKeys son las variables que se cosechan del proceso del usuario. Se
// copia sólo esta lista y no el entorno completo: heredar todo traería cosas
// irrelevantes o contradictorias del proceso de origen.
var sessionEnvKeys = []string{
	"DISPLAY",
	"WAYLAND_DISPLAY",
	"XAUTHORITY",
	"DBUS_SESSION_BUS_ADDRESS",
	"XDG_RUNTIME_DIR",
	"XDG_SESSION_TYPE",
	"XDG_CURRENT_DESKTOP",
	"PULSE_SERVER",
}

// findUserSession localiza la sesión gráfica activa.
//
// Dos pasos, en este orden por una razón: `loginctl` dice cuál es la sesión
// *activa* (la del asiento físico) cuando hay varios usuarios logueados, dato
// que escanear /proc no puede darnos. Pero `loginctl` no da el entorno, así que
// el segundo paso lo cosecha de un proceso real de ese usuario.
func (a *Agent) findUserSession() (*userSession, error) {
	logind, err := loginctlActiveSession()
	if err != nil {
		a.Logger.Debugln("findUserSession: loginctl no sirvió (" + err.Error() + "), se escanea /proc")
		uid, username, scanErr := scanProcForDesktopUser()
		if scanErr != nil {
			return nil, errNoUserSession
		}
		logind = &logindSession{uid: uid, username: username}
	}

	u, err := user.LookupId(strconv.FormatUint(uint64(logind.uid), 10))
	if err != nil {
		return nil, errNoUserSession
	}

	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return nil, errNoUserSession
	}

	s := &userSession{
		UID:       logind.uid,
		GID:       uint32(gid),
		Username:  logind.username,
		Home:      u.HomeDir,
		SessionID: logind.sessionID,
		Groups:    supplementaryGroups(u),
	}

	env := harvestSessionEnv(logind.uid)

	// Respaldos para lo que no se haya podido cosechar. XDG_RUNTIME_DIR y el bus
	// del usuario siguen una convención estable en systemd, así que reconstruirlos
	// es seguro.
	runtimeDir := filepath.Join("/run/user", strconv.FormatUint(uint64(logind.uid), 10))
	if _, ok := env["XDG_RUNTIME_DIR"]; !ok {
		env["XDG_RUNTIME_DIR"] = runtimeDir
	}
	if _, ok := env["DBUS_SESSION_BUS_ADDRESS"]; !ok {
		env["DBUS_SESSION_BUS_ADDRESS"] = "unix:path=" + filepath.Join(runtimeDir, "bus")
	}

	// DISPLAY: se prefiere el que reporta logind antes que adivinar `:0`.
	//
	// Esto no es teórico: medido en un equipo real con LightDM, el display de la
	// sesión gráfica era **`:10`**, y ningún proceso del usuario tenía DISPLAY en
	// su environ. Con `:0` fijo, todas las herramientas X11 habrían fallado.
	if _, okX := env["DISPLAY"]; !okX {
		if _, okW := env["WAYLAND_DISPLAY"]; !okW {
			if logind.display != "" {
				env["DISPLAY"] = logind.display
			} else {
				env["DISPLAY"] = ":0"
			}
		}
	}

	// XAUTHORITY: sin la cookie, X11 rechaza la conexión aunque DISPLAY esté bien.
	// Si no se cosechó de ningún proceso, se prueba la ubicación por convención en
	// el home del usuario, pero sólo si el archivo existe: apuntar a un archivo
	// inexistente es peor que no definir la variable, porque impide que la
	// herramienta use su propio valor por defecto.
	if _, ok := env["XAUTHORITY"]; !ok {
		xauth := filepath.Join(u.HomeDir, ".Xauthority")
		if _, err := os.Stat(xauth); err == nil {
			env["XAUTHORITY"] = xauth
		}
	}

	s.Env = []string{
		"HOME=" + u.HomeDir,
		"USER=" + logind.username,
		"LOGNAME=" + logind.username,
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"SHELL=/bin/sh",
	}
	for k, v := range env {
		s.Env = append(s.Env, k+"="+v)
	}

	return s, nil
}

// loginctlActiveSession pregunta a systemd-logind por la sesión activa.
//
// Se exige que sea gráfica (`x11`/`wayland`), de clase `user` y activa. Una
// sesión `tty` existe pero no tiene dónde dibujar un diálogo; el greeter del
// display manager sí es gráfico pero no hay nadie detrás; y una sesión inactiva
// es un usuario que dejó la sesión abierta en otro asiento.
// logindSession es lo que systemd-logind sabe de una sesión.
type logindSession struct {
	uid       uint32
	username  string
	sessionID string
	display   string
}

func loginctlActiveSession() (*logindSession, error) {
	out, err := exec.Command("loginctl", "list-sessions", "--no-legend").Output()
	if err != nil {
		return nil, err
	}

	var fallback *logindSession

	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 1 {
			continue
		}
		sessionID := fields[0]

		props, err := loginctlSessionProps(sessionID)
		if err != nil {
			continue
		}

		uid, err := strconv.ParseUint(props["User"], 10, 32)
		if err != nil {
			continue
		}
		name := props["Name"]
		if name == "" {
			continue
		}

		usable, active := sessionIsUsable(props)
		if !usable {
			continue
		}

		s := &logindSession{
			uid:       uint32(uid),
			username:  name,
			sessionID: sessionID,
			display:   props["Display"],
		}

		if active {
			return s, nil
		}

		// Una sesión gráfica no marcada como activa sirve como respaldo: pasa con
		// algunos display managers que reportan el estado con retraso.
		if fallback == nil {
			fallback = s
		}
	}

	if fallback != nil {
		return fallback, nil
	}

	return nil, fmt.Errorf("loginctl no reportó ninguna sesión gráfica")
}

// sessionIsUsable decide si en una sesión de logind hay una persona a la que se
// le puede dibujar algo. Vive aparte del recorrido para poder probarla con la
// salida real de `loginctl show-session` sin necesitar logind en el test.
//
// Los dos filtros hacen falta y ambos se midieron en equipos reales:
//
//   - `Type`: el propio display manager abre una sesión `manager-early` con
//     `Type=unspecified`, y las consolas quedan como `Type=tty`. Ninguna de las
//     dos tiene dónde dibujar un diálogo.
//   - `Class`: cuando NADIE ha entrado, el greeter de LightDM deja una sesión con
//     `Type=x11`, `Display=:0` y `Active=yes` que pasa el filtro de arriba.
//     Escribir ahí manda el mensaje a la PANTALLA DE LOGIN y la acción reporta
//     `ok` sin que ningún usuario la vea — exactamente la mentira que estos
//     códigos de error existen para evitar. Sólo `Class=user` es una persona.
//
// `active` se devuelve aparte porque una sesión gráfica de usuario que todavía no
// está marcada activa sirve como respaldo: algunos display managers reportan el
// estado con retraso.
func sessionIsUsable(props map[string]string) (usable, active bool) {
	graphical := props["Type"] == "x11" || props["Type"] == "wayland"
	userSess := props["Class"] == "user"
	active = props["Active"] == "yes" && props["State"] == "active"

	return graphical && userSess, active
}

func loginctlSessionProps(sessionID string) (map[string]string, error) {
	out, err := exec.Command("loginctl", "show-session", sessionID,
		"--property=Active", "--property=State", "--property=Type",
		"--property=Class", "--property=Name", "--property=User",
		"--property=Display").Output()
	if err != nil {
		return nil, err
	}

	return parseLoginctlProps(out), nil
}

// parseLoginctlProps se separó de la llamada para poder probarla sin depender de
// que la máquina de test tenga logind.
func parseLoginctlProps(out []byte) map[string]string {
	props := make(map[string]string, 6)
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			props[k] = v
		}
	}
	return props
}

// scanProcForDesktopUser es el respaldo para sistemas sin logind (o donde
// `loginctl` no está en el PATH del servicio): busca en /proc un proceso no-root
// que tenga un bus de sesión, que es la huella de un escritorio vivo.
// minUserUID devuelve el primer uid que corresponde a una persona y no a una
// cuenta de servicio. No se hardcodea 1000: la flota incluye distribuciones
// antiguas donde el umbral era 500, así que se lee UID_MIN de /etc/login.defs y
// 1000 queda sólo como respaldo (el valor de Debian/Ubuntu/RHEL actuales).
func minUserUID() uint32 {
	const fallback = 1000

	data, err := os.ReadFile("/etc/login.defs")
	if err != nil {
		return fallback
	}

	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "UID_MIN" {
			continue
		}
		if v, err := strconv.ParseUint(fields[1], 10, 32); err == nil && v > 0 {
			return uint32(v)
		}
	}

	return fallback
}

func scanProcForDesktopUser() (uint32, string, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, "", err
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}

		env, err := readProcEnviron(filepath.Join("/proc", e.Name(), "environ"))
		if err != nil {
			continue
		}
		if env["DBUS_SESSION_BUS_ADDRESS"] == "" {
			continue
		}
		if env["DISPLAY"] == "" && env["WAYLAND_DISPLAY"] == "" {
			continue
		}

		info, err := os.Stat(filepath.Join("/proc", e.Name()))
		if err != nil {
			continue
		}
		// Se descartan las cuentas de sistema, no sólo root. El greeter del display
		// manager corre como una cuenta de sistema (lightdm = uid 118) y SÍ tiene
		// DISPLAY y bus de sesión, así que sin este filtro este respaldo elegiría
		// la pantalla de login igual que hacía loginctl antes de exigir
		// `Class=user`.
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid < minUserUID() {
			continue
		}

		u, err := user.LookupId(strconv.FormatUint(uint64(st.Uid), 10))
		if err != nil {
			continue
		}
		return st.Uid, u.Username, nil
	}

	return 0, "", fmt.Errorf("no se encontró ningún proceso de escritorio en /proc")
}

// harvestSessionEnv recoge las variables de sesión del primer proceso del uid que
// las tenga. Se van acumulando entre procesos porque no todos las traen todas:
// un proceso de GNOME puede tener DBUS pero no XAUTHORITY y otro al revés.
func harvestSessionEnv(uid uint32) map[string]string {
	found := make(map[string]string, len(sessionEnvKeys))

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return found
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}

		procDir := filepath.Join("/proc", e.Name())
		info, err := os.Stat(procDir)
		if err != nil {
			continue
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != uid {
			continue
		}

		env, err := readProcEnviron(filepath.Join(procDir, "environ"))
		if err != nil {
			continue
		}

		for _, k := range sessionEnvKeys {
			if v := env[k]; v != "" {
				if _, already := found[k]; !already {
					found[k] = v
				}
			}
		}

		// Con el bus y algo donde dibujar ya alcanza; seguir leyendo /proc es gasto.
		if found["DBUS_SESSION_BUS_ADDRESS"] != "" &&
			(found["DISPLAY"] != "" || found["WAYLAND_DISPLAY"] != "") &&
			found["XDG_RUNTIME_DIR"] != "" {
			break
		}
	}

	return found
}

func readProcEnviron(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	env := make(map[string]string)
	for _, kv := range strings.Split(string(data), "\x00") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return env, nil
}

// supplementaryGroups devuelve los grupos secundarios del usuario. Importan para
// el audio: en equipos que reproducen por ALSA directo, sin el grupo `audio` no
// hay sonido. Si falla, se sigue sin grupos: el camino por PulseAudio/PipeWire
// depende del socket en XDG_RUNTIME_DIR, no de los grupos.
func supplementaryGroups(u *user.User) []uint32 {
	ids, err := u.GroupIds()
	if err != nil {
		return nil
	}

	groups := make([]uint32, 0, len(ids))
	for _, id := range ids {
		g, err := strconv.ParseUint(id, 10, 32)
		if err != nil {
			continue
		}
		groups = append(groups, uint32(g))
	}
	return groups
}

// newUserSessionCmd prepara un comando que corre como el usuario del escritorio,
// con su entorno de sesión.
//
// La bajada de privilegios se hace con `SysProcAttr.Credential` —el agente es
// root, así que puede— y no invocando `su`, `runuser` o `setpriv`. Es una
// dependencia externa menos y evita que un shell de login pise el entorno que
// acabamos de cosechar con esmero.
//
// `Setpgid` pone al hijo en su propio grupo de procesos, que es lo que permite
// después matar el árbol completo de la alarma de un golpe.
func (a *Agent) newUserSessionCmd(ctx context.Context, exe string, args ...string) (*exec.Cmd, error) {
	sess, err := a.findUserSession()
	if err != nil {
		return nil, err
	}
	return a.newSessionCmdFor(ctx, sess, exe, args...), nil
}

func (a *Agent) newSessionCmdFor(ctx context.Context, sess *userSession, exe string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = sess.Env
	cmd.Dir = sess.Home
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid:    sess.UID,
			Gid:    sess.GID,
			Groups: sess.Groups,
		},
		Setpgid: true,
	}
	return cmd
}

// killUserSessionCmd mata el grupo de procesos completo.
//
// El signo negativo es la clave: `kill(-pgid)` alcanza al shell del bucle y al
// reproductor que tenga lanzado en ese momento. Matar sólo el pid dejaría el
// último sonido sonando hasta terminar, que es exactamente lo que un operador
// que aprieta "detener" no espera.
func killUserSessionCmd(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	pid := cmd.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		// Si el grupo ya no existe o no se creó, se cae al pid suelto.
		_ = KillProc(int32(pid))
	}
}
