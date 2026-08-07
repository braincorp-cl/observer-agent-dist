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

// reconcileServiceExe reapunta el servicio al binario que el agente tiene por
// suyo, cuando el registrado es otro.
//
// Es la reparación de los equipos que ya quedaron con las dos rutas divergidas:
// el servicio ejecutando `C:\Program Files (x86)\ObserverAgent` mientras el
// instalador escribe en `C:\Program Files\ObserverAgent`. Corre dentro de la
// actualización normal, con el servicio ya detenido por InitializeSetup, así que
// esos equipos se sanan con un solo salto de versión y sin reinstalar a mano.
//
// En una instalación sana no hace nada: las dos rutas coinciden y retorna sin
// tocar el servicio.
func (a *Agent) reconcileServiceExe() error {
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

	oldExe, unambiguous := parseServiceExePath(conf.BinaryPathName)
	if oldExe == "" || strings.EqualFold(filepath.Clean(oldExe), filepath.Clean(a.EXE)) {
		return nil
	}
	if !unambiguous {
		// Una ruta sin comillas y con espacios no se puede reescribir sin
		// riesgo de inventar un argumento. Se deja el servicio como está.
		a.Logger.Errorln("El BinaryPathName del servicio es ambiguo, no se reapunta:", conf.BinaryPathName)
		return nil
	}

	// El destino tiene que existir ANTES de reapuntar. Si no, esta función
	// dejaría el servicio mirando a un binario inexistente y —peor— borraría a
	// continuación el directorio donde sí estaba el agente: la máquina quedaría
	// sin agente, sin desinstalador y sin forma de volver desde la consola.
	//
	// Pasa cuando `-m installsvc` corre desde una carpeta cualquiera en un equipo
	// instalado con /DIR: ahí no hay `unins*.exe` que reconocer ni `InstallDir`
	// que leer, y ResolveProgramDir cae al valor por defecto, que no es el suyo.
	if !rmm.FileExists(a.EXE) {
		a.Logger.Errorln("No se reapunta el servicio: el binario de destino no existe:", a.EXE)
		return nil
	}

	a.Logger.Infof("El servicio apunta a %s pero el agente está instalado en %s; reapuntando.", oldExe, a.EXE)

	conf.BinaryPathName = replaceServiceExePath(conf.BinaryPathName, a.EXE)
	if err := srv.UpdateConfig(conf); err != nil {
		return err
	}

	a.cleanupStaleProgramDir(filepath.Dir(oldExe))
	return nil
}

// cleanupStaleProgramDir borra el directorio de instalación que quedó huérfano
// después de reapuntar el servicio.
//
// Antes de borrarlo rescata `meshagent.exe`: es lo único que no se vuelve a
// bajar solo. Python, nushell y deno los redescarga el agente cuando los
// necesita, y el desinstalador de Inno Setup no vive acá sino en `{app}`.
//
// Las guardas no son paranoia decorativa: esta función recibe una ruta derivada
// de la configuración de un servicio y termina en un RemoveAll.
func (a *Agent) cleanupStaleProgramDir(dir string) {
	if dir == "" || strings.EqualFold(filepath.Clean(dir), filepath.Clean(a.ProgramDir)) {
		return
	}
	if !staleDirNameLooksRight(dir) {
		a.Logger.Debugln("cleanupStaleProgramDir(): ruta inesperada, no se borra:", dir)
		return
	}

	// Comparar las cadenas no alcanza para saber si son directorios distintos.
	// `C:\PROGRA~1\ObserverAgent` y `C:\Program Files\ObserverAgent` son el MISMO
	// directorio escrito de dos formas, y un servicio puede haber quedado
	// registrado con la forma corta 8.3; lo mismo vale para una unión (junction)
	// o una unidad creada con subst. Sin esta comprobación, el RemoveAll se
	// llevaría la instalación viva que se acaba de dejar como buena.
	if sameDir(dir, a.ProgramDir) {
		a.Logger.Debugln("cleanupStaleProgramDir(): es el mismo directorio por otro nombre, no se borra:", dir)
		return
	}

	// Un directorio con `unins*.exe` es una instalación de Inno Setup con su
	// entrada viva en «Agregar o quitar programas». Borrarlo dejaría la entrada
	// apuntando a un desinstalador que ya no existe —el residuo en ARP que
	// justamente se persigue en amd64— y `AgentUninstall` fallaría en silencio
	// para siempre. Si aparece uno acá, hay que mirarlo, no borrarlo.
	if hasInnoUninstaller(dir) {
		a.Logger.Errorln("cleanupStaleProgramDir(): el directorio huérfano tiene desinstalador de Inno Setup, no se borra:", dir)
		return
	}

	stale := filepath.Join(dir, a.MeshInstaller)
	dst := filepath.Join(a.ProgramDir, a.MeshInstaller)
	if rmm.FileExists(stale) && !rmm.FileExists(dst) {
		if err := copyFile(stale, dst); err != nil {
			a.Logger.Errorln("cleanupStaleProgramDir() copia del instalador de mesh:", err)
		}
	}

	a.Logger.Infoln("Borrando el directorio de instalación huérfano:", dir)
	if err := os.RemoveAll(dir); err != nil {
		a.Logger.Errorln("cleanupStaleProgramDir() RemoveAll:", err)
	}
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
