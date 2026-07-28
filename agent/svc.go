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
	geoInterval := geoTickInterval(conf.Geo)
	checkInGeoTicker := time.NewTicker(time.Duration(geoInterval) * time.Second)

	// Guard atómico: si un SyncMeshNodeID del tick anterior sigue corriendo,
	// saltamos este tick en vez de solaparlos (backport v2.11.0 "fix ... race").
	var syncMeshRunning atomic.Bool

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

			// Si el intervalo vigente cambió, se re-arma. Cubre el caso del arranque
			// sin red, que dejaba puesto el intervalo del repliegue en vez del real.
			if nuevo := geoTickInterval(geoConf.Geo); nuevo != geoInterval {
				a.Logger.Debugf("geo: intervalo %ds -> %ds", geoInterval, nuevo)
				geoInterval = nuevo
				checkInGeoTicker.Reset(time.Duration(geoInterval) * time.Second)
			}

			if !geoConf.GeoEnabled {
				// Se registra: el silencio sin explicación era justamente el problema.
				a.Logger.Debugln("geo: interruptor apagado en este tick, no se captura")
				continue
			}

			// Gap 3: el force-on también se re-evalúa, porque con el repliegue llegaba
			// siempre apagado. Se escribe desde esta misma goroutine, inmediatamente
			// antes de la captura que lo lee, así que no hay carrera con ella.
			a.GeoForceOn = geoConf.GeoForceOn
			a.NatsMessage(nc, "agent-geolocation")
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
	}
	return ret
}
