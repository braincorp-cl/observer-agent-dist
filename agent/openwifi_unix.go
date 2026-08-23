//go:build !windows && !darwin

package agent

import (
	"strings"
	"time"
)

// Feature 041 · RF-05 (T016) · backend Linux de asociación a red abierta.
//
// Consistente con geolocation_unix.go: NetworkManager vía nmcli. Una red ABIERTA
// es la que nmcli reporta con SECURITY vacío (sin WPA/WEP/802.1X). Si nmcli no
// está (equipo sin NetworkManager), escanearAbiertas devuelve vacío y el
// orquestador es no-op — mismo fail-soft que el escaneo de geolocalización.

func init() { openWifiBackendImpl = linuxOpenWifi{} }

type linuxOpenWifi struct{}

func (linuxOpenWifi) escanearAbiertas(a *Agent) []string {
	// -t: salida por campos separados por ":"; los ":" internos del SSID vienen
	// escapados como "\:" (mismo manejo que scanWifiBSSIDsNmcli).
	out, err := runCapture(15*time.Second, "nmcli", "-t", "-f", "SSID,SECURITY", "dev", "wifi")
	if err != nil {
		a.Logger.Debugln("openwifi: nmcli dev wifi no disponible:", err.Error())
		return nil
	}

	var abiertas []string
	vistas := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(strings.ReplaceAll(line, `\:`, "\x00"), ":")
		if len(fields) < 2 {
			continue
		}
		ssid := strings.ReplaceAll(fields[0], "\x00", ":")
		security := strings.TrimSpace(strings.Join(fields[1:], ":"))
		// SSID vacío = red oculta (no se puede asociar por nombre); SECURITY no
		// vacío = red con credenciales (se descarta). Sólo abiertas visibles.
		if ssid == "" || security != "" {
			continue
		}
		if !vistas[ssid] {
			vistas[ssid] = true
			abiertas = append(abiertas, ssid)
		}
	}
	return abiertas
}

func (linuxOpenWifi) conectar(a *Agent, ssid string) error {
	// Red abierta ⇒ sin password. nmcli crea una conexión guardada con el nombre
	// del SSID; el desconectar la baja y la borra para no dejar auto-reconnect.
	if _, err := runCapture(30*time.Second, "nmcli", "dev", "wifi", "connect", ssid); err != nil {
		return err
	}
	return nil
}

func (linuxOpenWifi) desconectar(a *Agent, ssid string) {
	_, _ = runCapture(10*time.Second, "nmcli", "con", "down", "id", ssid)
	// Se borra el perfil para que NetworkManager no se re-asocie solo a una red
	// que resultó cautiva o sin salida. Best-effort: si no existe, nmcli falla y
	// se ignora.
	_, _ = runCapture(10*time.Second, "nmcli", "con", "delete", "id", ssid)
}
