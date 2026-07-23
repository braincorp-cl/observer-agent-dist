//go:build !windows && !darwin

package agent

import (
	"encoding/json"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

// Feature 023 · provider Linux/Unix.
//
// Linux no tiene una API de ubicación nativa universal (GeoClue2 requiere de
// todos modos un backend WiFi/MLS y suele faltar en servidores). Estrategia
// (veredicto del spike T005, ver investigation.md §2):
//  1. Escanear BSSIDs WiFi con nmcli.
//  2. Si hay APs y existe un endpoint WiFi configurado (OBSERVER_GEO_WIFI_URL,
//     formato compatible MLS/BeaconDB) → resolver a lat/long → source wifi.
//  3. Sin APs o sin endpoint → "no disponible" → GetLocation degrada a IP.
//
// El endpoint WiFi es OPCIONAL y está apagado por defecto: la cobertura real de
// BeaconDB en Chile queda pendiente de validar en staging. Sin él, un notebook
// Linux cae a IP igual que un servidor.

var macRe = regexp.MustCompile(`([0-9A-Fa-f]{2}(?:\\?:)){5}[0-9A-Fa-f]{2}`)

type wifiAP struct {
	MacAddress     string `json:"macAddress"`
	SignalStrength int    `json:"signalStrength"`
}

func osLocation(a *Agent) (Location, error) {
	aps := scanWifiBSSIDs(a)
	wifiURL := geoEnv("OBSERVER_GEO_WIFI_URL", "")
	if len(aps) == 0 || wifiURL == "" {
		return Location{Source: SourceUnavailable}, errNativeUnavailable
	}
	loc, err := a.wifiGeolocation(wifiURL, aps)
	if err != nil {
		a.Logger.Debugln("wifiGeolocation err:", err)
		return Location{Source: SourceUnavailable}, err
	}
	return loc, nil
}

// scanWifiBSSIDs devuelve los puntos de acceso visibles usando nmcli. Si nmcli
// no está o falla, devuelve lista vacía (→ degradación a IP). No expone las MAC
// en logs de nivel normal.
func scanWifiBSSIDs(a *Agent) []wifiAP {
	out, err := exec.Command("nmcli", "-t", "-f", "BSSID,SIGNAL", "dev", "wifi").Output()
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
		m := macRe.FindString(line)
		if m == "" {
			continue
		}
		mac := strings.ReplaceAll(m, `\:`, ":")
		// La señal (0-100 en nmcli) es el último campo numérico; se convierte a
		// dBm aproximado. Si no se parsea, se usa un valor por defecto razonable.
		signal := -65
		if idx := strings.LastIndex(line, ":"); idx >= 0 {
			if pct, perr := strconv.Atoi(strings.TrimSpace(line[idx+1:])); perr == nil && pct > 0 {
				signal = pct/2 - 100 // 100%→-50dBm, 0%→-100dBm (aprox.)
			}
		}
		aps = append(aps, wifiAP{MacAddress: mac, SignalStrength: signal})
	}
	return aps
}

// wifiGeolocation consulta un servicio de geolocalización WiFi (formato MLS/
// BeaconDB: POST {wifiAccessPoints:[...]} → {location:{lat,lng},accuracy}).
func (a *Agent) wifiGeolocation(url string, aps []wifiAP) (Location, error) {
	client := resty.New()
	client.SetHeader("User-Agent", a.AgentHeader)
	client.SetTimeout(10 * time.Second)
	if len(a.Proxy) > 0 {
		client.SetProxy(a.Proxy)
	}
	reqBody := map[string]interface{}{
		"considerIp":       true,
		"wifiAccessPoints": aps,
	}
	r, err := client.R().SetHeader("Content-Type", "application/json").SetBody(reqBody).Post(url)
	if err != nil {
		return Location{}, err
	}
	var resp struct {
		Location struct {
			Lat  float64 `json:"lat"`
			Lng  float64 `json:"lng"`
		} `json:"location"`
		Accuracy float64 `json:"accuracy"`
	}
	if err := json.Unmarshal(r.Body(), &resp); err != nil {
		return Location{}, err
	}
	return Location{
		Lat:       resp.Location.Lat,
		Long:      resp.Location.Lng,
		AccuracyM: int(resp.Accuracy),
		Source:    SourceWiFi,
	}, nil
}
