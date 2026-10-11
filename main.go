package main

import (
	"flag"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"

	"github.com/braincorp-cl/observer-agent-dist/agent"
	"github.com/kardianos/service"
	"github.com/sirupsen/logrus"
)

var (
	version = "2.10.7"
	log     = logrus.New()
	logFile *os.File
)

func main() {
	ver := flag.Bool("version", false, "Prints version")
	mode := flag.String("m", "", "The mode to run")
	taskPK := flag.Int("p", 0, "Task PK")
	logLevel := flag.String("log", "INFO", "The log level")
	logTo := flag.String("logto", "file", "Where to log to")
	api := flag.String("api", "", "API URL")
	clientID := flag.Int("client-id", 0, "Client ID")
	siteID := flag.Int("site-id", 0, "Site ID")
	timeout := flag.Duration("timeout", 900, "Installer timeout (seconds)")
	desc := flag.String("desc", "", "Agent's Description")
	atype := flag.String("agent-type", "server", "server or workstation")
	token := flag.String("auth", "", "Token")
	power := flag.Bool("power", false, "Disable sleep/hibernate")
	rdp := flag.Bool("rdp", false, "Enable RDP")
	ping := flag.Bool("ping", false, "Enable ping")
	localMesh := flag.String("local-mesh", "", "Path to mesh executable")
	noMesh := flag.Bool("nomesh", false, "Do not install mesh agent")
	meshDir := flag.String("meshdir", "", "Path to custom meshcentral dir")
	tmpDir := flag.String("tmpdir", "", "Path to custom temp dir")
	meshNodeID := flag.String("meshnodeid", "", "Mesh Node ID")
	cert := flag.String("cert", "", "Path to domain CA .pem")
	silent := flag.Bool("silent", false, "Do not popup any message boxes during installation")
	proxy := flag.String("proxy", "", "Use a http proxy")
	insecure := flag.Bool("insecure", false, "Insecure for testing only")
	natsport := flag.String("natsport", "", "nats standard port")
	flag.Parse()

	if *ver {
		agent.ShowVersionInfo(version)
		return
	}

	if len(os.Args) == 1 {
		switch runtime.GOOS {
		case "windows":
			agent.ShowStatus(version)
		default:
			agent.ShowVersionInfo(version)
		}
		return
	}

	setupLogging(logLevel, logTo)
	defer logFile.Close()

	a := *agent.New(log, version)

	if *mode == "install" {
		a.Logger.SetOutput(os.Stdout)
	}

	a.Logger.Debugf("%+v\n", a)

	switch *mode {
	case "getenv":
		fmt.Println(os.Getenv(flag.Arg(0)))
	case "nixmeshnodeid":
		fmt.Print(a.NixMeshNodeID())
	case "installsvc":
		// El código de salida IMPORTA acá y no es decorativo. Inno Setup corre
		// esto en DeinitializeSetup y registra `install service: <ResultCode>`:
		// si el error se descarta, una máquina reparada y una que sigue rota
		// producen exactamente el mismo log y el mismo 0, y el bucle de descarga
		// horaria continúa sin que nadie pueda saber en cuáles equipos surtió
		// efecto la actualización.
		if err := a.InstallService(); err != nil {
			a.Logger.Errorln("installsvc InstallService():", err)
			os.Exit(1)
		}
		if err := a.ReconcileService(); err != nil {
			a.Logger.Errorln("installsvc ReconcileService():", err)
			os.Exit(1)
		}
	case "checkin":
		a.DoNatsCheckIn()
	case "rpc":
		a.RunRPC()
	case "svc":
		if runtime.GOOS == "windows" {
			s, _ := service.New(&a, a.ServiceConfig)
			s.Run()
		} else {
			a.RunRPC()
		}
	case "pk":
		fmt.Println(a.AgentPK)
	case "winagentsvc":
		fmt.Println("deprecated. use 'svc'")
	case "runchecks":
		a.RunChecks(true)
	case "checkrunner":
		a.RunChecks(false)
	case "software":
		a.SendSoftware()
	case "cleanup":
		a.UninstallCleanup()
	case "publicip":
		fmt.Println(a.PublicIP())
	case "actividad-muestra":
		// Feature 049: muestra instantánea de actividad. Corre como el usuario
		// (self-exec). flag.Arg(0) == "titulo" pide además el título de la ventana.
		agent.ModoActividadMuestra(flag.Arg(0) == "titulo")
	case "geolocation":
		// Diagnóstico: resuelve y muestra la ubicación con la config vigente.
		a.RunGeolocation()
	case "geoprobe":
		// Feature 025 (macOS): el daemon se re-ejecuta con este modo en el
		// contexto del usuario de consola, porque CoreLocation no atiende a la
		// sesión 0. Imprime una sola línea (FIX/DENIED/NOFIX) que el padre parsea.
		a.RunGeoProbe()
	case "screencapture":
		// Feature 030 (Windows): el servicio se re-ejecuta con este modo DENTRO
		// de la sesión del usuario, porque un BitBlt hecho desde la sesión 0 no
		// falla — devuelve una pantalla negra. Imprime una sola línea
		// (OK <ruta> / FAIL <motivo>) que el padre parsea. El nombre del archivo
		// llega como argumento posicional, igual que en `-m getenv`.
		a.RunScreenCapture(flag.Arg(0))
	case "webcamcapture":
		// Feature 030 (Windows): NO lo usa el ciclo del modo perdido, que saca la
		// foto en el propio servicio. Existe para poder medir la cámara de un
		// equipo real sin marcarlo como perdido — marcarlo subiría además
		// capturas de su pantalla y su ubicación. Imprime una sola línea
		// (OK <archivo> <ruta> / FAIL <motivo>), igual que `-m screencapture`.
		a.RunWebcamCapture(flag.Arg(0))
	case "portalscreenshot":
		// Feature 030 (Linux/Wayland): el servicio se re-ejecuta con este modo
		// DENTRO de la sesión del usuario, porque el bus de sesión no acepta a
		// root y el portal contesta con una señal dirigida al cliente que pidió
		// —hay que pedir y escuchar en la MISMA conexión—. Imprime una sola línea
		// (OK <ruta> / FAIL <motivo>) que el padre parsea. Ver agent/portalshot_linux.go.
		a.RunPortalScreenshot(flag.Arg(0))
	case "capturaaqua":
		// Feature 030 (macOS): el ayudante que SÍ toca la cámara y la pantalla.
		// Lo despierta launchd desde dentro de su bundle y en la sesión Aqua, que
		// son las dos condiciones que le dan identidad ante TCC y contexto
		// gráfico — desde el servicio, con el permiso concedido, la cámara
		// devuelve cero cuadros y ningún error. No se invoca a mano: lee el
		// pedido que dejó el servicio y le contesta por el mismo buzón.
		// Ver agent/capturaaqua.go.
		a.RunCapturaAqua()
	case "tccpermisos":
		// Feature 030 (macOS): fuerza una ronda de permisos TCC. Es la vía
		// manual —desde la biblioteca de scripts o un rawcmd— para cuando hay
		// una persona frente al equipo y hay que reponer lo que se llevó un
		// auto-update. El servicio la corre solo en su arranque; esto es para
		// no tener que esperarlo.
		a.RondaPermisosTCC(true)
	case "portalwayland":
		// Feature 030 (Linux/Wayland): fuerza una ronda de enrolamiento del portal.
		// Es la vía manual —desde la biblioteca de scripts o un rawcmd— para cuando
		// hay una persona frente al equipo y hay que reponer el permiso. El servicio
		// la corre solo en su arranque; esto es para no tener que esperarlo. Mismo
		// patrón y misma razón que `-m tccpermisos`.
		a.RondaPortalWayland(true)
	case "getpython":
		a.GetPython(true)
	case "installdeno":
		a.InstallDeno(true)
	case "installnushell":
		a.InstallNushell(true)
	case "runmigrations":
		a.RunMigrations()
	case "recovermesh":
		a.RecoverMesh()
	case "macventurafix":
		a.FixVenturaMesh()
	case "taskrunner":
		if len(os.Args) < 5 || *taskPK == 0 {
			return
		}
		a.RunTask(*taskPK, true)
	case "install":
		if runtime.GOOS != "windows" {
			u, err := user.Current()
			if err != nil {
				log.Fatalln(err)
			}
			if u.Uid != "0" {
				log.Fatalln("must run as root")
			}
		}

		if *api == "" || *clientID == 0 || *siteID == 0 || *token == "" {
			return
		}
		a.Install(&agent.Installer{
			RMM:              *api,
			ClientID:         *clientID,
			SiteID:           *siteID,
			Description:      *desc,
			AgentType:        *atype,
			Power:            *power,
			RDP:              *rdp,
			Ping:             *ping,
			Token:            *token,
			LocalMesh:        *localMesh,
			Cert:             *cert,
			Proxy:            *proxy,
			Timeout:          *timeout,
			Silent:           *silent,
			NoMesh:           *noMesh,
			MeshDir:          *meshDir,
			MeshNodeID:       *meshNodeID,
			Insecure:         *insecure,
			NatsStandardPort: *natsport,
			TmpDir:           *tmpDir,
		})
	default:
		agent.ShowStatus(version)
	}
}

func setupLogging(level, to *string) {
	ll, err := logrus.ParseLevel(*level)
	if err != nil {
		ll = logrus.InfoLevel
	}
	log.SetLevel(ll)

	if *to == "stdout" {
		log.SetOutput(os.Stdout)
	} else {
		switch runtime.GOOS {
		case "windows":
			// La ruta sale de la ubicación instalada, no de %ProgramFiles%: en un
			// agente de 32 bits sobre x64 esa variable apunta a otro directorio y
			// el log terminaba lejos del resto de la instalación.
			logFile, _ = os.OpenFile(filepath.Join(agent.ResolveProgramDir(), "agent.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0664)
		default:
			logFile, _ = os.OpenFile(filepath.Join("/var/log/", "observeragent.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0664)
		}
		log.SetOutput(logFile)
	}
}
