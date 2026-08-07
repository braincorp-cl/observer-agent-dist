package agent

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// El agente no puede deducir dónde está instalado a partir de %ProgramFiles%.
//
// Un agente de 32 bits sobre Windows x64 corre bajo WOW64, donde esa variable
// vale `C:\Program Files (x86)`, mientras que el instalador deja los archivos en
// `C:\Program Files` (build/setup.iss, DefaultDirName). Ninguna de las dos rutas
// consulta la versión de Windows: la divergencia es puramente WOW64, así que
// aparece en cualquier x64 —Win7, Win10, Win11— donde se haya elegido el
// instalador de 32 bits.
//
// Con las dos rutas apuntando a directorios distintos, el servicio quedaba
// registrado en una y el instalador escribía en la otra: la actualización nunca
// surtía efecto y el equipo volvía a descargarla cada hora, en silencio.
//
// La salida es dejar de adivinar. La ubicación real manda, y se averigua por dos
// vías independientes antes de caer al valor histórico.
const installDirRegValue = "InstallDir"

// ResolveProgramDir devuelve el directorio donde el agente está realmente
// instalado, en este orden:
//
//  1. El directorio del ejecutable en curso, si contiene un desinstalador de
//     Inno Setup. Es un hecho del sistema de archivos, no un dato declarado, y
//     por eso va primero: no puede quedar rancio.
//  2. `InstallDir` del registro, grabado por el propio agente al instalarse.
//     Cubre a los procesos que no corren desde el directorio de instalación
//     (checkin, taskrunner, scripts, diagnóstico desde una consola cualquiera).
//  3. El valor histórico `%ProgramFiles%\ObserverAgent`. Es el correcto en amd64
//     y en Windows de 32 bits genuino, donde no hay divergencia que resolver.
func ResolveProgramDir() string {
	if dir := selfInstallDir(); dir != "" {
		return dir
	}
	if dir := readInstallDir(); dir != "" {
		return dir
	}
	return defaultProgramDir()
}

func defaultProgramDir() string {
	return filepath.Join(os.Getenv("ProgramFiles"), progFilesName)
}

// selfInstallDir devuelve el directorio del ejecutable en curso, pero sólo
// cuando ese directorio es de verdad una instalación de Inno Setup.
//
// El `unins*.exe` es la firma: Inno lo crea siempre en `{app}` y en ninguna otra
// parte. Sin esa comprobación, el binario recién descargado que corre desde una
// carpeta temporal —el método manual, sin instalador— se declararía a sí mismo
// instalado ahí.
func selfInstallDir() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	if !strings.EqualFold(filepath.Base(self), winExeName) {
		return ""
	}
	dir := filepath.Dir(self)
	matches, err := filepath.Glob(filepath.Join(dir, "unins*.exe"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	return dir
}

func readInstallDir() string {
	// QUERY_VALUE y no ALL_ACCESS: esto también lo lee `observeragent.exe` sin
	// argumentos desde una consola de usuario, que no viene elevada.
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\ObserverRMM`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()

	dir, _, err := k.GetStringValue(installDirRegValue)
	if err != nil {
		return ""
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	return filepath.Clean(dir)
}

// writeInstallDir graba el directorio de instalación, y sólo lo graba: abre la
// clave, nunca la crea.
//
// La diferencia no es cosmética. La existencia de `SOFTWARE\ObserverRMM` es la
// señal que usan `checkExistingAndRemove` para abortar ante una instalación
// previa y `InstallService` para saltarse la primera pasada de Inno Setup. Si
// esta función creara la clave, una instalación nueva se vería a sí misma como
// una reinstalación y se negaría a continuar.
func writeInstallDir(dir string) error {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\ObserverRMM`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	return k.SetStringValue(installDirRegValue, dir)
}
