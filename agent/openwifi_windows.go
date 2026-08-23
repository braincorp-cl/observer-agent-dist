//go:build windows

package agent

import (
	"bytes"
	"encoding/xml"
	"os"
	"regexp"
	"strings"
	"time"
)

// Feature 041 · RF-05 (T016) · backend Windows de asociación a red abierta.
//
// Consistente con geolocation_windows.go: netsh, sin UI ni WinRT (que no es fiable
// desde SYSTEM/sesión 0). El agente corre como SYSTEM, así que los perfiles se
// agregan user=all.
//
// 🟡 Lacuna de implementación (no de alcance): el parseo de `netsh wlan show
// networks` es sensible al locale en las ETIQUETAS. Por eso NO se lee la etiqueta
// "Authentication/Encryption" sino el VALOR de cifrado contra un set multi-idioma
// ("None/Ninguno/Nenhuma/..."), que es el marcador limpio de "red abierta" y de
// paso excluye WEP (cifrado presente aunque la auth sea Open). El literal "SSID N :"
// sí es estable entre locales. Se confirma en hardware real (mitigado: si el parseo
// falla, escanearAbiertas devuelve vacío y el orquestador es no-op).

func init() { openWifiBackendImpl = winOpenWifi{} }

type winOpenWifi struct{}

// ssidLineRe captura el nombre en "SSID 1 : MiRed". Ancla al inicio para no cazar
// las líneas "BSSID 1 : ..".
var ssidLineRe = regexp.MustCompile(`(?i)^\s*SSID\s+\d+\s*:\s*(.*)$`)

// cifradoAbierto: valores de "Encryption/Cifrado" que significan SIN cifrado, en
// los locales relevantes. Un valor fuera de este set (CCMP, TKIP, WEP, ...) marca
// la red como cerrada y se descarta.
var cifradoAbierto = map[string]bool{
	"none": true, "ninguno": true, "ninguna": true,
	"nenhum": true, "nenhuma": true, "aucun": true, "aucune": true,
}

func (winOpenWifi) escanearAbiertas(a *Agent) []string {
	out, err := runCapture(15*time.Second, "netsh", "wlan", "show", "networks", "mode=bssid")
	if err != nil {
		a.Logger.Debugln("openwifi: netsh show networks falló:", err.Error())
		return nil
	}

	var abiertas []string
	var actual string
	var actualAbierta bool
	flush := func() {
		if actual != "" && actualAbierta {
			abiertas = append(abiertas, actual)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if m := ssidLineRe.FindStringSubmatch(line); m != nil {
			flush()
			actual = strings.TrimSpace(m[1])
			actualAbierta = false
			continue
		}
		if actual == "" {
			continue
		}
		if i := strings.Index(line, ":"); i >= 0 {
			val := strings.ToLower(strings.TrimSpace(line[i+1:]))
			if cifradoAbierto[val] {
				actualAbierta = true
			}
		}
	}
	flush()
	return abiertas
}

// perfilAbiertoXML es el perfil WLAN mínimo para una red abierta (open/none). El
// SSID se escapa como texto XML para no romper el documento.
func perfilAbiertoXML(ssid string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(ssid))
	e := b.String()
	return `<?xml version="1.0"?>
<WLANProfile xmlns="http://www.microsoft.com/networking/WLAN/profile/v1">
  <name>` + e + `</name>
  <SSIDConfig><SSID><name>` + e + `</name></SSID></SSIDConfig>
  <connectionType>ESS</connectionType>
  <connectionMode>manual</connectionMode>
  <MSM><security>
    <authEncryption><authentication>open</authentication><encryption>none</encryption><useOneX>false</useOneX></authEncryption>
  </security></MSM>
</WLANProfile>`
}

func (winOpenWifi) conectar(a *Agent, ssid string) error {
	// netsh necesita un perfil para conectar. Se crea uno temporal user=all,
	// se conecta, y se borra el archivo (el perfil queda registrado hasta el
	// desconectar/limpieza).
	f, err := os.CreateTemp("", "ormm-openwifi-*.xml")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.WriteString(perfilAbiertoXML(ssid)); err != nil {
		f.Close()
		return err
	}
	f.Close()

	if _, err := runCapture(10*time.Second, "netsh", "wlan", "add", "profile",
		"filename="+name, "user=all"); err != nil {
		return err
	}
	if _, err := runCapture(25*time.Second, "netsh", "wlan", "connect",
		"name="+ssid, "ssid="+ssid); err != nil {
		return err
	}
	// La asociación de netsh es asíncrona; se da un margen antes de que el
	// orquestador sondee la conectividad.
	time.Sleep(4 * time.Second)
	return nil
}

func (winOpenWifi) desconectar(a *Agent, ssid string) {
	_, _ = runCapture(10*time.Second, "netsh", "wlan", "disconnect")
	// Se borra el perfil temporal para que Windows no se re-asocie solo a una red
	// que resultó cautiva o sin salida.
	_, _ = runCapture(10*time.Second, "netsh", "wlan", "delete", "profile", "name="+ssid)
}
