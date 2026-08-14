//go:build darwin
// +build darwin

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Feature 030 · Fase 2 · T016b/T016c — la ronda de permisos TCC en macOS.
//
// La lógica de "si toca" y el estado que se persiste están en tccpermisos.go,
// junto con el porqué de todo esto. Acá está el cómo.

const (
	// tccIdentificadorAgente es lo que se le pasa a `tccutil` para acotar el
	// reset a NUESTRO cliente. Debe coincidir con el CFBundleIdentifier de
	// agent/Info.darwin.plist y con el `--identifier` del `codesign` del
	// release.yml; si alguno de los tres se mueve sin los otros, el reset deja
	// de encontrar la fila (y falla de forma inocua, que es el lado correcto).
	tccIdentificadorAgente = "cl.observer.agent"

	// tccPlazoDialogo es lo que se espera a que la persona conteste. Un
	// diálogo de permiso puede quedarse en pantalla mucho rato; pasado el
	// plazo se cierra el intento y se sigue, que es mejor que dejar un proceso
	// colgado indefinidamente dentro del servicio.
	tccPlazoDialogo = 3 * time.Minute

	// tccPlazoConsulta acota la consulta de estado, que es instantánea salvo
	// que tccd esté ocupado.
	tccPlazoConsulta = 20 * time.Second

	// tccEsperaArranque separa la ronda del arranque del servicio. Con el
	// equipo recién encendido la sesión gráfica puede no estar lista, y un
	// diálogo lanzado contra una sesión a medio levantar es un diálogo que
	// nadie ve — y como el sistema sólo pregunta una vez, perderlo cuesta el
	// permiso entero hasta la ronda siguiente.
	tccEsperaArranque = 90 * time.Second

	// tccEsperaInstalacion es la espera cuando el servicio arranca recién
	// instalado. Ahí la sesión gráfica lleva rato levantada y hay una persona
	// mirando la consola del instalador: hacerla esperar un minuto y medio
	// frente a una terminal que ya terminó es la mejor forma de que se vaya
	// antes de que aparezca el diálogo.
	tccEsperaInstalacion = 10 * time.Second
)

// tccRondaEnCurso evita dos rondas simultáneas: la del arranque y una pedida a
// mano por el operador se pisarían mostrando dos diálogos del mismo permiso.
var tccRondaEnCurso sync.Mutex

// marcarPedidoDePermisosTCC deja la marca que el instalador usa para que el
// servicio pida los permisos en su primer arranque.
//
// El instalador NO pide los permisos él mismo, y es deliberado: corre desde
// Terminal.app y macOS atribuye el permiso al `responsible process`, o sea al
// terminal. La fila nacería a nombre de Terminal —que además ya suele tener
// cámara concedida, con lo que ni siquiera aparecería un diálogo— y el agente
// se quedaría sin nada. Medido el 2026-08-13: la misma captura funciona desde
// Terminal y falla desde el servicio, por exactamente este motivo.
func (a *Agent) marcarPedidoDePermisosTCC() {
	path := tccMarcaPath(a.lostModeDataDir())
	if err := os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0600); err != nil {
		a.Logger.Debugln("marcarPedidoDePermisosTCC:", err)
	}
}

// RondaPermisosTCC intenta dejar concedidos los permisos que el modo perdido
// necesita. Es idempotente y barata cuando ya están: una consulta por servicio.
//
// `forzar` lo ponen los dos casos en que hay una persona esperando —la marca
// del instalador y el `-m tccpermisos` manual— y salta el tope de rondas y la
// espera entre rondas, no las guardias de seguridad de más abajo.
func (a *Agent) RondaPermisosTCC(forzar bool) {
	if !tccRondaEnCurso.TryLock() {
		a.Logger.Debugln("RondaPermisosTCC: ya hay una ronda en curso")
		return
	}
	defer tccRondaEnCurso.Unlock()

	// 🚫 GUARDIA PRIMERA, la que no se puede perder: en un equipo marcado como
	// perdido, un diálogo de permiso le avisa al ladrón que alguien está
	// mirando. Toda la Fase 2 está construida para no delatarse; esto no puede
	// ser la excepción. Los permisos se piden en instalación y tras actualizar,
	// nunca durante un ciclo activo.
	if activo, _ := a.getLostMode(); activo {
		a.Logger.Infoln("RondaPermisosTCC: modo perdido activo, no se piden permisos")
		return
	}

	// Sin nadie en consola no hay quien conteste el diálogo. Además, sin sesión
	// gráfica el sistema puede denegar sin preguntar y eso GASTA el estado: la
	// fila queda como denegación y deja de haber diálogo posible.
	if _, _, err := consoleUser(); err != nil {
		a.Logger.Debugln("RondaPermisosTCC: sin sesión de consola, se posterga:", err)
		return
	}

	dir := a.lostModeDataDir()
	marca := tccMarcaPath(dir)
	if _, err := os.Stat(marca); err == nil {
		forzar = true
		os.Remove(marca)
	}

	ident := identidadDelEjecutable(a.Version)
	st, cambioDeBinario := alinearEstadoTCC(a.cargarEstadoTCC(), ident)
	if cambioDeBinario {
		// Este es el disparador de T016c. No hace falta detectar "hubo un
		// update": el binario cambió, y eso es exactamente lo que rompe las
		// filas de TCC.
		a.Logger.Infoln("RondaPermisosTCC: el binario del agente cambió; los permisos de macOS pudieron perderse en el camino")
	}

	ahora := time.Now().UTC()
	pendientesAvisar := []string{}

	for _, servicio := range tccServiciosGestionados {
		reg := st.Servicios[servicio]

		if !debeCorrerRondaTCC(reg, ahora, forzar) {
			a.Logger.Debugln("RondaPermisosTCC:", servicio, "no toca en esta ronda")
			if !reg.Concedido {
				pendientesAvisar = append(pendientesAvisar, servicio)
			}
			continue
		}

		reg = a.escalarPermisoTCC(servicio, reg, ahora)
		st.Servicios[servicio] = reg

		if !reg.Concedido && !reg.Aviso {
			pendientesAvisar = append(pendientesAvisar, servicio)
		}
	}

	// El aviso se muestra UNA vez por identidad del binario y agrupado: dos
	// diálogos seguidos por dos permisos distintos se leen como un fallo del
	// producto, y quien los cierra en cadena no lee ninguno.
	if len(pendientesAvisar) > 0 {
		a.avisarPermisosTCC(pendientesAvisar)
		for _, s := range pendientesAvisar {
			reg := st.Servicios[s]
			reg.Aviso = true
			st.Servicios[s] = reg
		}
	}

	a.guardarEstadoTCC(st)
}

// escalarPermisoTCC es la escalada de un servicio, de lo barato a lo invasivo.
// Cada paso VERIFICA su efecto consultando el estado otra vez: acá el `exit 0`
// no prueba nada, y el propio `granted=NO` del sistema es ambiguo (una
// denegación de política se ve igual que una persona diciendo que no).
func (a *Agent) escalarPermisoTCC(servicio string, reg intentoTCC, ahora time.Time) intentoTCC {
	reg.Rondas++
	reg.Ultima = ahora

	estado, err := a.estadoPermisoTCC(servicio)
	if err != nil {
		a.Logger.Debugln("escalarPermisoTCC:", servicio, "no se pudo consultar:", err)
		reg.UltimoPaso = "consulta_fallida"
		return reg
	}

	if estado == tccEstadoConcedido {
		a.Logger.Debugln("escalarPermisoTCC:", servicio, "ya está concedido")
		reg.Concedido = true
		reg.UltimoPaso = "ya_concedido"
		return reg
	}

	if estado == tccEstadoRestringido {
		// Restringido es política del equipo (perfil de configuración, control
		// parental). No lo puede levantar ni la persona ni el agente, así que
		// insistir sólo produce ruido.
		a.Logger.Infoln("escalarPermisoTCC:", servicio, "está RESTRINGIDO por política del equipo; no hay diálogo posible")
		reg.UltimoPaso = "restringido"
		return reg
	}

	// Paso 1 — el diálogo, como hijo DIRECTO del servicio. Es el camino medido
	// el 2026-08-13: `tccd` publicó `AUTHREQ_PROMPTING` con
	// `Sub:{/opt/observeragent/observeragent}` y la fila nació a nombre del
	// agente, con su cdhash vigente y `auth_reason=2` (consentimiento).
	if a.pedirPermisoTCC(servicio, false) {
		reg.Concedido = true
		reg.UltimoPaso = "dialogo_servicio"
		return reg
	}

	// Paso 2 — el mismo diálogo, pero desde la sesión gráfica del usuario.
	// 🟡 NO MEDIDO: se agrega porque la cámara enseñó que el contexto de
	// lanzamiento decide cosas que parecían decididas por el permiso, y porque
	// las APIs de pantalla hablan con el servidor de ventanas. Si en terreno
	// resulta que el paso 1 basta siempre, este paso no cuesta nada (no se
	// llega a él). `launchctl asuser` no rompe la atribución: con el servicio
	// como padre, el `responsible` sigue siendo el agente.
	if a.pedirPermisoTCC(servicio, true) {
		reg.Concedido = true
		reg.UltimoPaso = "dialogo_sesion_usuario"
		return reg
	}

	// Paso 3 — el reset acotado. Acá estamos en el caso de T016c: la fila
	// quedó como denegación (o como permiso rancio anclado a un cdhash viejo),
	// y con una fila así el sistema YA NO PREGUNTA. Borrarla devuelve el
	// servicio a "no determinado", que es el único estado en que existe el
	// diálogo.
	//
	// 🟡 NO MEDIDO que `tccutil` acepte a este cliente: el agente es un
	// ejecutable suelto, no un bundle, y TCC lo indexa por ruta. Por eso se
	// prueba con el identificador y con la ruta, y se VERIFICA leyendo el
	// estado después — si no cambió nada, no cambió nada.
	//
	// 🚫 Nunca un reset global: `tccutil reset ScreenCapture` a secas barrería
	// también el navegador, las apps de videollamada, el escritorio remoto y el
	// meshagent de este mismo producto.
	if !reg.UsoReset {
		reg.UsoReset = true
		if a.resetPermisoTCC(servicio) {
			if a.pedirPermisoTCC(servicio, false) || a.pedirPermisoTCC(servicio, true) {
				reg.Concedido = true
				reg.UltimoPaso = "reset_y_dialogo"
				return reg
			}
		}
	}

	a.Logger.Infoln("escalarPermisoTCC:", servicio, "sigue sin concederse tras agotar los intentos")
	reg.UltimoPaso = "sin_dialogo_posible"
	return reg
}

// estadoPermisoTCC consulta el permiso SIN efectos secundarios.
//
// Se pregunta con `osascript` cargando el framework, y no con cgo, por dos
// razones concretas: es EXACTAMENTE la consulta que ya se midió respondiendo en
// el Mac de terreno (la cámara devolvió `0` y después de conceder, `3`), y
// sirve igual en un binario compilado sin cgo. Los dos scripts viven en
// archivos sin etiqueta de plataforma para que la CI los vigile.
//
// La consulta va por la sesión del usuario porque es la misma cadena que usa la
// captura: lo que interesa no es lo que TCC opine en abstracto sino lo que le
// va a responder al camino por el que se saca la evidencia. El `responsible`
// sigue siendo el agente —lo publica `tccd`— aunque el proceso sea `osascript`.
//
// 🪤 La pantalla NO tiene escala: `CGPreflightScreenCaptureAccess` es un
// booleano, así que un permiso denegado y uno sin determinar se ven idénticos
// desde acá. Por eso la escalada intenta primero el diálogo (que no muestra
// nada si la fila es una denegación) y sólo después el reset.
func (a *Agent) estadoPermisoTCC(servicio string) (int, error) {
	script := scriptEstadoTCCCamara
	if servicio == tccServicioPantalla {
		script = scriptPreflightPantalla
	}

	ctx, cancel := context.WithTimeout(context.Background(), tccPlazoConsulta)
	defer cancel()

	cmd, err := a.newUserSessionCmd(ctx, "/usr/bin/osascript", "-e", script)
	if err != nil {
		return 0, err
	}

	salida, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	valor := strings.TrimSpace(string(salida))

	if servicio == tccServicioPantalla {
		if valor == "1" {
			return tccEstadoConcedido, nil
		}
		return tccEstadoNoDeterminado, nil
	}

	estado, err := strconv.Atoi(valor)
	if err != nil {
		return 0, fmt.Errorf("respuesta inesperada de TCC: %q", valor)
	}
	return estado, nil
}

// pedirPermisoTCC dispara el diálogo del sistema y verifica el resultado.
//
// El diálogo se pide en un proceso HIJO y no acá dentro por dos razones: el
// servicio no puede quedarse bloqueado tres minutos esperando a que alguien
// conteste, y un fallo dentro de AVFoundation se lleva al hijo y no al agente.
func (a *Agent) pedirPermisoTCC(servicio string, viaSesionUsuario bool) bool {
	self, err := os.Executable()
	if err != nil {
		a.Logger.Debugln("pedirPermisoTCC: os.Executable():", err)
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), tccPlazoDialogo)
	defer cancel()

	var cmd *exec.Cmd
	if viaSesionUsuario {
		cmd, err = a.newUserSessionCmd(ctx, self, "-m", "tccpedir", servicio)
		if err != nil {
			a.Logger.Debugln("pedirPermisoTCC: sin sesión de usuario:", err)
			return false
		}
	} else {
		cmd = exec.CommandContext(ctx, self, "-m", "tccpedir", servicio)
	}

	salida, err := cmd.Output()
	if err != nil {
		a.Logger.Debugln("pedirPermisoTCC:", servicio, "el hijo falló:", err)
		return false
	}
	a.Logger.Debugln("pedirPermisoTCC:", servicio, "sesion_usuario:", viaSesionUsuario,
		"respuesta:", strings.TrimSpace(string(salida)))

	// Lo que dijo el hijo es informativo. Lo que decide es el estado que ve el
	// servicio, que es el sujeto real del permiso.
	estado, err := a.estadoPermisoTCC(servicio)
	if err != nil {
		return false
	}
	return estado == tccEstadoConcedido
}

// resetPermisoTCC borra la fila de ESTE cliente y confirma que quedó borrada.
func (a *Agent) resetPermisoTCC(servicio string) bool {
	self, _ := os.Executable()

	clientes := []string{tccIdentificadorAgente}
	if self != "" {
		clientes = append(clientes, self)
	}

	for _, cliente := range clientes {
		if cliente == "" {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), tccPlazoConsulta)
		salida, err := exec.CommandContext(ctx, "/usr/bin/tccutil", "reset", servicio, cliente).CombinedOutput()
		cancel()

		a.Logger.Debugln("resetPermisoTCC:", servicio, cliente, "err:", err,
			"salida:", strings.TrimSpace(string(salida)))

		// El `exit 0` de tccutil no prueba que haya borrado nada: sale 0
		// aunque el cliente no exista. Lo que prueba el efecto es que el
		// servicio vuelva a "no determinado".
		if estado, e := a.estadoPermisoTCC(servicio); e == nil && estado == tccEstadoNoDeterminado {
			a.Logger.Infoln("resetPermisoTCC:", servicio, "vuelto a 'no determinado' con", cliente)
			return true
		}
	}

	a.Logger.Infoln("resetPermisoTCC:", servicio, "no se pudo devolver a 'no determinado'")
	return false
}

// avisarPermisosTCC le dice a la persona lo que el sistema ya no le va a
// preguntar, y abre el panel exacto de Ajustes del primer permiso pendiente.
//
// El panel se abre a modo informativo: está MEDIDO que apagar y encender el
// interruptor no re-ancla el requisito, así que esto no es "la solución" y el
// texto del aviso no la promete. Lo que resuelve de verdad es reinstalar el
// agente o firmarlo con Developer ID (ADR-018).
func (a *Agent) avisarPermisosTCC(servicios []string) {
	titulo, cuerpo := mensajeAvisoTCC(servicios)

	if err := a.ShowAlert(titulo, cuerpo); err != nil {
		a.Logger.Debugln("avisarPermisosTCC: no se pudo mostrar el aviso:", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), tccPlazoConsulta)
	defer cancel()

	cmd, err := a.newUserSessionCmd(ctx, "/usr/bin/open", panelAjustesTCC(servicios[0]))
	if err != nil {
		return
	}
	if err := cmd.Run(); err != nil {
		a.Logger.Debugln("avisarPermisosTCC: no se pudo abrir Ajustes:", err)
	}
}

func (a *Agent) cargarEstadoTCC() estadoTCC {
	var st estadoTCC

	data, err := os.ReadFile(tccEstadoPath(a.lostModeDataDir()))
	if err != nil {
		return st
	}
	if err := json.Unmarshal(data, &st); err != nil {
		a.Logger.Debugln("cargarEstadoTCC: archivo ilegible, se empieza de cero:", err)
		return estadoTCC{}
	}
	return st
}

// guardarEstadoTCC escribe temporal + rename, igual que el estado del modo
// perdido: un JSON truncado se comporta como "no había nada", y acá eso
// significaría volver a preguntar permisos que ya se pidieron.
func (a *Agent) guardarEstadoTCC(st estadoTCC) {
	path := tccEstadoPath(a.lostModeDataDir())

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		a.Logger.Debugln("guardarEstadoTCC:", err)
		return
	}

	data, err := json.Marshal(st)
	if err != nil {
		a.Logger.Debugln("guardarEstadoTCC:", err)
		return
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		a.Logger.Debugln("guardarEstadoTCC:", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		a.Logger.Debugln("guardarEstadoTCC:", err)
		os.Remove(tmp)
	}
}

// LanzarRondaPermisosTCC la corre en segundo plano tras el arranque del
// servicio. La espera no es cosmética: con el equipo recién encendido la sesión
// gráfica puede no estar lista, y un diálogo contra una sesión a medio levantar
// se pierde — gastando el único disparo que había.
func (a *Agent) LanzarRondaPermisosTCC() {
	espera := tccEsperaArranque
	if _, err := os.Stat(tccMarcaPath(a.lostModeDataDir())); err == nil {
		espera = tccEsperaInstalacion
	}

	go func() {
		time.Sleep(espera)
		a.RondaPermisosTCC(false)
	}()
}

// RunPedirPermisoTCC es el modo `-m tccpedir`: el proceso hijo que DISPARA el
// diálogo del sistema. Imprime una sola línea que el padre registra, con el
// mismo formato de `-m geoprobe` y `-m screencapture`.
//
// Los dos permisos se piden por caminos distintos y no por gusto:
//
//   - **Pantalla**: `CGRequestScreenCaptureAccess()` es sincrónica y devuelve un
//     booleano, así que AppleScript la puede invocar tal cual — el mismo truco
//     del preflight, sin compilar nada.
//   - **Cámara**: `requestAccessForMediaType:completionHandler:` recibe un
//     BLOQUE de Objective-C, y AppleScript no sabe construir bloques. No hay
//     forma de pedirlo sin cgo; de ahí el puente de tccpermisos_darwin.m.
//
// Sin cgo (compilación con CGO_ENABLED=0, que es sólo un camino de desarrollo:
// el release de macOS lleva cgo desde la 025) la cámara no se puede pedir y se
// dice así, en vez de fingir que se intentó.
func (a *Agent) RunPedirPermisoTCC(servicio string) {
	switch servicio {
	case tccServicioPantalla:
		ctx, cancel := context.WithTimeout(context.Background(), tccPlazoDialogo)
		defer cancel()

		salida, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", scriptPedirPantalla).Output()
		if err != nil {
			fmt.Println("FAIL osascript:" + err.Error())
			return
		}
		fmt.Println("OK " + strings.TrimSpace(string(salida)))

	case tccServicioCamara:
		estado, err := tccPedirCamaraNativo(tccPlazoDialogo)
		if err != nil {
			fmt.Println("FAIL " + err.Error())
			return
		}
		fmt.Println("OK " + strconv.Itoa(estado))

	default:
		fmt.Println("FAIL servicio_desconocido")
	}
}
