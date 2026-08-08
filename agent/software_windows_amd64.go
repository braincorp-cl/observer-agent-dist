package agent

import (
	"fmt"

	wapi "github.com/iamacarpet/go-win64api"
	ormm "github.com/braincorp-cl/observer-agent-dist/shared"
)

// El error de `InstalledSoftwareList` se REGISTRA en vez de tragarse: esa función
// del upstream aborta el listado entero ante el primer `OpenKey` fallido, así que
// basta una subclave del Uninstall con ACL restrictiva para devolver cero
// programas — y sin log, en la consola eso se lee como un hecho del equipo.
func (a *Agent) GetInstalledSoftware() []ormm.WinSoftwareList {
	ret := make([]ormm.WinSoftwareList, 0)

	sw, err := wapi.InstalledSoftwareList()
	if err != nil {
		a.Logger.Errorln("GetInstalledSoftware():", err)
		return ret
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
