package agent

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

// Feature 023: geolocalización de activos. Cada plataforma implementa
// osLocation() con build tags (geolocation_windows.go / _darwin.go / _unix.go).
// Este archivo es común (sin build tag) y contiene el contrato, la política de
// degradación y el fallback por IP. Regla de oro: la geolocalización JAMÁS debe
// romper el check-in de inventario.

// Fuentes de ubicación. Coinciden con GeoNats.Source (CONTRACT-01) y con lo que
// la consola sabe interpretar.
const (
	SourceNative      = "native"
	SourceWiFi        = "wifi"
	SourceIP          = "ip"
	SourceDenied      = "denied"
	SourceUnavailable = "unavailable"
)

// errNativeUnavailable lo devuelven los proveedores de plataforma cuando el
// sensor nativo no está disponible (sin hardware, daemon headless, etc.).
// GetLocation lo interpreta como "degradar a IP".
var errNativeUnavailable = errors.New("native location provider unavailable")

// Location es el resultado de una captura de geolocalización.
type Location struct {
	Lat        float64
	Long       float64
	AccuracyM  int
	Source     string
	CapturedAt time.Time
}

// HasFix indica si la ubicación tiene coordenadas válidas y publicables.
func (l Location) HasFix() bool {
	return validCoords(l.Lat, l.Long) &&
		(l.Source == SourceNative || l.Source == SourceWiFi || l.Source == SourceIP)
}

// validCoords: WGS84 dentro de rango y distinto de (0,0) (Null Island = típico
// artefacto de "sin fix").
func validCoords(lat, long float64) bool {
	if lat < -90 || lat > 90 || long < -180 || long > 180 {
		return false
	}
	if lat == 0 && long == 0 {
		return false
	}
	return true
}

// geoEnv lee una variable de entorno con default.
func geoEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// GetLocation devuelve la mejor ubicación disponible siguiendo la política de
// degradación (RF-07):
//  1. Proveedor nativo del SO (o WiFi en Linux) → source native/wifi.
//  2. Nativo NO disponible (sin sensor, headless) → geolocalización por IP
//     (nivel ciudad) → source ip.
//  3. Permiso DENEGADO explícitamente → source denied, SIN caer a IP (se respeta
//     la negativa; no se inventa una ubicación).
//  4. Nada de lo anterior → source unavailable.
func (a *Agent) GetLocation() Location {
	now := time.Now().UTC()

	loc, err := osLocation(a)
	if err == nil && validCoords(loc.Lat, loc.Long) {
		loc.CapturedAt = now
		return loc
	}
	if loc.Source == SourceDenied {
		a.Logger.Debugln("GetLocation: proveedor nativo denegado por permiso/política")
		return Location{Source: SourceDenied, CapturedAt: now}
	}
	// Nativo no disponible → degradar a IP.
	if ip, ierr := a.ipGeolocation(); ierr == nil && validCoords(ip.Lat, ip.Long) {
		ip.CapturedAt = now
		return ip
	}
	a.Logger.Debugln("GetLocation: sin fix nativo ni por IP")
	return Location{Source: SourceUnavailable, CapturedAt: now}
}

// ipGeolocation resuelve una ubicación aproximada (nivel ciudad) a partir de la
// IP pública. Endpoint configurable vía OBSERVER_GEO_IP_URL (default: ipwho.is,
// gratuito, HTTPS, sin API key; devuelve latitude/longitude). Se prefiere ipwho.is
// sobre ipapi.co porque este último geolocalizaba mal ciertos rangos chilenos
// (p.ej. ENTEL a Arica en vez de Santiago). No loggea coordenadas a nivel normal.
func (a *Agent) ipGeolocation() (Location, error) {
	url := geoEnv("OBSERVER_GEO_IP_URL", "https://ipwho.is/")
	client := resty.New()
	client.SetHeader("User-Agent", a.AgentHeader)
	client.SetTimeout(8 * time.Second)
	if len(a.Proxy) > 0 {
		client.SetProxy(a.Proxy)
	}
	r, err := client.R().Get(url)
	if err != nil {
		return Location{}, err
	}
	var body struct {
		Lat  float64 `json:"latitude"`
		Long float64 `json:"longitude"`
	}
	if err := json.Unmarshal(r.Body(), &body); err != nil {
		return Location{}, err
	}
	return Location{
		Lat:       body.Lat,
		Long:      body.Long,
		AccuracyM: 20000, // nivel ciudad: precisión nominal ~20 km
		Source:    SourceIP,
	}, nil
}

// wifiAP es un punto de acceso WiFi visible, en el formato que consume tanto un
// servicio MLS/BeaconDB como Google Geolocation API. Campos homologados con Prey
// (lib/agent/providers/geo/strategies.js): macAddress + signalStrength + channel.
// El canal mejora la precisión del fix; se omite si es 0 (desconocido). No se envía
// ssid (Google lo ignora y es un dato de red sensible).
type wifiAP struct {
	MacAddress     string `json:"macAddress"`
	SignalStrength int    `json:"signalStrength"`
	Channel        int    `json:"channel,omitempty"`
}

// geoWifiBackendPath es el resolver WiFi→coordenadas del backend (Feature 023 · F4).
// El agente le manda las antenas visibles y el backend las resuelve contra Google
// con la API key viviendo SOLO en el servidor (modelo Prey): la key nunca se
// distribuye a la flota. Se llama con a.rClient (autenticado con el token del agente).
const geoWifiBackendPath = "/api/v3/geolocate/"

// prepareWifiAPs homologa el pre-proceso de Prey antes de resolver
// (strategies.js): descarta MACs del rango reservado IANA 00:00:5E (VRRP/virtuales,
// no ubicables) y ordena por señal ascendente (mismo criterio que Prey, que lo
// adoptó como mejora de precisión). Devuelve un slice nuevo; no muta la entrada.
func prepareWifiAPs(aps []wifiAP) []wifiAP {
	out := make([]wifiAP, 0, len(aps))
	for _, ap := range aps {
		if len(ap.MacAddress) >= 8 && strings.EqualFold(ap.MacAddress[:8], "00:00:5e") {
			continue
		}
		out = append(out, ap)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].SignalStrength < out[j].SignalStrength
	})
	return out
}

// resolveWifiLocation toma las antenas WiFi visibles y las resuelve a coordenadas.
// Orden de preferencia:
//  1. OBSERVER_GEO_WIFI_URL, si está seteado → endpoint MLS/BeaconDB externo (sin
//     autenticación, cliente propio). Pensado para pruebas directas contra un
//     servicio público.
//  2. Si no → el resolver del backend Observer RMM (a.rClient, autenticado), que
//     consulta a Google con la key server-side.
//
// Sin antenas visibles → no disponible; GetLocation degradará a IP.
func (a *Agent) resolveWifiLocation(aps []wifiAP) (Location, error) {
	aps = prepareWifiAPs(aps)
	if len(aps) == 0 {
		return Location{Source: SourceUnavailable}, errNativeUnavailable
	}
	if url := geoEnv("OBSERVER_GEO_WIFI_URL", ""); url != "" {
		c := resty.New()
		c.SetHeader("User-Agent", a.AgentHeader)
		c.SetTimeout(10 * time.Second)
		if len(a.Proxy) > 0 {
			c.SetProxy(a.Proxy)
		}
		return a.wifiGeolocation(c.R(), url, aps)
	}
	return a.wifiGeolocation(a.rClient.R(), geoWifiBackendPath, aps)
}

// wifiGeolocation envía las antenas al endpoint dado y parsea la respuesta. El
// cuerpo (considerIp + wifiAccessPoints) y la respuesta ({location:{lat,lng},
// accuracy}) siguen el formato de Google Geolocation API / MLS. considerIp=false:
// solo aceptamos un fix WiFi real; la degradación a IP la maneja GetLocation con
// su propio proveedor, evitando etiquetar como source=wifi un fallback por IP.
func (a *Agent) wifiGeolocation(req *resty.Request, url string, aps []wifiAP) (Location, error) {
	reqBody := map[string]interface{}{
		"considerIp":       false,
		"wifiAccessPoints": aps,
	}
	r, err := req.SetHeader("Content-Type", "application/json").SetBody(reqBody).Post(url)
	if err != nil {
		return Location{}, err
	}
	var resp struct {
		Location struct {
			Lat float64 `json:"lat"`
			Lng float64 `json:"lng"`
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
