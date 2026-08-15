//go:build darwin
// +build darwin

package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Feature 030 · Fase 2 · T016b/T016c — la ronda de permisos TCC en macOS.
//
// La lógica de "si toca" y el estado que se persiste están en tccpermisos.go,
// junto con el porqué de todo esto. Acá está el cómo.

const (
	// tccIdentificadorAgente es la identidad con la que se firma el binario.
	// Debe coincidir con el CFBundleIdentifier de agent/Info.darwin.plist y con
	// el `--identifier` del `codesign` de release.yml.
	//
	// 🪤 No sirve para hablarle a `tccutil`: está medido que rechaza este
	// cliente en todas sus formas porque el agente no es un bundle (ver la nota
	// de "NO HAY PASO 3" más abajo). Se conserva porque es la identidad de
	// firma, que es lo que pasa a importar con Developer ID (ADR-018).
	tccIdentificadorAgente = "cl.observer.agent"

	// tccPlazoDialogo es lo que se espera a que la persona conteste. Un
	// diálogo de permiso puede quedarse en pantalla mucho rato; pasado el
	// plazo se cierra el intento y se sigue, que es mejor que dejar un proceso
	// colgado indefinidamente dentro del servicio.
	tccPlazoDialogo = 3 * time.Minute

	// tccPlazoConsulta acota la consulta de estado, que es instantánea salvo
	// que tccd esté ocupado.
	tccPlazoConsulta = 20 * time.Second

	// tccPlazoConcesionPantalla es lo que se espera a que la concesión de
	// Grabación de pantalla APAREZCA, después de haberla pedido.
	//
	// 🔑 Existe porque las dos APIs no se comportan igual, y creer lo contrario
	// costó un aviso falso en terreno: `requestAccessForMediaType` (cámara)
	// recibe un bloque y contesta cuando la persona contesta, pero
	// `CGRequestScreenCaptureAccess` (pantalla) NO espera — abre el diálogo,
	// devuelve el estado de ESE instante, que es "todavía no", y se va. Medido
	// el 2026-08-15: la ronda se dio por vencida a las 20:27:53 y la fila quedó
	// concedida a las 20:28:11, dieciocho segundos después.
	//
	// El plazo se parece al del diálogo porque el trabajo humano es el mismo, y
	// en la pantalla es más largo: el sistema no concede desde el diálogo, manda
	// a Ajustes.
	tccPlazoConcesionPantalla = 3 * time.Minute

	// tccSondeoConcesion separa dos consultas de la espera de arriba. Cada una
	// despierta al ayudante, así que no puede ser un sondeo apretado.
	tccSondeoConcesion = 15 * time.Second

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

	// Paso 1 — el diálogo, disparado POR EL AYUDANTE.
	//
	// 🔑 Tiene que pedirlo él y no el servicio, porque desde T016 el que captura
	// es él: un permiso concedido al servicio quedaría a nombre de un cliente que
	// ya no abre la cámara ni la pantalla. El sujeto del permiso es el
	// `responsible process`, y el del ayudante es su propio bundle.
	if a.pedirYEsperarTCC(servicio) == tccEstadoConcedido {
		reg.Concedido = true
		reg.UltimoPaso = "dialogo_ayudante"
		return reg
	}

	// Paso 2 — `tccutil reset` acotado al bundle, y volver a preguntar.
	//
	// Este paso EXISTIÓ, se borró el 2026-08-14 porque no funcionaba, y vuelve
	// ahora porque el ayudante es un `.app`. Vale la pena dejar escrito el porqué
	// de las dos cosas:
	//
	// Fallaba con `rc=64 · No such bundle identifier` en sus cuatro formas
	// (identificador y ruta × root y usuario de consola), y se archivó como
	// «tccutil sólo entiende bundles y el agente es un ejecutable suelto». La
	// medición del 2026-08-15 mostró que el diagnóstico era incompleto:
	// `com.apple.screensharing.agent` —un identificador de Apple perfectamente
	// real— falla EXACTAMENTE IGUAL, porque `tccutil` resuelve por
	// LaunchServices y sólo acepta un `.app`. Un `.bundle` tampoco sirve.
	//
	// Con el ayudante empaquetado y registrado con `lsregister`, el reset
	// responde `Successfully reset` y la fila vuelve a «no determinado» — el
	// único estado en que el sistema pregunta de nuevo. Y esto es lo que
	// convierte a T016c en algo que REPARA en vez de sólo avisar.
	//
	// 🪤 La verificación compara contra el estado PREVIO. La versión anterior
	// daba el reset por bueno con sólo ver «no determinado» al final, así que un
	// servicio que YA estaba ahí —el caso de Grabación de pantalla el
	// 2026-08-14— declaraba éxito sin haber hecho nada.
	if a.valeLaPenaResetear(servicio, estado) {
		if a.pedirYEsperarTCC(servicio) == tccEstadoConcedido {
			reg.Concedido = true
			reg.UltimoPaso = "dialogo_tras_reset"
			return reg
		}
		reg.UltimoPaso = "reset_sin_concesion"
		return reg
	}

	a.Logger.Infoln("escalarPermisoTCC:", servicio, "sigue sin concederse tras agotar los intentos")
	reg.UltimoPaso = "sin_dialogo_posible"
	return reg
}

// estadoPermisoTCC consulta el permiso SIN efectos secundarios.
//
// 🔑 SE LE PREGUNTA AL AYUDANTE, no al servicio. Lo que interesa no es lo que
// TCC opine en abstracto sino lo que le va a responder al camino por el que se
// saca la evidencia, y desde T016 ese camino es el bundle. Preguntado desde el
// servicio, el estado sería el del servicio: un dato correcto sobre el proceso
// equivocado, que es la peor clase de dato.
//
// 🪤 La pantalla NO tiene escala: `CGPreflightScreenCaptureAccess` es un
// booleano, así que un permiso denegado y uno sin determinar se ven idénticos
// desde acá. Por eso la escalada intenta primero el diálogo —que no muestra nada
// si la fila es una denegación— y sólo después el reset.
func (a *Agent) estadoPermisoTCC(servicio string) (int, error) {
	resp, err := a.consultarAlAyudante(capturaTipoEstado, servicio, tccPlazoConsulta)
	if err != nil {
		return 0, err
	}
	return resp.Estado, nil
}

// pedirPermisoTCC dispara el diálogo del sistema y devuelve el estado resultante.
//
// El diálogo lo muestra el ayudante, en un proceso aparte, y no el servicio.
// Dos razones y las dos importan: el sujeto del permiso tiene que ser el bundle
// (ver `estadoPermisoTCC`), y el servicio no puede quedarse bloqueado tres
// minutos esperando a que alguien conteste.
//
// Se devuelve el ESTADO y no un booleano: el booleano diría qué contestó la
// persona, y lo que importa es en qué quedó la fila de TCC.
// pedirYEsperarTCC pide el permiso y, si el sistema todavía no lo dio, espera a
// que aparezca en vez de declarar el fracaso en el acto.
//
// La espera es SÓLO para la pantalla, y no por prudencia: la cámara no la
// necesita porque su API ya contesta cuando la persona contesta. Esperar ahí
// también sería tiempo muerto por un permiso que ya está resuelto.
//
// 🪤 Cada consulta despierta un ayudante NUEVO, y eso es parte del arreglo, no un
// costo: macOS cachea el estado de Grabación de pantalla por proceso, así que
// preguntar de nuevo dentro del mismo ayudante devolvería el "todavía no" de
// siempre por mucho que la persona ya haya concedido.
func (a *Agent) pedirYEsperarTCC(servicio string) int {
	if estado := a.pedirPermisoTCC(servicio); estado == tccEstadoConcedido {
		return estado
	}

	if servicio != tccServicioPantalla {
		return tccEstadoNoDeterminado
	}

	limite := time.Now().Add(tccPlazoConcesionPantalla)
	for time.Now().Before(limite) {
		time.Sleep(tccSondeoConcesion)

		estado, err := a.estadoPermisoTCC(servicio)
		if err != nil {
			a.Logger.Debugln("pedirYEsperarTCC:", servicio, "no se pudo consultar:", err)
			continue
		}
		if estado == tccEstadoConcedido {
			a.Logger.Infoln("pedirYEsperarTCC:", servicio, "concedido mientras se esperaba")
			return tccEstadoConcedido
		}
	}

	return tccEstadoNoDeterminado
}

func (a *Agent) pedirPermisoTCC(servicio string) int {
	// El plazo del lado del servicio va holgado sobre el del diálogo: si el
	// ayudante se cuelga, el que espera tiene que rendirse DESPUÉS que él, no
	// antes, o se pierde una respuesta que sí llegó.
	resp, err := a.consultarAlAyudante(capturaTipoPermiso, servicio, tccPlazoDialogo+30*time.Second)
	if err != nil {
		a.Logger.Debugln("pedirPermisoTCC:", servicio, err)
		return tccEstadoNoDeterminado
	}
	return resp.Estado
}

// consultarAlAyudante despierta al ayudante para una pregunta de permisos.
func (a *Agent) consultarAlAyudante(tipo, servicio string, plazo time.Duration) (capturaAquaRespuesta, error) {
	uid, _, err := consoleUser()
	if err != nil {
		return capturaAquaRespuesta{}, err
	}
	if err := a.prepararAyudanteCaptura(uid); err != nil {
		return capturaAquaRespuesta{}, err
	}
	return a.encargarAlAyudante(capturaAquaPedido{
		ID:       nonceCaptura(),
		Tipo:     tipo,
		Servicio: servicio,
	}, uid, plazo)
}

// resetearPermisoTCC devuelve la fila del ayudante a "no determinado".
//
// Corre como root y desde el SERVICIO, no desde el ayudante: `tccutil` necesita
// alcanzar la TCC.db del sistema —donde vive la fila de Grabación de pantalla—
// y está medido que lo consigue con SIP activo, porque el trabajo lo hace `tccd`
// y no un `sqlite` directo.
//
// 🪤 EL `rc=0` NO PRUEBA NADA, y acá ya costó una vez: se compara el estado
// contra el que había antes. Un `Successfully reset` sobre una fila que ya
// estaba en "no determinado" es indistinguible de uno que arregló algo.
// valeLaPenaResetear decide si el paso 2 tiene algo que reparar, y lo repara.
//
// 🔑 LA PANTALLA NO SE PUEDE JUZGAR POR SU ESTADO, y creer que sí dejó al paso 2
// muerto exactamente en el caso para el que existe.
//
// `CGPreflightScreenCaptureAccess` es un booleano, así que una fila VIEJA —la
// que quedó con el csreq del ayudante anterior— se lee IGUAL que "nunca se
// preguntó". Medido el 2026-08-15 al actualizar a la v2.15.23: la fila decía
// "permitido" con el csreq de antes del update, Ajustes la mostraba encendida,
// el preflight respondía que no, y la escalada se saltaba el reset porque el
// estado le parecía "no determinado". La captura quedaba muerta con la
// reparación a un paso.
//
// Por eso, en la pantalla, ni se condiciona por el estado ni se verifica por él
// —no puede moverse—: el testigo es el rc de `tccutil` (0 hizo algo, 64 no
// resolvió el bundle) y la prueba de verdad es la concesión que viene después.
// Medido el mismo día: `rc=0` y la fila muerta desaparece; con un identificador
// inventado, `rc=64`.
func (a *Agent) valeLaPenaResetear(servicio string, estado int) bool {
	if servicio == tccServicioPantalla {
		return a.ejecutarResetTCC(servicio)
	}
	return estado != tccEstadoNoDeterminado && a.resetearPermisoTCC(servicio, estado)
}

// ejecutarResetTCC corre el reset acotado al bundle y devuelve si tccutil hizo
// algo. No mira el estado: eso es trabajo de quien lo llama, y hay un servicio
// donde no se puede.
func (a *Agent) ejecutarResetTCC(servicio string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), tccPlazoConsulta)
	defer cancel()

	salida, err := exec.CommandContext(ctx, "/usr/bin/tccutil",
		"reset", nombreServicioTCC(servicio), capturaAquaID).CombinedOutput()
	if err != nil {
		a.Logger.Debugln("ejecutarResetTCC:", servicio, err, strings.TrimSpace(string(salida)))
		return false
	}
	return true
}

func (a *Agent) resetearPermisoTCC(servicio string, estadoPrevio int) bool {
	if !a.ejecutarResetTCC(servicio) {
		return false
	}

	estado, err := a.estadoPermisoTCC(servicio)
	if err != nil {
		a.Logger.Debugln("resetearPermisoTCC: no se pudo verificar el efecto:", err)
		return false
	}
	if estado == estadoPrevio {
		a.Logger.Debugln("resetearPermisoTCC:", servicio, "dijo que si y el estado no se movio de", estado)
		return false
	}

	a.Logger.Infoln("resetearPermisoTCC:", servicio, "vuelto a no determinado; se puede volver a preguntar")
	return true
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
