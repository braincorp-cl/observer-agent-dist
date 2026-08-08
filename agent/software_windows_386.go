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
// Lo que se devuelve además de la lista es para que ningún faltante quede mudo:
// la cantidad de subclaves ilegibles y los errores de cada vista. Un inventario
// corto sin explicación es el mismo modo de falla silencioso que este archivo
// está corrigiendo, así que las fallas parciales se reportan en vez de tragarse.
//
// Y una vista que falla NO descarta la otra: son dos lecturas independientes.
// Devolver nil porque una de las dos falló convertía «faltan 97 de 424» en «cero
// programas instalados», que es peor: el servidor reemplaza el inventario de la
// máquina por nada y en la consola se lee como si no tuviera software.
func installedSoftwareList() ([]so.Software, int, []error) {
	var errs []error

	wow64, err := isWow64()
	if err != nil {
		// No se pudo averiguar. Se sigue por la rama de vista única, que es lo
		// único que se puede hacer, pero el error se reporta: si esto pasara en
		// un x64, el agente volvería a leer sólo la vista redirigida y a
		// reportar 97 de 424 claves sin que nada lo delatara.
		errs = append(errs, fmt.Errorf("no se pudo determinar si el proceso corre bajo WOW64: %s", err.Error()))
	}

	if !wow64 {
		// Windows de 32 bits genuino: hay una sola vista y las banderas WOW64 no
		// significan nada. Pedir las dos devolvería el mismo listado dos veces.
		list, skipped, err := getSoftwareList(uninstallKey, "X32", 0)
		if err != nil {
			errs = append(errs, err)
		}
		return list, skipped, errs
	}

	list := make([]so.Software, 0)
	skipped := 0

	sw64, skipped64, err := getSoftwareList(uninstallKey, "X64", registry.WOW64_64KEY)
	if err != nil {
		errs = append(errs, fmt.Errorf("vista de 64 bits: %s", err.Error()))
	} else {
		list = append(list, sw64...)
		skipped += skipped64
	}

	sw32, skipped32, err := getSoftwareList(uninstallKey, "X32", registry.WOW64_32KEY)
	if err != nil {
		errs = append(errs, fmt.Errorf("vista de 32 bits: %s", err.Error()))
	} else {
		list = append(list, sw32...)
		skipped += skipped32
	}

	return list, skipped, errs
}

// isWow64 responde si este proceso de 32 bits corre sobre un Windows de 64 bits.
//
// Devuelve el error en vez de colapsarlo en false: «Windows de 32 bits genuino»
// y «no se pudo averiguar» llevan a la misma rama pero no son lo mismo, y
// confundirlos reintroduce el defecto sin dejar rastro.
//
// Se usa IsWow64Process y no IsWow64Process2: la segunda existe recién desde
// Windows 10, y este binario es justamente el de la pata legacy, que tiene que
// correr en Windows 7 (ADR-023).
func isWow64() (bool, error) {
	var wow64 bool
	if err := windows.IsWow64Process(windows.CurrentProcess(), &wow64); err != nil {
		return false, err
	}
	return wow64, nil
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

// GetInstalledSoftware devuelve el inventario y si es CONFIABLE.
//
// El segundo valor no es opcional ni decorativo: el servidor REEMPLAZA el
// inventario de la máquina con lo que llegue, así que enviar una lista corta es
// peor que no enviar nada. Un admin que audite software desde la consola
// —buscando por ejemplo una versión vulnerable de un producto de 64 bits— vería
// la máquina limpia sin ninguna marca de que la lectura falló; el único rastro
// sería un `Errorln` en el `agent.log` de ese equipo.
//
// Que la lista NO esté vacía no basta para creerle: si `isWow64` falla se cae a
// la rama de vista única, que bajo WOW64 devuelve ~97 de 424 entradas y no está
// vacía en absoluto.
func (a *Agent) GetInstalledSoftware() ([]ormm.WinSoftwareList, bool) {
	ret := make([]ormm.WinSoftwareList, 0)

	sw, skipped, errs := installedSoftwareList()
	for _, e := range errs {
		a.Logger.Errorln("GetInstalledSoftware():", e)
	}
	if skipped > 0 {
		a.Logger.Errorf("GetInstalledSoftware(): %d subclaves del registro no se pudieron leer; el inventario va incompleto.", skipped)
	}
	complete := len(errs) == 0 && skipped == 0

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
	return ret, complete
}
