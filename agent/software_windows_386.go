package agent

import (
	"fmt"

	so "github.com/iamacarpet/go-win64api/shared"
	wapf "github.com/wh1te909/go-win64api"
	trmm "github.com/braincorp-cl/observer-agent-dist/shared"
)

func installedSoftwareList() ([]so.Software, error) {
	sw32, err := wapf.GetSoftwareList(`SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`, "X32")
	if err != nil {
		return nil, err
	}

	return sw32, nil
}

func (a *Agent) GetInstalledSoftware() []trmm.WinSoftwareList {
	ret := make([]trmm.WinSoftwareList, 0)

	sw, err := installedSoftwareList()
	if err != nil {
		return ret
	}

	for _, s := range sw {
		t := s.InstallDate
		ret = append(ret, trmm.WinSoftwareList{
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
