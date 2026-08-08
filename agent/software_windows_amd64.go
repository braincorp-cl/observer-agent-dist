package agent

import (
	"fmt"

	wapi "github.com/iamacarpet/go-win64api"
	ormm "github.com/braincorp-cl/observer-agent-dist/shared"
)

// GetInstalledSoftware devuelve el inventario y si es CONFIABLE.
//
// El segundo valor existe porque el servidor REEMPLAZA el inventario de la
// máquina con lo que llegue: una lectura fallida que devuelva una lista corta
// —o vacía— borra lo que el servidor ya sabía y en la consola se lee como un
// hecho del equipo y no como un error. `InstalledSoftwareList` del upstream
// aborta el listado entero ante un `OpenKey` fallido, así que basta una subclave
// del Uninstall con ACL restrictiva para llegar acá con `err`.
func (a *Agent) GetInstalledSoftware() ([]ormm.WinSoftwareList, bool) {
	ret := make([]ormm.WinSoftwareList, 0)

	sw, err := wapi.InstalledSoftwareList()
	if err != nil {
		a.Logger.Errorln("GetInstalledSoftware():", err)
		return ret, false
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
	return ret, true
}
