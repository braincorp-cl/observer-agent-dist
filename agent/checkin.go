package agent

import (
	"runtime"
	"time"

	nats "github.com/nats-io/nats.go"
	"github.com/ugorji/go/codec"
	trmm "github.com/braincorp/observeragent/shared"
)

func (a *Agent) NatsMessage(nc *nats.Conn, mode string) {
	var resp []byte
	var payload interface{}
	ret := codec.NewEncoderBytes(&resp, new(codec.MsgpackHandle))

	switch mode {
	case "agent-hello":
		payload = trmm.CheckInNats{
			Agentid: a.AgentID,
			Version: a.Version,
		}
	case "agent-winsvc":
		payload = trmm.WinSvcNats{
			Agentid: a.AgentID,
			WinSvcs: a.GetServices(),
		}
	case "agent-agentinfo":
		osinfo := a.osString()
		reboot, err := a.SystemRebootRequired()
		if err != nil {
			reboot = false
		}
		payload = trmm.AgentInfoNats{
			Agentid:      a.AgentID,
			Username:     a.LoggedOnUser(),
			Hostname:     a.Hostname,
			OS:           osinfo,
			Platform:     runtime.GOOS,
			TotalRAM:     a.TotalRAM(),
			BootTime:     a.BootTime(),
			RebootNeeded: reboot,
			GoArch:       a.GoArch,
		}
	case "agent-wmi":
		payload = trmm.WinWMINats{
			Agentid: a.AgentID,
			WMI:     a.GetWMIInfo(),
		}
	case "agent-disks":
		payload = trmm.WinDisksNats{
			Agentid: a.AgentID,
			Disks:   a.GetDisks(),
		}
	case "agent-publicip":
		payload = trmm.PublicIPNats{
			Agentid:  a.AgentID,
			PublicIP: a.PublicIP(),
		}
	case "agent-geolocation":
		// Feature 023. Solo se publica cuando hay un fix con coordenadas
		// (native/wifi/ip). Estados denied/unavailable NO generan punto
		// (privacidad + CONTRACT-01: nada de filas con coordenadas nulas).
		loc := a.GetLocation()
		if !loc.HasFix() {
			a.Logger.Debugln("geo: sin fix, no se publica")
			return
		}
		payload = trmm.GeoNats{
			Func:       "geo",
			Agentid:    a.AgentID,
			Version:    a.Version,
			Lat:        loc.Lat,
			Long:       loc.Long,
			AccuracyM:  loc.AccuracyM,
			Source:     loc.Source,
			CapturedAt: loc.CapturedAt.Unix(),
		}
	}

	a.Logger.Debugln(mode, payload)
	ret.Encode(payload)
	nc.PublishRequest(a.AgentID, mode, resp)
}

func (a *Agent) DoNatsCheckIn() {
	opts := a.setupNatsOptions()
	nc, err := nats.Connect(a.NatsServer, opts...)
	if err != nil {
		a.Logger.Errorln(err)
		return
	}

	for _, s := range natsCheckin {
		time.Sleep(time.Duration(randRange(100, 400)) * time.Millisecond)
		a.NatsMessage(nc, s)
	}
	nc.Close()
}
