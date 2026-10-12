package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/shirou/gopsutil/v3/process"
)

// Feature 049: el módulo nativo reemplaza al muestreador por scripts de la 048
// (actividad-instalar*.{ps1,sh}), que salió de la biblioteca. Los equipos que lo
// tenían instalado y estaban apagados al retirarlo se limpian acá, una vez por
// arranque del servicio: se quita el arranque automático, se matan los
// muestreadores y se borran programa y datos (incluidas sus capturas). Si no hay
// nada instalado no hace nada.

type legado048 struct {
	base      string   // carpeta del programa
	autostart string   // .desktop (Linux) o LaunchAgent (macOS)
	datos     []string // patrones glob de las carpetas de datos por perfil
	marca     string   // fragmento de la línea de comandos del muestreador
}

func rutasLegado048(goos string) legado048 {
	switch goos {
	case "windows":
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		sd := os.Getenv("SystemDrive")
		if sd == "" {
			sd = "C:"
		}
		return legado048{
			base:  filepath.Join(pd, "ObserverActividad"),
			datos: []string{filepath.Join(sd+`\`, "Users", "*", "AppData", "Local", "ObserverActividad")},
			marca: `observeractividad\muestreo.ps1`,
		}
	case "darwin":
		return legado048{
			base:      "/Library/Application Support/ObserverActividad",
			autostart: "/Library/LaunchAgents/cl.observer.actividad.plist",
			datos:     []string{"/Users/*/Library/Application Support/ObserverActividad"},
			marca:     "observeractividad/muestreo.sh",
		}
	default:
		return legado048{
			base:      "/opt/observer-actividad",
			autostart: "/etc/xdg/autostart/observer-actividad.desktop",
			datos:     []string{"/home/*/.local/share/observer-actividad"},
			marca:     "observer-actividad/muestreo.sh",
		}
	}
}

func (l legado048) instalado() bool {
	for _, p := range []string{l.base, l.autostart} {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// borrar quita autostart, programa y datos; devuelve lo que borró.
func (l legado048) borrar() []string {
	var borrados []string
	rutas := []string{l.autostart, l.base}
	for _, patron := range l.datos {
		m, _ := filepath.Glob(patron)
		rutas = append(rutas, m...)
	}
	for _, p := range rutas {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if os.RemoveAll(p) == nil {
			borrados = append(borrados, p)
		}
	}
	return borrados
}

func matarMuestreadores048(marca string) {
	procs, err := process.Processes()
	if err != nil {
		return
	}
	for _, p := range procs {
		cmd, err := p.Cmdline()
		if err == nil && strings.Contains(strings.ToLower(cmd), marca) {
			_ = p.Kill()
		}
	}
}

func (a *Agent) retirarActividad048() {
	l := rutasLegado048(runtime.GOOS)
	if !l.instalado() {
		return
	}
	switch runtime.GOOS {
	case "windows":
		_ = exec.Command("schtasks.exe", "/Delete", "/TN", "ObserverActividad", "/F").Run()
	case "darwin":
		// Con KeepAlive, launchd relanzaría el muestreador: primero descargarlo.
		if out, err := exec.Command("stat", "-f", "%u", "/dev/console").Output(); err == nil {
			if uid := strings.TrimSpace(string(out)); uid != "" && uid != "0" {
				_ = exec.Command("launchctl", "bootout", "gui/"+uid, l.autostart).Run()
			}
		}
	}
	matarMuestreadores048(l.marca)
	for _, p := range l.borrar() {
		a.Logger.Infoln("actividad: retirado el muestreador por scripts (048):", p)
	}
}
