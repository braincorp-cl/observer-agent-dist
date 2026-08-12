package agent

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	nats "github.com/nats-io/nats.go"
)

// geoMinInterval es el piso de la captura de ubicación, en segundos. La geo es una
// feature de baja frecuencia por diseño: seguir a alguien de cerca no es el caso de
// uso, y un intervalo corto por config —o por un error de tipeo en el servidor—
// convertiría la flota en un rastreador.
const geoMinInterval = 300

func (a *Agent) RunAsService(nc *nats.Conn) {
	var wg sync.WaitGroup
	wg.Add(1)
	go a.AgentSvc(nc)
	go a.CheckRunner()
	wg.Wait()
}

type AgentCheckInConfig struct {
	Hello                  int    `json:"checkin_hello"`
	AgentInfo              int    `json:"checkin_agentinfo"`
	WinSvc                 int    `json:"checkin_winsvc"`
	PubIP                  int    `json:"checkin_pubip"`
	Disks                  int    `json:"checkin_disks"`
	SW                     int    `json:"checkin_sw"`
	WMI                    int    `json:"checkin_wmi"`
	SyncMesh               int    `json:"checkin_syncmesh"`
	LimitData              bool   `json:"limit_data"`
	InstallNushell         bool   `json:"install_nushell"`
	InstallNushellVersion  string `json:"install_nushell_version"`
	InstallNushellUrl      string `json:"install_nushell_url"`
	NushellEnableConfig    bool   `json:"nushell_enable_config"`
	InstallDeno            bool   `json:"install_deno"`
	InstallDenoVersion     string `json:"install_deno_version"`
	InstallDenoUrl         string `json:"install_deno_url"`
	DenoDefaultPermissions string `json:"deno_default_permissions"`
	// Feature 023: interruptor global de geolocalización + intervalo de captura.
	GeoEnabled bool `json:"geo_enabled"`
	Geo        int  `json:"checkin_geo"`
	// Gap 3: force-on del sensor de ubicación/radio WiFi en el endpoint corporativo.
	GeoForceOn bool `json:"geo_force_on"`
	// Feature 030: PRIMER campo per-agente de este endpoint. Es el canal de
	// respaldo del push por NATS — un equipo que estaba apagado cuando lo
	// marcaron se entera acá al reconectar.
	LostMode            bool `json:"lost_mode"`
	LostModeIntervalMin int  `json:"lost_mode_interval_min"`
	// fromServer distingue una config REAL de la del repliegue. Sin exportar y
	// sin etiqueta JSON: no viaja por la red, lo pone GetCheckInConfFromAPI.
	// Existe por el modo perdido — ver el comentario en checkInConfFallback.
	fromServer bool
}

func (a *Agent) AgentSvc(nc *nats.Conn) {
	a.RunMigrations()

	if runtime.GOOS == "windows" {
		go a.GetPython(false)

		err := createWinTempDir()
		if err != nil {
			a.Logger.Errorln("AgentSvc() createWinTempDir():", err)
		}
	}

	// Feature 028 Fase 2. Va acá, antes del sleep de arranque, y después de
	// createWinTempDir porque en Windows el archivo de estado vive en ese mismo
	// directorio. Dos cosas pueden estar pendientes de la ejecución anterior:
	// una alarma eterna que debe volver a sonar, y un volumen subido que hay que
	// devolver. La segunda importa incluso cuando la primera no aplica — si el
	// agente murió a mitad de alarma, el equipo quedó al máximo.
	a.ResumeAlarm()

	// Feature 030. Va junto a ResumeAlarm y por la misma razón: un equipo robado
	// se reinicia, y sin esto la captura quedaría apagada hasta el próximo push
	// o el próximo tick de config — que en un equipo que ya no vuelve a la red
	// puede no llegar nunca.
	a.ResumeLostMode()

	// Sync temprano del mesh node id justo tras instalar, para que el server
	// conozca la conectividad Mesh sin esperar al primer tick (backport v2.11.0
	// "get meshid after install"). Solo unix; en Windows el install de Mesh es
	// síncrono. Sin el campo run_sync_task: nuestro backend lo ignora.
	if runtime.GOOS != "windows" {
		go a.SyncMeshNodeID()
	}

	sleepDelay := randRange(7, 25)
	a.Logger.Debugf("AgentSvc() sleeping for %v seconds", sleepDelay)
	time.Sleep(time.Duration(sleepDelay) * time.Second)

	if runtime.GOOS == "windows" {
		a.KillHungUpdates()
		time.Sleep(1 * time.Second)
		a.CleanupAgentUpdates()
	}

	// Windows has GetAgentCheckInConfig() while unix has a stub GetAgentCheckInConfig()
	conf := a.GetAgentCheckInConfig(a.GetCheckInConfFromAPI())
	a.Logger.Debugf("AgentCheckInConf: %+v\n", conf)

	// Feature 030 · esta config YA trae el estado de modo perdido, y hasta el
	// 2026-08-11 se ignoraba: sólo el primer tick de geo lo aplicaba. Medido en
	// terreno contra staging: un equipo marcado mientras estaba apagado tardó
	// 5 min 02 s desde que arrancó el servicio hasta empezar a capturar, y ese
	// número es el `checkin_geo` del ambiente —con el default del producto
	// (1500-2100 s) serían 25 a 35 minutos de un equipo robado moviéndose sin
	// que se registre nada. El dato estaba acá desde el primer segundo.
	//
	// Mismo resguardo que en el tick: sólo se aplica si la config vino DE VERDAD
	// del servidor. Con el repliegue (sin red al arrancar, que es lo normal en un
	// portátil recién encendido) todos los campos son ceros, y ese `lost_mode=false`
	// apagaría el estado que ResumeLostMode acaba de restaurar desde el disco.
	if conf.fromServer {
		a.SetLostMode(conf.LostMode, conf.LostModeIntervalMin)
	}
	// Gap 3: propaga el force-on al Agent para que osLocation lo lea antes de
	// escanear. Global; solo el backend lo enciende (y solo si geo_tracking=ON).
	a.GeoForceOn = conf.GeoForceOn
	for _, s := range natsCheckin {
		if conf.LimitData && stringInSlice(s, limitNatsData) {
			continue
		} else {
			a.NatsMessage(nc, s)
			time.Sleep(time.Duration(randRange(100, 400)) * time.Millisecond)
		}
	}

	// The server conf check is also done in the functions to keep the parameters the same.
	// Don't force a download when restarting the service.
	if conf.InstallNushell {
		go a.InstallNushell(false)
	}
	if conf.InstallDeno {
		go a.InstallDeno(false)
	}

	go a.SyncMeshNodeID()

	time.Sleep(time.Duration(randRange(1, 3)) * time.Second)
	if runtime.GOOS == "windows" && !conf.LimitData {
		a.AgentStartup()
		a.SendSoftware()
	}

	if runtime.GOOS == "darwin" {
		go a.FixVenturaMesh()
	}

	checkInHelloTicker := time.NewTicker(time.Duration(conf.Hello) * time.Second)
	checkInAgentInfoTicker := time.NewTicker(time.Duration(conf.AgentInfo) * time.Second)
	checkInWinSvcTicker := time.NewTicker(time.Duration(conf.WinSvc) * time.Second)
	checkInPubIPTicker := time.NewTicker(time.Duration(conf.PubIP) * time.Second)
	checkInDisksTicker := time.NewTicker(time.Duration(conf.Disks) * time.Second)
	checkInSWTicker := time.NewTicker(time.Duration(conf.SW) * time.Second)
	checkInWMITicker := time.NewTicker(time.Duration(conf.WMI) * time.Second)
	syncMeshTicker := time.NewTicker(time.Duration(conf.SyncMesh) * time.Second)

	// Feature 023: ticker de geolocalización (baja frecuencia, con piso de seguridad).
	//
	// El ticker **nunca se detiene** y el interruptor se **re-evalúa en cada tick**.
	// Antes se hacía `Stop()` cuando el interruptor venía apagado, y eso convertía un
	// fallo transitorio en una avería permanente y silenciosa: si el agente arranca sin
	// DNS —un portátil recién encendido, o antes de que suba la VPN— el repliegue de
	// `GetCheckInConfFromAPI` devuelve `GeoEnabled=false`, el ticker quedaba detenido
	// para toda la vida del proceso, y la geolocalización no volvía hasta que alguien
	// reiniciaba el agente. Medido en un Mac el 2026-07-27: 2 h 30 min sin un solo
	// punto, con `geo_tracking_enabled=True` en el servidor todo el tiempo.
	//
	// Y arregla la dirección que importa más: hasta ahora **apagar** la geo en la
	// consola tampoco surtía efecto hasta que el agente reiniciara. Para un
	// interruptor de privacidad, ése era el lado grave.
	//
	// (TODO: disparo adicional al detectar cambio de red — diferido; el ticker cubre
	// la captura periódica garantizada.)
	// Feature 030 · el ticker nace con la cadencia que corresponde al estado, no
	// con la normal. Antes se creaba con `geoTickInterval(conf.Geo)` y la cadencia
	// del caso recién bajaba en el primer tick: medido en terreno, 5 min 12 s a
	// cadencia normal después de un `kill -9` del servicio (y 25 a 35 min con el
	// `checkin_geo` por omisión del producto). Justo lo que la persistencia del
	// estado venía a evitar: un equipo robado se reinicia, y el reinicio no puede
	// costar una ventana ciega en cada arranque.
	perdidoAlArrancar, intervaloAlArrancar := a.getLostMode()
	geoInterval := geoTickIntervalFor(perdidoAlArrancar, intervaloAlArrancar, conf.Geo)
	a.Logger.Debugf("geo: ticker inicial %ds (modo perdido: %v)", geoInterval, perdidoAlArrancar)
	checkInGeoTicker := time.NewTicker(time.Duration(geoInterval) * time.Second)

	// Guard atómico: si un SyncMeshNodeID del tick anterior sigue corriendo,
	// saltamos este tick en vez de solaparlos (backport v2.11.0 "fix ... race").
	var syncMeshRunning atomic.Bool

	// Lo mismo para la captura de ubicación (feature 030). Con el piso de 300 s
	// dos capturas no llegaban a solaparse; con la cadencia del modo perdido sí.
	var geoRunning atomic.Bool

	// lanzarCapturaGeo saca la captura del hilo del `select`. Antes corría en
	// línea, y en macOS una sola puede costar hasta ~40 s (CoreLocation 20 s + la
	// consulta de config + la degradación a IP 10 s). Con el piso de 300 s eso se
	// toleraba; con la cadencia del modo perdido pasa a ser el estado normal, y
	// bloquear el `select` atrasa `agent-hello` (30-60 s) hasta hacer que el equipo
	// aparezca OFFLINE en la consola justo cuando más importa.
	//
	// Está en una función porque ahora hay DOS disparadores —el tick y el aviso del
	// push— y duplicar el guard invitaba a que uno de los dos se olvidara de tomarlo.
	//
	// `GeoForceOn` se escribe sólo cuando se acaba de tomar la guardia, desde esta
	// misma goroutine; el `Store(false)` diferido establece la relación de orden con
	// la captura siguiente, así que no hay carrera con la que lo lee.
	lanzarCapturaGeo := func(forceOn bool) {
		if geoRunning.CompareAndSwap(false, true) {
			a.GeoForceOn = forceOn
			go func() {
				defer geoRunning.Store(false)
				a.NatsMessage(nc, "agent-geolocation")
			}()
			return
		}
		// Con cadencias cortas una captura lenta puede pisar a la siguiente. Se
		// salta el turno en vez de apilarlas.
		a.Logger.Debugln("geo: la captura anterior sigue corriendo, se salta este turno")
	}

	// Última cadencia normal conocida, para poder volver a ella cuando el modo
	// perdido se apaga por el push. La del arranque sirve de punto de partida y
	// cada tick la refresca con lo que diga el servidor.
	geoConfSeg := conf.Geo

	for {
		select {
		case <-checkInHelloTicker.C:
			a.NatsMessage(nc, "agent-hello")
		case <-checkInAgentInfoTicker.C:
			a.NatsMessage(nc, "agent-agentinfo")
		case <-checkInWinSvcTicker.C:
			a.NatsMessage(nc, "agent-winsvc")
		case <-checkInPubIPTicker.C:
			a.NatsMessage(nc, "agent-publicip")
		case <-checkInDisksTicker.C:
			a.NatsMessage(nc, "agent-disks")
		case <-checkInSWTicker.C:
			a.SendSoftware()
		case <-checkInWMITicker.C:
			a.NatsMessage(nc, "agent-wmi")
		case <-checkInGeoTicker.C:
			// Una consulta de config por tick. El tick tiene piso de 300 s, así que
			// esto es mucho menos tráfico que el ticker de `agent-hello` (30–60 s);
			// va por HTTP y no por NATS, eso sí, o sea que toca Django. Si a escala de
			// flota pesa, la perilla es subir `checkin_geo`: baja las capturas y las
			// consultas juntas.
			geoConf := a.GetAgentCheckInConfig(a.GetCheckInConfFromAPI())

			// Feature 030 · canal de respaldo del push por NATS. Es el ÚNICO
			// camino garantizado: si el equipo estaba apagado cuando lo marcaron
			// —el escenario central de la feature— el push se perdió y esta es la
			// forma de enterarse al reconectar. Y en el otro sentido: si lo
			// recuperaron mientras estaba sin red, acá se apaga.
			//
			// Sólo se aplica cuando la config vino DE VERDAD del servidor. Con el
			// repliegue (sin red, servidor caído) todos los campos son ceros, y
			// leer ese `lost_mode=false` como "ya no está perdido" apagaría la
			// captura justo cuando el equipo perdió contacto.
			if geoConf.fromServer {
				a.SetLostMode(geoConf.LostMode, geoConf.LostModeIntervalMin)
			}

			perdido, perdidoIntervaloMin := a.getLostMode()
			geoConfSeg = geoConf.Geo

			// Si el intervalo vigente cambió, se re-arma. Cubre el caso del arranque
			// sin red, que dejaba puesto el intervalo del repliegue en vez del real,
			// y ahora también las dos transiciones del modo perdido: el ticker nunca
			// se detiene, así que entrar y salir del modo no necesita más código.
			if nuevo := geoTickIntervalFor(perdido, perdidoIntervaloMin, geoConf.Geo); nuevo != geoInterval {
				a.Logger.Debugf("geo: intervalo %ds -> %ds (modo perdido: %v)", geoInterval, nuevo, perdido)
				geoInterval = nuevo
				checkInGeoTicker.Reset(time.Duration(geoInterval) * time.Second)
			}

			// Feature 030 · el modo perdido IGNORA el interruptor global.
			//
			// Sin este bypass la feature sería un no-op en cualquier flota con la
			// geo apagada por omisión, que es la instalación por defecto
			// (ADR-024) — o sea, justo donde hace falta. Lo que lo justifica es el
			// régimen de ADR-025: motivo obligatorio, permiso dedicado y una
			// entrada de auditoría que deja escrito que el marcaje pisa esta
			// perilla. No es una excepción silenciosa.
			if !perdido && !geoConf.GeoEnabled {
				// Se registra: el silencio sin explicación era justamente el problema.
				a.Logger.Debugln("geo: interruptor apagado en este tick, no se captura")
				continue
			}

			// Gap 3: el force-on también se re-evalúa, porque con el repliegue llegaba
			// siempre apagado.
			//
			// En modo perdido va SIEMPRE encendido, y no es un extra: en Windows 11
			// 24H2 / Server 2025 el escaneo de BSSIDs exige la ubicación del sistema
			// activada, así que con el force-on apagado la geo intensiva devolvería
			// `denied` o degradaría a IP —nivel ciudad— y el bypass de arriba no
			// serviría de nada. Encender el interruptor sin encender el sensor sería
			// otro "ok falso".
			//
			// Se escribe desde ESTA goroutine y sólo cuando se acaba de tomar la
			// guardia; el `Store(false)` diferido establece la relación de orden con
			// el tick siguiente, así que no hay carrera con la captura que lo lee.
			lanzarCapturaGeo(geoConf.GeoForceOn || perdido)

		// Feature 030 · el estado del modo perdido cambió por el push de NATS.
		//
		// Sin este case, el push fijaba el estado al instante y la cadencia del caso
		// recién empezaba en el tick normal siguiente: 3 min 20 s medidos en terreno
		// el 2026-08-11, y 25 a 35 min con el `checkin_geo` por omisión del producto,
		// con el equipo encendido y en línea. El estado llegaba a tiempo y no se
		// aplicaba donde hacía falta — el mismo error que ya se corrigió en el
		// arranque y en el reinicio.
		case <-lostModeChanged:
			perdido, perdidoIntervaloMin := a.getLostMode()

			if nuevo := geoTickIntervalFor(perdido, perdidoIntervaloMin, geoConfSeg); nuevo != geoInterval {
				a.Logger.Debugf("geo: intervalo %ds -> %ds por aviso de modo perdido (%v)", geoInterval, nuevo, perdido)
				geoInterval = nuevo
				checkInGeoTicker.Reset(time.Duration(geoInterval) * time.Second)
			}

			// Al ENTRAR se captura de inmediato, sin esperar el primer tick de la
			// cadencia nueva. Con una cadencia de 60 min, esperarlo sería una hora
			// sin un solo punto justo después de que alguien denunció el robo; y el
			// primer punto —dónde estaba el equipo al momento del marcaje— es el más
			// valioso del caso. Al SALIR no se captura: recuperar es dejar de mirar.
			//
			// El force-on va encendido por la misma razón que en el tick: en Windows
			// 11 24H2 el escaneo de BSSIDs exige la ubicación del sistema activada.
			if perdido {
				lanzarCapturaGeo(true)
			}
		case <-syncMeshTicker.C:
			if syncMeshRunning.CompareAndSwap(false, true) {
				go func() {
					defer syncMeshRunning.Store(false)
					a.SyncMeshNodeID()
				}()
			} else {
				a.Logger.Debugln("syncMeshTicker SyncMeshNodeID already running, skipping tick")
			}
		}
	}
}

func (a *Agent) AgentStartup() {
	url := "/api/v3/checkin/"
	payload := map[string]interface{}{"agent_id": a.AgentID}
	_, err := a.rClient.R().SetBody(payload).Post(url)
	if err != nil {
		a.Logger.Debugln("AgentStartup()", err)
	}
}

// checkInConfFallback son los valores que usa el agente cuando NO pudo leer la
// config del servidor. Va en su propia función para poder congelar en tests lo que
// el repliegue promete: ahí vivía un bug silencioso, porque `GeoEnabled=false`
// combinado con un ticker que se detenía para siempre dejaba la geolocalización
// muerta hasta el próximo reinicio del agente.
//
// Los intervalos van aleatorizados a propósito: reparten en el tiempo a una flota
// que se reconecta junta (un corte de red, un arranque masivo).
func checkInConfFallback() AgentCheckInConfig {
	return AgentCheckInConfig{
		Hello:     randRange(30, 60),
		AgentInfo: randRange(200, 400),
		WinSvc:    randRange(2400, 3000),
		PubIP:     randRange(300, 500),
		Disks:     randRange(1000, 2000),
		SW:        randRange(2800, 3500),
		WMI:       randRange(3000, 4000),
		SyncMesh:  randRange(800, 1200),

		// La geo se apaga en el repliegue, y eso está bien: es una feature sensible
		// y sin respuesta del servidor no se sabe si el operador la quiere encendida.
		// Lo que cambió es el intervalo, que antes era 1500–2100 s: ahora que el
		// interruptor se re-evalúa en cada tick, este número dejó de significar "cada
		// cuánto capturo estando roto" y pasó a significar "en cuánto me doy cuenta de
		// la config real". Un valor largo hoy es estrictamente peor.
		GeoEnabled: false,
		Geo:        randRange(geoMinInterval, geoMinInterval*2),
		GeoForceOn: false,

		// El modo perdido NO se apaga en el repliegue: a diferencia de la geo,
		// su estado vive en el archivo local y no depende de esta llamada. Un
		// `false` acá se lee como "el servidor dice que ya no está perdido", y
		// sin respuesta del servidor eso es exactamente lo que no se sabe. Por
		// eso el consumidor (svc.go) sólo aplica este campo cuando la config
		// vino DE VERDAD del servidor.
		LostMode:            false,
		LostModeIntervalMin: 0,
	}
}

// geoTickInterval aplica el piso de seguridad al intervalo de geolocalización: pase
// lo que pase por config, la captura nunca es de alta frecuencia.
func geoTickInterval(fromConf int) int {
	if fromConf < geoMinInterval {
		return geoMinInterval
	}
	return fromConf
}

// geoTickIntervalFor resuelve el intervalo EFECTIVO del ticker de geo.
//
// El piso de 300 s es una salvaguarda de privacidad para la operación normal: sin
// él, un `checkin_geo` bajo —puesto a mano o por un tipeo en el servidor—
// convertiría la flota en un rastreador. El modo perdido lo saltea a propósito,
// porque un equipo robado se mueve y un punto cada cinco minutos no reconstruye
// un recorrido. El límite ahí lo pone el clamp de 1-60 min, que se aplica en el
// servidor y otra vez en el agente (ver lostmodestate.go).
//
// Va en su propia función para poder congelar en tests que el piso protege lo que
// tiene que proteger y no lo que no.
func geoTickIntervalFor(perdido bool, perdidoIntervaloMin, fromConf int) int {
	if perdido {
		return clampLostModeInterval(perdidoIntervaloMin) * 60
	}
	return geoTickInterval(fromConf)
}

func (a *Agent) GetCheckInConfFromAPI() AgentCheckInConfig {
	ret := AgentCheckInConfig{}
	url := fmt.Sprintf("/api/v3/%s/config/", a.AgentID)
	r, err := a.rClient.R().SetResult(&AgentCheckInConfig{}).Get(url)
	if err != nil {
		a.Logger.Debugln("GetAgentCheckInConfig()", err)
		ret = checkInConfFallback()
	} else {
		ret.Hello = r.Result().(*AgentCheckInConfig).Hello
		ret.AgentInfo = r.Result().(*AgentCheckInConfig).AgentInfo
		ret.WinSvc = r.Result().(*AgentCheckInConfig).WinSvc
		ret.PubIP = r.Result().(*AgentCheckInConfig).PubIP
		ret.Disks = r.Result().(*AgentCheckInConfig).Disks
		ret.SW = r.Result().(*AgentCheckInConfig).SW
		ret.WMI = r.Result().(*AgentCheckInConfig).WMI
		ret.SyncMesh = r.Result().(*AgentCheckInConfig).SyncMesh
		ret.LimitData = r.Result().(*AgentCheckInConfig).LimitData
		ret.InstallNushell = r.Result().(*AgentCheckInConfig).InstallNushell
		ret.InstallNushellVersion = r.Result().(*AgentCheckInConfig).InstallNushellVersion
		ret.InstallNushellUrl = r.Result().(*AgentCheckInConfig).InstallNushellUrl
		ret.NushellEnableConfig = r.Result().(*AgentCheckInConfig).NushellEnableConfig
		ret.InstallDeno = r.Result().(*AgentCheckInConfig).InstallDeno
		ret.InstallDenoVersion = r.Result().(*AgentCheckInConfig).InstallDenoVersion
		ret.InstallDenoUrl = r.Result().(*AgentCheckInConfig).InstallDenoUrl
		ret.DenoDefaultPermissions = r.Result().(*AgentCheckInConfig).DenoDefaultPermissions
		ret.GeoEnabled = r.Result().(*AgentCheckInConfig).GeoEnabled
		ret.Geo = r.Result().(*AgentCheckInConfig).Geo
		ret.GeoForceOn = r.Result().(*AgentCheckInConfig).GeoForceOn
		ret.LostMode = r.Result().(*AgentCheckInConfig).LostMode
		ret.LostModeIntervalMin = r.Result().(*AgentCheckInConfig).LostModeIntervalMin
		ret.fromServer = true
	}
	return ret
}
