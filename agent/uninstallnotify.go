package agent

import (
	"net"
	"runtime"
	"strings"
	"time"
)

// Aviso de desinstalación: el equipo le cuenta al servidor que lo están
// desmantelando, ANTES de desmantelarse.
//
// El problema que resuelve: la desinstalación local no habla con el servidor.
// El script (o el uninstaller de InnoSetup en Windows) para el servicio, borra
// binarios y limpia el mesh, pero la fila del agente en la consola sobrevive y
// el equipo queda para siempre como un Offline que ya no existe. Con el aviso,
// el servidor levanta la alerta, deja el registro de auditoría y programa el
// borrado del agente y del nodo Mesh, igual que si lo hubieran borrado desde la
// web.
//
// En Linux y macOS el aviso lo manda el propio script de desinstalación, que es
// quien tiene el contexto de quién lo ejecutó (`SUDO_USER`). En Windows no hay
// script: el punto de entrada es `observeragent.exe -m cleanup`, que el
// `[UninstallRun]` del instalador ya invoca, y de ahí sale esta ruta.

type uninstallNotice struct {
	AgentID     string `json:"agent_id"`
	Actor       string `json:"actor"`
	SudoUser    string `json:"sudo_user"`
	LoginUser   string `json:"login_user"`
	ConsoleUser string `json:"console_user"`
	LanIPs      string `json:"lan_ips"`
	LocalTime   string `json:"local_time"`
	Source      string `json:"source"`
}

// localIPs junta las IPv4 no-loopback del equipo. Si acá no sale nada, el
// servidor cae a la última IP que el agente reportó, así que no es crítico.
func localIPs() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}

	ips := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		if v4 := ipnet.IP.To4(); v4 != nil {
			ips = append(ips, v4.String())
		}
	}

	return strings.Join(ips, ",")
}

// NotifyUninstall avisa y se olvida.
//
// Nunca puede hacer fallar la desinstalación: sin red, con el servidor caído o
// sin token, se calla y vuelve. Lo que se pierde es el aviso, no la
// desinstalación — el equipo tiene que quedar limpio pase lo que pase.
func (a *Agent) NotifyUninstall(source string) {
	if a.AgentID == "" {
		return
	}

	actor, sudoUser, loginUser, consoleUser := uninstallActor()

	payload := uninstallNotice{
		AgentID:     a.AgentID,
		Actor:       actor,
		SudoUser:    sudoUser,
		LoginUser:   loginUser,
		ConsoleUser: consoleUser,
		LanIPs:      localIPs(),
		LocalTime:   time.Now().Format(time.RFC3339),
		Source:      source,
	}

	if _, err := a.rClient.R().SetBody(payload).Post("/api/v3/uninstalled/"); err != nil {
		// Debug y no Error: en una desinstalación el servidor puede ser
		// inalcanzable por razones perfectamente normales (el equipo se está
		// sacando de la red, justamente).
		a.Logger.Debugln("NotifyUninstall:", err)
	}
}

// uninstallSource nombra el origen del aviso para que quede en la auditoría.
func uninstallSource() string {
	return "agent-" + runtime.GOOS
}
