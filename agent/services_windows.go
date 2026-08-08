package agent

import (
	"fmt"
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
		// Devolver error y no nil: este equipo NO quedó reparado, y `-m installsvc`
		// tiene que salir con código distinto de cero para que el `install service:`
		// del log de Inno lo distinga de uno que sí se reparó. Si acá se retorna
		// nil, la única señal que permitiría encontrarlo reporta éxito y el equipo
		// se queda en el bucle de descarga horaria para siempre.
		return fmt.Errorf("no se pudo determinar el ejecutable del servicio, no se reapunta: %q", conf.BinaryPathName)
	}
	if strings.EqualFold(filepath.Clean(oldExe), filepath.Clean(a.EXE)) {
		return nil
	}

	// El destino tiene que existir ANTES de reapuntar, y tiene que ser un ARCHIVO
	// que se llame como el agente. Si no, esta función dejaría el servicio mirando
	// a algo que no arranca: la máquina quedaría sin agente y sin forma de volver
	// desde la consola.
	//
	// Pasa cuando `-m installsvc` corre desde una carpeta cualquiera en un equipo
	// instalado con /DIR: ahí no hay `unins*.exe` que reconocer ni `InstallDir`
	// que leer, y ResolveProgramDir cae al valor por defecto, que no es el suyo.
	if !isAgentExeFile(a.EXE) {
		return fmt.Errorf("no se reapunta el servicio: el binario de destino no existe o no es un archivo: %q", a.EXE)
	}

	a.Logger.Infof("El servicio apunta a %s pero el agente está instalado en %s; reapuntando.", oldExe, a.EXE)

	conf.BinaryPathName = replaceServiceExePath(conf.BinaryPathName, oldExe, a.EXE)
	if err := srv.UpdateConfig(conf); err != nil {
		return err
	}

	oldDir := filepath.Dir(oldExe)
	a.rescueMeshInstaller(oldDir)
	a.refreshStaleAgentBinary(oldDir)
	return nil
}

// refreshStaleAgentBinary deja también en el directorio viejo una copia del
// binario nuevo.
//
// No es redundancia: `tasks_windows.go` crea cada tarea programada con `Path`
// RELATIVO (`observeragent.exe`) y `WorkingDir = a.ProgramDir`, y el Programador
// de tareas resuelve ese nombre contra el WorkingDir **grabado cuando la tarea se
// creó**. En un equipo divergente eso apunta al directorio viejo, y como el
// arreglo dejó de borrarlo —a propósito, porque borrarlo mataba las tareas— esas
// tareas seguirían ejecutando el binario ANTERIOR indefinidamente, sin que
// ninguna corrección del update alcance a los scripts programados, mientras la
// consola muestra la máquina como actualizada.
//
// Reescribir la definición de cada tarea sería lo «limpio», pero exige recorrer y
// modificar el Programador de tareas en mitad de una actualización desatendida.
// Copiar un archivo hace lo mismo y no toca nada del sistema.
func (a *Agent) refreshStaleAgentBinary(oldDir string) {
	if oldDir == "" || sameDir(oldDir, a.ProgramDir) {
		return
	}

	stale := filepath.Join(oldDir, winExeName)
	if !isAgentExeFile(stale) {
		return
	}

	if err := copyFile(a.EXE, stale); err != nil {
		// Es esperable que falle si algo lo está ejecutando justo ahora: Windows
		// no deja sobrescribir un .exe mapeado. No es fatal.
		a.Logger.Errorln("refreshStaleAgentBinary():", err)
		return
	}
	a.Logger.Infoln("Binario nuevo copiado también al directorio viejo, para las tareas programadas que lo tienen de WorkingDir:", oldDir)
}

// isAgentExeFile responde si la ruta es un ARCHIVO que se llama como el binario
// del agente.
//
// No se usa `rmm.FileExists` a propósito: ese helper es `os.Stat` y devuelve
// **true para directorios** —y también ante errores que no sean «no existe»,
// como permiso denegado—. Con él, al resolver un BinaryPathName sin comillas la
// candidata `C:\Program Files` se aceptaba como «el ejecutable», y la reescritura
// producía una línea donde `-m svc` quedaba detrás de un argumento posicional.
// Go deja de parsear flags ahí, el agente sale de inmediato, el SCM da error 1053
// y el equipo queda muerto — sin downgrade ni update lateral que lo rescate.
func isAgentExeFile(path string) bool {
	if !looksLikeAgentExe(path) {
		return false
	}
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Mode().IsRegular()
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

	// El rescate va ACÁ y no sólo en el camino de actualización. Separar el
	// rescate del borrado dejó un hueco: una máquina que ya no recibe otro update
	// —porque ya está en LATEST_AGENT_VER, o porque un admin arregló la ruta del
	// servicio a mano— nunca pasa por `ReconcileService`, así que su `meshagent.exe`
	// nunca se rescata… y este borrado se lleva la ÚNICA copia de la máquina. El
	// `[UninstallRun] {app}\meshagent.exe -fulluninstall` no encontraría nada, el
	// nodo seguiría vivo en MeshCentral y no quedaría binario local para
	// desregistrarlo a mano. Rescatar primero es lo que hacía el código anterior,
	// de forma atómica, y se había perdido al separarlos.
	a.rescueMeshInstaller(legacy)

	// Un directorio con `unins*.exe` es OTRA instalación de Inno Setup, con su
	// entrada viva en «Agregar o quitar programas». Pasa en un equipo instalado con
	// /DIR fuera del directorio por defecto donde además existe un
	// `%ProgramFiles%\ObserverAgent` por su cuenta. Borrarlo dejaría esa entrada
	// apuntando a un desinstalador inexistente, imposible de quitar: el mismo
	// residuo en ARP que se persigue aparte. Esta guarda existía y se perdió al
	// mover el borrado; vuelve.
	if hasInnoUninstaller(legacy) {
		a.Logger.Errorln("removeLegacyProgramDir(): el directorio tiene desinstalador de Inno Setup, es otra instalación y no se borra:", legacy)
		return
	}

	// Y tiene que contener el binario del agente. El nombre del directorio solo no
	// alcanza para afirmar que es nuestro.
	if !isAgentExeFile(filepath.Join(legacy, winExeName)) {
		a.Logger.Errorln("removeLegacyProgramDir(): el directorio no contiene el binario del agente, no se borra:", legacy)
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
		// `isAgentExeFile` y no `rmm.FileExists`: la comprobación laxa aceptaba
		// DIRECTORIOS, así que en `C:\Program Files (x86)\ObserverAgent\observeragent.exe -m svc`
		// sin comillas la candidata `C:\Program Files` —que existe, porque es un
		// directorio— se tomaba como el ejecutable y la reescritura dejaba el
		// servicio sin `-m svc`, o sea el agente muerto y sin rescate por red.
		if isAgentExeFile(cand) {
			return cand
		}
	}

	// Ninguna existe: el binario registrado ya no está. Queda el parseo
	// sintáctico, que acierta salvo con rutas con espacios y sin comillas; se
	// exige igual que el nombre sea el del agente, porque de acá sale la ruta que
	// se compara y se reescribe.
	if exe, ok := parseServiceExePath(binPath); ok && looksLikeAgentExe(exe) {
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
