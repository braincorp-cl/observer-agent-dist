//go:build !windows
// +build !windows

package agent

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"time"

	ormm "github.com/braincorp-cl/observer-agent-dist/shared"
	"github.com/spf13/viper"
)

func (a *Agent) installerMsg(msg, alert string, silent bool) {
	if alert == "error" {
		a.Logger.Fatalln(msg)
	} else {
		a.Logger.Info(msg)
	}
}

func createAgentConfig(baseurl, agentid, apiurl, token, agentpk, cert, proxy, meshdir, natsport string, insecure bool, unixtmpdir string) {
	viper.SetConfigType("json")
	viper.Set("baseurl", baseurl)
	viper.Set("agentid", agentid)
	viper.Set("apiurl", apiurl)
	viper.Set("token", token)
	viper.Set("agentpk", agentpk)
	viper.Set("cert", cert)
	viper.Set("proxy", proxy)
	viper.Set("meshdir", meshdir)
	viper.Set("natsstandardport", natsport)
	viper.Set("tmpdir", unixtmpdir)

	if insecure {
		viper.Set("insecure", "true")
	}
	viper.SetConfigPermissions(0660)
	err := viper.SafeWriteConfigAs(etcConfig)
	if err != nil {
		log.Fatalln("createAgentConfig", err)
	}
}

func (a *Agent) checkExistingAndRemove(silent bool) {
	if runtime.GOOS == "darwin" {
		if ormm.FileExists(a.MeshSystemEXE) {
			a.Logger.Infoln("Existing meshagent found, attempting to remove...")
			uopts := a.NewCMDOpts()
			// `--no-embedded=1` va DESPUÉS del comando, igual que en los scripts
			// del backend. El binario que MeshCentral entrega para instalar trae
			// un instalador JS anexado que se dispara ante CUALQUIER argumento,
			// también ante `-fulluninstall`: medido en Linux (HP-ProOne-400,
			// 2026-07-29) abre un diálogo en el escritorio y se queda esperando
			// un clic que nadie va a dar. Acá eso colgaría la INSTALACIÓN, que
			// es lo que esta limpieza previa está habilitando.
			uopts.Command = fmt.Sprintf("%s -fulluninstall --no-embedded=1", a.MeshSystemEXE)
			uout := a.CmdV2(uopts)
			fmt.Println(uout.Stdout)
			time.Sleep(1 * time.Second)
		}

		if ormm.FileExists(macPlistPath) {
			a.Logger.Infoln("Existing observeragent plist found, attempting to remove...")
			opts := a.NewCMDOpts()
			opts.Command = fmt.Sprintf("launchctl bootout system %s", macPlistPath)
			a.CmdV2(opts)
		}

		// Feature 030 · T016: el ayudante Aqua se descarga de la sesión del
		// usuario ANTES de borrar su plist. Al revés queda un trabajo registrado
		// apuntando a un archivo que ya no existe, y launchd lo arrastra hasta
		// que la persona cierre sesión.
		a.descargarAyudanteCaptura()

		os.RemoveAll(defaultMacMeshSvcDir)
		os.RemoveAll(nixMeshDir)
		os.Remove(etcConfig)
		os.RemoveAll(nixAgentDir)
		os.Remove(macPlistPath)
		os.Remove(capturaAquaPlist)
	}
}

func DisableSleepHibernate() {}

func EnablePing() {}

func EnableRDP() {}
