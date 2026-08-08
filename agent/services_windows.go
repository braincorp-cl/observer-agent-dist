package agent

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	rmm "github.com/braincorp-cl/observer-agent-dist/shared"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func GetServiceStatus(name string) (string, error) {
	conn, err := mgr.Connect()
	if err != nil {
		return "n/a", err
	}
	defer conn.Disconnect()

	srv, err := conn.OpenService(name)
	if err != nil {
		return "n/a", err
	}
	defer srv.Close()

	q, err := srv.Query()
	if err != nil {
		return "n/a", err
	}

	return serviceStatusText(uint32(q.State)), nil
}

func (a *Agent) ControlService(name, action string) rmm.WinSvcResp {
	conn, err := mgr.Connect()
	if err != nil {
		return rmm.WinSvcResp{Success: false, ErrorMsg: err.Error()}
	}
	defer conn.Disconnect()

	srv, err := conn.OpenService(name)
	if err != nil {
		return rmm.WinSvcResp{Success: false, ErrorMsg: err.Error()}
	}
	defer srv.Close()

	var status svc.Status
	switch action {

	case "stop":
		status, err = srv.Control(svc.Stop)
		if err != nil {
			return rmm.WinSvcResp{Success: false, ErrorMsg: err.Error()}
		}
		timeout := time.Now().Add(30 * time.Second)
		for status.State != svc.Stopped {
			if timeout.Before(time.Now()) {
				return rmm.WinSvcResp{Success: false, ErrorMsg: "Timed out waiting for service to stop"}
			}
			time.Sleep(500 * time.Millisecond)
			status, err = srv.Query()
			if err != nil {
				return rmm.WinSvcResp{Success: false, ErrorMsg: err.Error()}
			}
		}
		return rmm.WinSvcResp{Success: true, ErrorMsg: ""}

	case "start":
		err := srv.Start()
		if err != nil {
			return rmm.WinSvcResp{Success: false, ErrorMsg: err.Error()}
		}
		return rmm.WinSvcResp{Success: true, ErrorMsg: ""}
	}

	return rmm.WinSvcResp{Success: false, ErrorMsg: "Something went wrong"}
}

func (a *Agent) EditService(name, startupType string) rmm.WinSvcResp {
	conn, err := mgr.Connect()
	if err != nil {
		return rmm.WinSvcResp{Success: false, ErrorMsg: err.Error()}
	}
	defer conn.Disconnect()

	srv, err := conn.OpenService(name)
	if err != nil {
		return rmm.WinSvcResp{Success: false, ErrorMsg: err.Error()}
	}
	defer srv.Close()

	conf, err := srv.Config()
	if err != nil {
		return rmm.WinSvcResp{Success: false, ErrorMsg: err.Error()}
	}

	var startType uint32
	switch startupType {
	case "auto":
		startType = 2
	case "autodelay":
		startType = 2
	case "manual":
		startType = 3
	case "disabled":
		startType = 4
	default:
		return rmm.WinSvcResp{Success: false, ErrorMsg: "Unknown startup type provided"}
	}

	conf.StartType = startType
	switch startupType {
	case "autodelay":
		conf.DelayedAutoStart = true
	case "auto":
		conf.DelayedAutoStart = false
	}

	err = srv.UpdateConfig(conf)
	if err != nil {
		return rmm.WinSvcResp{Success: false, ErrorMsg: err.Error()}
	}
	return rmm.WinSvcResp{Success: true, ErrorMsg: ""}
}

func (a *Agent) GetServiceDetail(name string) rmm.WindowsService {
	ret := rmm.WindowsService{}

	conn, err := mgr.Connect()
	if err != nil {
		a.Logger.Errorln(err)
		return ret
	}
	defer conn.Disconnect()

	srv, err := conn.OpenService(name)
	if err != nil {
		a.Logger.Errorln(err)
		return ret
	}
	defer srv.Close()

	q, err := srv.Query()
	if err != nil {
		a.Logger.Errorln(err)
		return ret
	}

	conf, err := srv.Config()
	if err != nil {
		a.Logger.Errorln(err)
		return ret
	}

	ret.BinPath = CleanString(conf.BinaryPathName)
	ret.Description = CleanString(conf.Description)
	ret.DisplayName = CleanString(conf.DisplayName)
	ret.Name = name
	ret.PID = q.ProcessId
	ret.StartType = serviceStartType(uint32(conf.StartType))
	ret.Status = serviceStatusText(uint32(q.State))
	ret.Username = CleanString(conf.ServiceStartName)
	ret.DelayedAutoStart = conf.DelayedAutoStart
	return ret
}

// GetServices returns a list of windows services
func (a *Agent) GetServices() []rmm.WindowsService {
	ret := make([]rmm.WindowsService, 0)

	conn, err := mgr.Connect()
	if err != nil {
		a.Logger.Debugln(err)
		return ret
	}
	defer conn.Disconnect()

	svcs, err := conn.ListServices()

	if err != nil {
		a.Logger.Debugln(err)
		return ret
	}

	for _, s := range svcs {
		srv, err := conn.OpenService(s)
		if err != nil {
			if err.Error() != "Access is denied." {
				a.Logger.Debugln("Open Service", s, err)
			}
			continue
		}
		defer srv.Close()

		q, err := srv.Query()
		if err != nil {
			a.Logger.Debugln(err)
			continue
		}

		conf, err := srv.Config()
		if err != nil {
			a.Logger.Debugln(err)
			continue
		}

		ret = append(ret, rmm.WindowsService{
			Name:             s,
			Status:           serviceStatusText(uint32(q.State)),
			DisplayName:      CleanString(conf.DisplayName),
			BinPath:          CleanString(conf.BinaryPathName),
			Description:      CleanString(conf.Description),
			Username:         CleanString(conf.ServiceStartName),
			PID:              q.ProcessId,
			StartType:        serviceStartType(uint32(conf.StartType)),
			DelayedAutoStart: conf.DelayedAutoStart,
		})
	}
	return ret
}

// WaitForService will wait for a service to be in X state for X retries
func WaitForService(name string, status string, retries int) {
	attempts := 0
	for {
		stat, err := GetServiceStatus(name)
		if err != nil {
			attempts++
			time.Sleep(5 * time.Second)
		} else {
			if stat != status {
				attempts++
				time.Sleep(5 * time.Second)
			} else {
				attempts = 0
			}
		}
		if attempts == 0 || attempts >= retries {
			break
		}
	}
}

// ReconcileService reapunta el servicio al binario que el agente tiene por suyo,
// cuando el registrado es otro.
//
// Es la reparación de los equipos que ya quedaron con las dos rutas divergidas:
// el servicio ejecutando `C:\Program Files (x86)\ObserverAgent` mientras el
// instalador escribe en `C:\Program Files\ObserverAgent`. Corre dentro de la
// actualización normal, así que esos equipos se sanan con un solo salto de
// versión y sin reinstalar a mano.
//
// En una instalación sana no hace nada: las dos rutas coinciden y retorna sin
// tocar el servicio.
//
// NO BORRA NADA, y eso es deliberado. La versión anterior eliminaba acá mismo el
// directorio que quedaba huérfano, en el peor momento posible: a mitad de una
// actualización desatendida, con el servicio recién matado a taskkill y sin
// nadie mirando. Cuatro modos de falla salían de esa sola decisión —entre ellos
// dejar sin CWD a todas las tareas programadas del equipo, que `tasks_windows.go`
// grabó apuntando al `ProgramDir` viejo—. El directorio se elimina al
// DESINSTALAR, en `UninstallCleanup`, donde ya nadie lo necesita.
func (a *Agent) ReconcileService() error {
	if !serviceExists(winSvcName) {
		return nil
	}

	conn, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	srv, err := conn.OpenService(winSvcName)
	if err != nil {
		return err
	}
	defer srv.Close()

	conf, err := srv.Config()
	if err != nil {
		return err
	}

	oldExe := resolveServiceExe(conf.BinaryPathName)
	if oldExe == "" {
		a.Logger.Errorln("No se pudo determinar el ejecutable del servicio, no se reapunta:", conf.BinaryPathName)
		return nil
	}
	if strings.EqualFold(filepath.Clean(oldExe), filepath.Clean(a.EXE)) {
		return nil
	}

	// El destino tiene que existir ANTES de reapuntar. Si no, esta función
	// dejaría el servicio mirando a un binario inexistente: la máquina quedaría
	// sin agente y sin forma de volver desde la consola.
	//
	// Pasa cuando `-m installsvc` corre desde una carpeta cualquiera en un equipo
	// instalado con /DIR: ahí no hay `unins*.exe` que reconocer ni `InstallDir`
	// que leer, y ResolveProgramDir cae al valor por defecto, que no es el suyo.
	if !rmm.FileExists(a.EXE) {
		a.Logger.Errorln("No se reapunta el servicio: el binario de destino no existe:", a.EXE)
		return nil
	}

	a.Logger.Infof("El servicio apunta a %s pero el agente está instalado en %s; reapuntando.", oldExe, a.EXE)

	conf.BinaryPathName = replaceServiceExePath(conf.BinaryPathName, oldExe, a.EXE)
	if err := srv.UpdateConfig(conf); err != nil {
		return err
	}

	a.rescueMeshInstaller(filepath.Dir(oldExe))
	return nil
}

// rescueMeshInstaller copia `meshagent.exe` desde el directorio viejo al nuevo.
//
// Es lo único del directorio viejo que hace falta traer: en un equipo divergente
// esa es la ÚNICA copia de la máquina —`install.go` la descargó al ProgramDir de
// entonces, y el `[Files]` del instalador sólo trae el binario del agente—, y de
// ella depende que el `[UninstallRun] {app}\meshagent.exe -fulluninstall` corte
// el acceso remoto al desinstalar. Python, nushell y deno se redescargan solos
// cuando el agente los necesita.
//
// Copia y no mueve, a propósito: si la copia falla, el original sigue donde
// estaba y la recuperación manual sigue siendo posible.
func (a *Agent) rescueMeshInstaller(oldDir string) {
	if oldDir == "" || sameDir(oldDir, a.ProgramDir) {
		return
	}

	src := filepath.Join(oldDir, a.MeshInstaller)
	dst := filepath.Join(a.ProgramDir, a.MeshInstaller)
	if !rmm.FileExists(src) || rmm.FileExists(dst) {
		return
	}

	if err := copyFile(src, dst); err != nil {
		a.Logger.Errorln("rescueMeshInstaller():", err)
		return
	}
	a.Logger.Infoln("Instalador de mesh rescatado del directorio viejo:", src)
}

// removeLegacyProgramDir borra el directorio que dejó la divergencia 386 × x64.
//
// Corre SÓLO al desinstalar. Ahí ya nadie lo necesita: el servicio se está
// yendo, `CleanupSchedTasks` borra las tareas que lo tenían como WorkingDir y el
// mesh se desinstala por su cuenta. Durante una actualización, en cambio,
// borrarlo rompía cosas vivas.
//
// La ruta es CALCULADA, no leída de la configuración de un servicio: es
// exactamente el `%ProgramFiles%\ObserverAgent` que el código viejo usaba como
// ProgramDir, que bajo WOW64 es `C:\Program Files (x86)\ObserverAgent`. En amd64
// y en Windows de 32 bits genuino coincide con el directorio vivo y no se toca.
func (a *Agent) removeLegacyProgramDir() {
	legacy := defaultProgramDir()
	if sameDir(legacy, a.ProgramDir) || !rmm.FileExists(legacy) {
		return
	}
	if !staleDirNameLooksRight(legacy) {
		return
	}

	a.Logger.Infoln("Borrando el directorio de instalación que dejó la divergencia 386 × x64:", legacy)
	if err := os.RemoveAll(legacy); err != nil {
		a.Logger.Errorln("removeLegacyProgramDir():", err)
	}
}

// resolveServiceExe averigua cuál es el ejecutable de un BinaryPathName,
// resolviendo contra el disco la ambigüedad que la cadena no resuelve.
//
// Hace lo mismo que el propio SCM: prueba las candidatas de la más corta a la
// más larga y se queda con la primera que existe. Importa porque un
// `sc config observeragent binpath= C:\Program Files\...\observeragent.exe -m svc`
// sin comillas —el remedio que aplicaría a mano un administrador que vio el
// defecto— dejaba al equipo permanentemente fuera del alcance de la reparación:
// la condición no cambia sola y no hay ninguna otra ruta en el producto que
// vuelva a ejercitar el update.
func resolveServiceExe(binPath string) string {
	for _, cand := range serviceExeCandidates(binPath) {
		if rmm.FileExists(cand) {
			return cand
		}
	}

	// Ninguna existe: el binario registrado ya no está. Queda el parseo
	// sintáctico, que acierta salvo con rutas con espacios y sin comillas.
	if exe, ok := parseServiceExePath(binPath); ok {
		return exe
	}
	return ""
}

func serviceExists(name string) bool {
	conn, err := mgr.Connect()
	if err != nil {
		return false
	}
	defer conn.Disconnect()

	srv, err := conn.OpenService(name)
	if err != nil {
		return false
	}
	defer srv.Close()

	return true
}

// https://docs.microsoft.com/en-us/dotnet/api/system.serviceprocess.servicecontrollerstatus?view=dotnet-plat-ext-3.1
func serviceStatusText(num uint32) string {
	switch num {
	case 1:
		return "stopped"
	case 2:
		return "start_pending"
	case 3:
		return "stop_pending"
	case 4:
		return "running"
	case 5:
		return "continue_pending"
	case 6:
		return "pause_pending"
	case 7:
		return "paused"
	default:
		return "unknown"
	}
}

// https://docs.microsoft.com/en-us/dotnet/api/system.serviceprocess.servicestartmode?view=dotnet-plat-ext-3.1
func serviceStartType(num uint32) string {
	switch num {
	case 0:
		return "Boot"
	case 1:
		return "System"
	case 2:
		return "Automatic"
	case 3:
		return "Manual"
	case 4:
		return "Disabled"
	default:
		return "Unknown"
	}
}
