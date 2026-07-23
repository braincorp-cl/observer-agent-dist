package agent

import (
	"encoding/json"
	"errors"
	"os"
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
// IP pública. Endpoint configurable vía OBSERVER_GEO_IP_URL (default: ipapi.co,
// gratuito, HTTPS, sin API key). No loggea coordenadas a nivel normal.
func (a *Agent) ipGeolocation() (Location, error) {
	url := geoEnv("OBSERVER_GEO_IP_URL", "https://ipapi.co/json/")
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
