//go:build !windows && !darwin

package agent

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Feature 023 · provider Linux/Unix.
//
// Linux no tiene una API de ubicación nativa universal (GeoClue2 requiere de
// todos modos un backend WiFi/MLS y suele faltar en servidores). Estrategia
// (veredicto del spike T005, ver investigation.md §2): escanear los BSSIDs WiFi
// con nmcli y resolverlos vía resolveWifiLocation() (código común): backend
// Observer RMM (Google server-side, modelo Prey) o, si se setea
// OBSERVER_GEO_WIFI_URL, un endpoint MLS/BeaconDB externo. Sin APs o sin
// resolución → "no disponible" → GetLocation degrada a IP.

var macRe = regexp.MustCompile(`([0-9A-Fa-f]{2}(?:\\?:)){5}[0-9A-Fa-f]{2}`)

func osLocation(a *Agent) (Location, error) {
	return a.resolveWifiLocation(scanWifiBSSIDs(a))
}

// scanWifiBSSIDs devuelve los puntos de acceso visibles usando nmcli, con BSSID,
// señal y canal (los mismos campos que Prey manda a resolver). Si nmcli no está o
// falla, devuelve lista vacía (→ degradación a IP). No expone las MAC en logs de
// nivel normal.
//
// nmcli -t separa campos con ":" y escapa los ":" internos del BSSID como "\:".
// Se sustituye el escape por un placeholder para poder separar los 3 campos con
// seguridad (BSSID:SIGNAL:CHAN) y luego se restaura.
func scanWifiBSSIDs(a *Agent) []wifiAP {
	out, err := exec.Command("nmcli", "-t", "-f", "BSSID,SIGNAL,CHAN", "dev", "wifi").Output()
	if err != nil {
		a.Logger.Debugln("scanWifiBSSIDs: nmcli no disponible:", err)
		return nil
	}
	var aps []wifiAP
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(strings.ReplaceAll(line, `\:`, "\x00"), ":")
		if len(fields) < 2 {
			continue
		}
		mac := macRe.FindString(strings.ReplaceAll(fields[0], "\x00", ":"))
		if mac == "" {
			continue
		}
		// Señal nmcli 0-100 → dBm aprox. Default razonable si no se parsea.
		signal := -65
		if pct, perr := strconv.Atoi(strings.TrimSpace(fields[1])); perr == nil && pct > 0 {
			signal = pct/2 - 100 // 100%→-50dBm, 0%→-100dBm (aprox.)
		}
		channel := 0
		if len(fields) >= 3 {
			if ch, cerr := strconv.Atoi(strings.TrimSpace(fields[2])); cerr == nil {
				channel = ch
			}
		}
		aps = append(aps, wifiAP{MacAddress: mac, SignalStrength: signal, Channel: channel})
	}
	return aps
}
