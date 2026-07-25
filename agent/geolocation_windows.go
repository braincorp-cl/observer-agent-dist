//go:build windows

package agent

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Feature 023 · provider Windows (F4).
//
// La API nativa WinRT Windows.Devices.Geolocation.Geolocator NO funciona desde el
// servicio del agente: corriendo como SYSTEM / sesión 0 la operación async nunca
// es atendida (confirmado en Win10 22H2, Win11 23H2/24H2 y hardware físico), y la
// doc de Microsoft exige app en primer plano con hilo de UI. Descartada.
//
// Se adopta el enfoque de Prey: escanear las antenas WiFi visibles con
// `netsh wlan show networks mode=bssid` (sin UI, sin WinRT) y resolverlas vía
// resolveWifiLocation() (código común) contra el backend, que consulta a Google
// con la key server-side. En Windows 11 24H2 / Server 2025 el escaneo de BSSIDs
// exige que la ubicación del sistema esté activada (se pre-configura por GPO); en
// Windows 10 y Server 2019/2022 no hay esa restricción. Sin antenas → GetLocation
// degrada a IP.

// netsh imprime, por cada red, un bloque por BSSID: "BSSID n : xx:xx:.." seguido de
// "Signal : NN%", "Channel : N", etc. El literal BSSID es estable entre locales; la
// señal la detectamos por el símbolo "%" (independiente del idioma). El canal por su
// etiqueta en los locales relevantes (EN "Channel" / ES "Canal"); si no se detecta,
// queda 0 y se omite del request.
var winBSSIDRe = regexp.MustCompile(`(?i)BSSID[ \t]*\d*[ \t]*:[ \t]*([0-9A-Fa-f]{2}(?::[0-9A-Fa-f]{2}){5})`)
var winPctRe = regexp.MustCompile(`(\d{1,3})\s*%`)
var winChannelRe = regexp.MustCompile(`(?i)(?:channel|canal)\D+(\d+)`)

func osLocation(a *Agent) (Location, error) {
	aps := scanWifiBSSIDs(a)
	// Gap 3 (permiso activo): en Windows 11 24H2 / Server 2025 el escaneo de BSSIDs
	// exige la ubicación del sistema ACTIVADA. Si el escaneo vino vacío y el
	// ConsentStore dice Deny, es ese gate (no "sin WiFi"): lo reportamos como
	// denied —visible en la consola y accionable por GPO— en vez de degradar mudo a
	// IP. Si hubo antenas (Win10 / Server 2019-2022 no gatean el escaneo), o si no
	// se puede afirmar la negativa, resolvemos/degradamos normal.
	if len(aps) == 0 && locationConsentDenied() {
		a.Logger.Infoln("geo: ubicación del sistema desactivada (ConsentStore=Deny); " +
			"el escaneo WiFi no es posible. Actívala por GPO para geolocalizar por WiFi.")
		return Location{Source: SourceDenied}, nil
	}
	return a.resolveWifiLocation(aps)
}

// locationConsentDenied lee el interruptor de ubicación del dispositivo
// (HKLM\...\CapabilityAccessManager\ConsentStore\location, valor "Value"). Devuelve
// true SOLO si está explícitamente en "Deny". Si no se puede leer (clave ausente,
// build antiguo, sin permiso), devuelve false: ante la duda NO bloqueamos —dejamos
// que GetLocation degrade a IP— para no perder el fix por una lectura fallida.
func locationConsentDenied() bool {
	k, err := registry.OpenKey(
		registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\location`,
		registry.QUERY_VALUE,
	)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringValue("Value")
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(v), "Deny")
}

// scanWifiBSSIDs lista las antenas visibles con netsh, acumulando por cada BSSID su
// señal (%→dBm aprox.) y su canal. Lista vacía si netsh falla o no hay WiFi (→
// degradación a IP). No expone las MAC en logs de nivel normal.
func scanWifiBSSIDs(a *Agent) []wifiAP {
	out, err := exec.Command("netsh", "wlan", "show", "networks", "mode=bssid").Output()
	if err != nil {
		a.Logger.Debugln("scanWifiBSSIDs: netsh no disponible:", err)
		return nil
	}

	var aps []wifiAP
	var cur *wifiAP
	haveSignal := false
	flush := func() {
		if cur != nil {
			aps = append(aps, *cur)
		}
		cur = nil
		haveSignal = false
	}

	for _, line := range strings.Split(string(out), "\n") {
		if m := winBSSIDRe.FindStringSubmatch(line); m != nil {
			flush()
			cur = &wifiAP{MacAddress: strings.ToLower(m[1]), SignalStrength: -65}
			continue
		}
		if cur == nil {
			continue
		}
		if !haveSignal {
			if s := winPctRe.FindStringSubmatch(line); s != nil {
				if pct, perr := strconv.Atoi(s[1]); perr == nil && pct > 0 {
					cur.SignalStrength = pct/2 - 100 // 100%→-50dBm, 0%→-100dBm (aprox.)
				}
				haveSignal = true
				continue
			}
		}
		if cur.Channel == 0 {
			if c := winChannelRe.FindStringSubmatch(line); c != nil {
				if ch, cerr := strconv.Atoi(c[1]); cerr == nil {
					cur.Channel = ch
				}
			}
		}
	}
	flush()
	return aps
}
