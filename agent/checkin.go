package agent

import (
	"runtime"
	"time"

	nats "github.com/nats-io/nats.go"
	"github.com/ugorji/go/codec"
	ormm "github.com/braincorp-cl/observer-agent-dist/shared"
)

func (a *Agent) NatsMessage(nc *nats.Conn, mode string) {
	var resp []byte
	var payload interface{}
	ret := codec.NewEncoderBytes(&resp, new(codec.MsgpackHandle))

	switch mode {
	case "agent-hello":
		payload = ormm.CheckInNats{
			Agentid: a.AgentID,
			Version: a.Version,
		}
	case "agent-winsvc":
		payload = ormm.WinSvcNats{
			Agentid: a.AgentID,
			WinSvcs: a.GetServices(),
		}
	case "agent-agentinfo":
		osinfo := a.osString()
		reboot, err := a.SystemRebootRequired()
		if err != nil {
			reboot = false
		}
		payload = ormm.AgentInfoNats{
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
		payload = ormm.WinWMINats{
			Agentid: a.AgentID,
			WMI:     a.GetWMIInfo(),
		}
	case "agent-disks":
		payload = ormm.WinDisksNats{
			Agentid: a.AgentID,
			Disks:   a.GetDisks(),
		}
	case "agent-publicip":
		payload = ormm.PublicIPNats{
			Agentid:  a.AgentID,
			PublicIP: a.PublicIP(),
		}
	case "agent-geolocation":
		// Feature 023. La medición y la publicación viven en PublishGeo porque el
		// ciclo del modo perdido (feature 030) necesita las dos por separado: mide
		// una sola vez y usa el MISMO punto para publicarlo acá y para adjuntarlo
		// a la evidencia. Medirlo dos veces costaría hasta 40 s de más en macOS y,
		// peor, dejaría el punto del mapa y el de la evidencia en instantes
		// distintos — dos respuestas para la misma pregunta.
		a.PublishGeo(nc, a.GetLocation())
		return
	}

	a.Logger.Debugln(mode, payload)
	ret.Encode(payload)
	nc.PublishRequest(a.AgentID, mode, resp)
}

// PublishGeo publica por NATS un punto YA MEDIDO.
//
// Sólo se publica cuando hay un fix con coordenadas (native/wifi/ip). Los
// estados denied/unavailable NO generan punto: es privacidad y es CONTRACT-01
// —nada de filas con coordenadas nulas—, y además una fila "sin fix" en el
// recorrido de un caso de robo se ve igual que una posición y no lo es.
func (a *Agent) PublishGeo(nc *nats.Conn, loc Location) {
	if !loc.HasFix() {
		a.Logger.Debugln("geo: sin fix, no se publica")
		return
	}

	var resp []byte
	ret := codec.NewEncoderBytes(&resp, new(codec.MsgpackHandle))
	ret.Encode(ormm.GeoNats{
		Func:       "geo",
		Agentid:    a.AgentID,
		Version:    a.Version,
		Lat:        loc.Lat,
		Long:       loc.Long,
		AccuracyM:  loc.AccuracyM,
		Source:     loc.Source,
		CapturedAt: loc.CapturedAt.Unix(),
	})

	nc.PublishRequest(a.AgentID, "agent-geolocation", resp)
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
