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
// —con `iw` (barrido activo, BSSIDs reales) y fallback a `nmcli`— y resolverlos
// vía resolveWifiLocation() (código común): backend Observer RMM (Google
// server-side, modelo Prey) o, si se setea OBSERVER_GEO_WIFI_URL, un endpoint
// MLS/BeaconDB externo. Sin APs o sin resolución → "no disponible" → GetLocation
// degrada a IP.
//
// Por qué `iw` primero: en varios equipos reales (validado en FAZOCAR, Ubuntu
// 24.04 + NetworkManager) `nmcli -f BSSID` devuelve BSSIDs ENMASCARADOS
// (00:01:02:00:00:xx) para las redes NO asociadas —solo el AP conectado trae su
// MAC real—, lo que hace que Google no ubique nada (404). `iw dev <if> scan`
// (netlink directo, como root) devuelve los BSSIDs reales de todas las vecinas y
// además fuerza un barrido fresco (análogo al WlanScan de Windows). Se mantiene
// `nmcli` como fallback por si `iw` no está instalado.

var macRe = regexp.MustCompile(`([0-9A-Fa-f]{2}(?:\\?:)){5}[0-9A-Fa-f]{2}`)

func osLocation(a *Agent) (Location, error) {
	if a.GeoForceOn {
		forceLocationOn(a)
	}
	return a.resolveWifiLocation(scanWifiBSSIDs(a))
}

// forceLocationOn enciende la radio WiFi (Feature 023 · gap 3). Linux no tiene un
// "consentimiento de ubicación" que gatee el escaneo (a diferencia de Win11 24H2):
// basta con la radio encendida. Best-effort: se ejecuta como root; cualquier fallo
// (sin rfkill/nmcli, o sin hardware WiFi) se ignora y la captura degradará a IP.
func forceLocationOn(a *Agent) {
	_ = exec.Command("rfkill", "unblock", "wifi").Run()
	_ = exec.Command("nmcli", "radio", "wifi", "on").Run()
}

// scanWifiBSSIDs devuelve los puntos de acceso visibles. Prefiere `iw` (BSSIDs
// reales + barrido fresco) y cae a `nmcli` si `iw` no está o no devolvió antenas.
func scanWifiBSSIDs(a *Agent) []wifiAP {
	if aps := scanWifiBSSIDsIw(a); len(aps) > 0 {
		return aps
	}
	a.Logger.Debugln("scanWifiBSSIDs: iw sin resultados; se usa nmcli como fallback")
	return scanWifiBSSIDsNmcli(a)
}

// iwBSSRe captura la MAC del encabezado de cada bloque BSS: "BSS xx:xx:..(on wlan0)".
// iwSignalRe/iwFreqRe leen la señal (ya en dBm, formato que consume Google) y la
// frecuencia (→ canal). El canal es opcional para Google, pero lo derivamos igual.
var (
	iwBSSRe    = regexp.MustCompile(`^BSS[ \t]+([0-9a-fA-F]{2}(?::[0-9a-fA-F]{2}){5})`)
	iwSignalRe = regexp.MustCompile(`signal:[ \t]*(-?\d+(?:\.\d+)?)[ \t]*dBm`)
	iwFreqRe   = regexp.MustCompile(`freq:[ \t]*(\d+)`)
)

// wifiInterfaces lista las interfaces WLAN en modo managed vía `iw dev`. Ignora los
// pseudo-dispositivos P2P ("Unnamed/non-netdev interface"), que no tienen nombre.
func wifiInterfaces() []string {
	out, err := exec.Command("iw", "dev").Output()
	if err != nil {
		return nil
	}
	var ifaces []string
	re := regexp.MustCompile(`Interface[ \t]+(\S+)`)
	for _, m := range re.FindAllStringSubmatch(string(out), -1) {
		ifaces = append(ifaces, m[1])
	}
	return ifaces
}

// scanWifiBSSIDsIw dispara un barrido activo con `iw dev <if> scan` en cada interfaz
// WLAN y parsea los BSSIDs reales. Requiere root (el agente corre como root). Lista
// vacía si `iw` no está, no hay interfaz WiFi, o el escaneo falla → el llamador cae
// a nmcli. No expone las MAC en logs de nivel normal.
func scanWifiBSSIDsIw(a *Agent) []wifiAP {
	var aps []wifiAP
	for _, iface := range wifiInterfaces() {
		out, err := exec.Command("iw", "dev", iface, "scan").Output()
		if err != nil {
			a.Logger.Debugln("scanWifiBSSIDs: iw scan falló en", iface, ":", err)
			continue
		}
		aps = append(aps, parseIwScan(string(out))...)
	}
	return aps
}

// parseIwScan extrae {BSSID, señal dBm, canal} de la salida de `iw ... scan`, que
// imprime un bloque por AP encabezado por "BSS <mac>(on <if>)" seguido de líneas
// indentadas (signal, freq, SSID, ...).
func parseIwScan(out string) []wifiAP {
	var aps []wifiAP
	var cur *wifiAP
	flush := func() {
		if cur != nil {
			aps = append(aps, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(out, "\n") {
		if m := iwBSSRe.FindStringSubmatch(line); m != nil {
			flush()
			cur = &wifiAP{MacAddress: strings.ToLower(m[1]), SignalStrength: -65}
			continue
		}
		if cur == nil {
			continue
		}
		if m := iwSignalRe.FindStringSubmatch(line); m != nil {
			if f, ferr := strconv.ParseFloat(m[1], 64); ferr == nil {
				cur.SignalStrength = int(f)
			}
			continue
		}
		if cur.Channel == 0 {
			if m := iwFreqRe.FindStringSubmatch(line); m != nil {
				if freq, cerr := strconv.Atoi(m[1]); cerr == nil {
					cur.Channel = freqToChannel(freq)
				}
			}
		}
	}
	flush()
	return aps
}

// freqToChannel convierte la frecuencia central (MHz) al número de canal WiFi
// (2.4/5/6 GHz). Devuelve 0 si no cae en un rango conocido (el canal es opcional
// para Google).
func freqToChannel(freq int) int {
	switch {
	case freq == 2484:
		return 14
	case freq >= 2412 && freq <= 2472:
		return (freq - 2407) / 5
	case freq >= 5000 && freq <= 5900:
		return (freq - 5000) / 5
	case freq >= 5955 && freq <= 7115: // 6 GHz (WiFi 6E)
		return (freq - 5950) / 5
	default:
		return 0
	}
}

// scanWifiBSSIDsNmcli es el fallback: lee los AP visibles con nmcli (BSSID, señal,
// canal). Si nmcli no está o falla, devuelve lista vacía (→ degradación a IP).
//
// nmcli -t separa campos con ":" y escapa los ":" internos del BSSID como "\:".
// Se sustituye el escape por un placeholder para poder separar los 3 campos con
// seguridad (BSSID:SIGNAL:CHAN) y luego se restaura.
func scanWifiBSSIDsNmcli(a *Agent) []wifiAP {
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
