package agent

import (
	"fmt"
	"time"

	ormm "github.com/braincorp-cl/observer-agent-dist/shared"
	so "github.com/iamacarpet/go-win64api/shared"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const uninstallKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`

// El inventario de software del agente de 32 bits veía menos de la cuarta parte
// del software instalado —97 de 424 claves en el equipo donde se midió— y no
// avisaba de nada: devolvía una lista corta como si fuera completa.
//
// La causa es la redirección de registro de WOW64. Un proceso de 32 bits que
// abre `HKLM\SOFTWARE\...` queda redirigido a `HKLM\SOFTWARE\WOW6432Node\...`,
// así que sólo ve el software de 32 bits. La implementación anterior pasaba
// `"X32"` como segundo argumento creyendo que seleccionaba una vista, pero ese
// parámetro es sólo una ETIQUETA de texto que termina copiada en el campo Arch:
// no toca el acceso al registro.
//
// La vista se elige con KEY_WOW64_64KEY / KEY_WOW64_32KEY al abrir la clave, que
// es lo que hace esta implementación. Con eso el agente de 32 bits reporta las
// dos vistas, igual que el de 64 bits.
//
// El segundo valor de retorno es la cantidad de subclaves que no se pudieron
// leer. Se devuelve en vez de tragarse: un inventario corto sin explicación es
// el mismo modo de falla silencioso que este archivo está corrigiendo.
func installedSoftwareList() ([]so.Software, int, error) {
	if !isWow64() {
		// Windows de 32 bits genuino: hay una sola vista y las banderas WOW64 no
		// significan nada. Pedir las dos devolvería el mismo listado dos veces.
		return getSoftwareList(uninstallKey, "X32", 0)
	}

	sw64, skipped64, err := getSoftwareList(uninstallKey, "X64", registry.WOW64_64KEY)
	if err != nil {
		return nil, 0, err
	}
	sw32, skipped32, err := getSoftwareList(uninstallKey, "X32", registry.WOW64_32KEY)
	if err != nil {
		return nil, 0, err
	}

	return append(sw64, sw32...), skipped64 + skipped32, nil
}

// isWow64 responde si este proceso de 32 bits corre sobre un Windows de 64 bits.
//
// Se usa IsWow64Process y no IsWow64Process2: la segunda existe recién desde
// Windows 10, y este binario es justamente el de la pata legacy, que tiene que
// correr en Windows 7 (ADR-023).
func isWow64() bool {
	var wow64 bool
	if err := windows.IsWow64Process(windows.CurrentProcess(), &wow64); err != nil {
		return false
	}
	return wow64
}

func getSoftwareList(baseKey, arch string, wow64Flag uint32) ([]so.Software, int, error) {
	access := uint32(registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS) | wow64Flag

	k, err := registry.OpenKey(registry.LOCAL_MACHINE, baseKey, access)
	if err != nil {
		return nil, 0, fmt.Errorf("error al leer el registro: %s", err.Error())
	}
	defer k.Close()

	swList := make([]so.Software, 0)
	skipped := 0

	subkeys, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil, 0, fmt.Errorf("error al listar las subclaves del registro: %s", err.Error())
	}

	for _, sw := range subkeys {
		// Se abre COLGANDO del handle ya abierto y pidiendo sólo QUERY_VALUE. La
		// vista WOW64 se hereda del padre, así que no hace falta repetir la
		// bandera, y pedir de más haría fallar la apertura en subclaves con ACL
		// restrictiva: ese programa desaparecería del inventario sin dejar rastro.
		sk, err := registry.OpenKey(k, sw, registry.QUERY_VALUE)
		if err != nil {
			// Una subclave ilegible no invalida el inventario entero: se salta,
			// pero se cuenta, para que el faltante no sea invisible.
			skipped++
			continue
		}

		dn, _, err := sk.GetStringValue("DisplayName")
		if err != nil {
			sk.Close()
			continue
		}

		swv := so.Software{DisplayName: dn, Arch: arch}

		if dv, _, err := sk.GetStringValue("DisplayVersion"); err == nil {
			swv.DisplayVersion = dv
		}
		if pub, _, err := sk.GetStringValue("Publisher"); err == nil {
			swv.Publisher = pub
		}
		if id, _, err := sk.GetStringValue("InstallDate"); err == nil {
			swv.InstallDate, _ = time.Parse("20060102", id)
		}
		if es, _, err := sk.GetIntegerValue("EstimatedSize"); err == nil {
			swv.EstimatedSize = es
		}
		if cont, _, err := sk.GetStringValue("Contact"); err == nil {
			swv.Contact = cont
		}
		if hlp, _, err := sk.GetStringValue("HelpLink"); err == nil {
			swv.HelpLink = hlp
		}
		if isource, _, err := sk.GetStringValue("InstallSource"); err == nil {
			swv.InstallSource = isource
		}
		if iloc, _, err := sk.GetStringValue("InstallLocation"); err == nil {
			swv.InstallLocation = iloc
		}
		if ustring, _, err := sk.GetStringValue("UninstallString"); err == nil {
			swv.UninstallString = ustring
		}
		if mver, _, err := sk.GetIntegerValue("VersionMajor"); err == nil {
			swv.VersionMajor = mver
		}
		if mnver, _, err := sk.GetIntegerValue("VersionMinor"); err == nil {
			swv.VersionMinor = mnver
		}

		swList = append(swList, swv)
		sk.Close()
	}

	return swList, skipped, nil
}

func (a *Agent) GetInstalledSoftware() []ormm.WinSoftwareList {
	ret := make([]ormm.WinSoftwareList, 0)

	sw, skipped, err := installedSoftwareList()
	if err != nil {
		a.Logger.Errorln("GetInstalledSoftware():", err)
		return ret
	}
	if skipped > 0 {
		a.Logger.Errorf("GetInstalledSoftware(): %d subclaves del registro no se pudieron leer; el inventario va incompleto.", skipped)
	}

	for _, s := range sw {
		t := s.InstallDate
		ret = append(ret, ormm.WinSoftwareList{
			Name:        CleanString(s.Name()),
			Version:     CleanString(s.Version()),
			Publisher:   CleanString(s.Publisher),
			InstallDate: fmt.Sprintf("%02d-%d-%02d", t.Year(), t.Month(), t.Day()),
			Size:        ByteCountSI(s.EstimatedSize * 1024),
			Source:      CleanString(s.InstallSource),
			Location:    CleanString(s.InstallLocation),
			Uninstall:   CleanString(s.UninstallString),
		})
	}
	return ret
}
