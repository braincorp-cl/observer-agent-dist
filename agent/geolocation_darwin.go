//go:build darwin && cgo

package agent

/*
#cgo LDFLAGS: -framework CoreLocation -framework Foundation
#include <stdlib.h>
#include "corelocation_darwin.h"
*/
import "C"

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Feature 025 · provider macOS (CoreLocation).
//
// Arquitectura "Camino A′", derivada del spike Fase 0 (ver investigation.md de
// la feature 025). Tres hechos la determinan, los tres validados en un Mac real
// (macOS 26.5.2, SIP enabled):
//
//  1. CoreLocation NO atiende a la sesión 0. Un LaunchDaemon root queda en
//     notDetermined para siempre —probado sin entrada, con entrada `root:`
//     sembrada a mano y con isSystemService=true—. Es el mismo muro que Windows
//     con WinRT desde SYSTEM: modelo de plataforma, no un defecto corregible.
//  2. Basta correr COMO EL USUARIO; no hace falta sesión gráfica. El fix salió
//     desde una sesión SSH pura con el uid del usuario. Por eso el daemon se
//     re-ejecuta a sí mismo con `-m geoprobe` vía `launchctl asuser` en vez de
//     instalar un LaunchAgent aparte.
//  3. No existe prompt de permiso para un binario CLI. La autorización se
//     pre-siembra en /var/db/locationd/clients.plist (SIP no protege ese
//     archivo), y eso queda gateado por el flag global geo_force_location_on,
//     igual que el force-on de Windows/Linux.
//
// Sin usuario en consola (Mac headless) se degrada a IP, que es el
// comportamiento que ya existía. La geolocalización JAMÁS rompe el check-in.

const (
	// Plazo que espera CoreLocation dentro del proceso-sonda.
	geoProbeTimeout = 20 * time.Second
	// Presupuesto total del daemon para el spawn + la sonda.
	geoProbeBudget = 45 * time.Second
	// Espera tras reiniciar locationd para que reconstruya su estado.
	geoLocationdSettle = 3 * time.Second

	geoClientsPlist = "/var/db/locationd/clients.plist"
	// Debe coincidir con CFBundleIdentifier de agent/Info.darwin.plist, que se
	// embebe en el Mach-O (__TEXT,__info_plist). Es la clave con la que
	// locationd registra al cliente.
	geoBundleID = "cl.observer.agent"
)

// CLAuthorizationStatus (CoreLocation).
const (
	clAuthNotDetermined = 0
	clAuthRestricted    = 1
	clAuthDenied        = 2
)

func osLocation(a *Agent) (Location, error) {
	// Ya corremos como usuario (modo geoprobe, o el agente lanzado a mano):
	// CoreLocation directo, sin dar más vueltas.
	if os.Geteuid() != 0 {
		return coreLocationFix(geoProbeTimeout)
	}

	uid, username, err := consoleUser()
	if err != nil {
		a.Logger.Debugln("osLocation(darwin): sin usuario en consola; se degradará a IP:", err)
		return Location{Source: SourceUnavailable}, errNativeUnavailable
	}

	loc, err := runGeoProbeAsUser(a, uid, username)
	if err == nil && validCoords(loc.Lat, loc.Long) {
		return loc, nil
	}
	// Negativa explícita del SO: se respeta, no se inventa una ubicación por IP.
	if loc.Source == SourceDenied {
		return loc, nil
	}
	if !a.GeoForceOn {
		a.Logger.Debugln("osLocation(darwin): sin fix y geo_force_location_on apagado; se degradará a IP")
		return Location{Source: SourceUnavailable}, errNativeUnavailable
	}
	// Un solo reintento, y solo si la siembra cambió algo (idempotencia): así no
	// reiniciamos locationd en cada tick.
	if !seedLocationAuthorization(a) {
		return Location{Source: SourceUnavailable}, errNativeUnavailable
	}
	a.Logger.Debugln("osLocation(darwin): autorización sembrada; reintentando la sonda")
	return runGeoProbeAsUser(a, uid, username)
}

// coreLocationFix pide una ubicación única al SO. Solo tiene sentido en el
// contexto del usuario; desde sesión 0 devuelve timeout (ver nota de arriba).
func coreLocationFix(timeout time.Duration) (Location, error) {
	var lat, lng, acc C.double
	var auth C.int

	rc := int(C.observer_corelocation_fix(
		C.double(timeout.Seconds()), &lat, &lng, &acc, &auth))

	switch rc {
	case C.OBSERVER_CL_OK:
		accuracy := 0
		if a := float64(acc); a > 0 {
			accuracy = int(math.Round(a))
		}
		return Location{
			Lat:       float64(lat),
			Long:      float64(lng),
			AccuracyM: accuracy,
			Source:    SourceNative,
		}, nil
	case C.OBSERVER_CL_ERROR, C.OBSERVER_CL_DISABLED:
		// Igual que en Windows: solo se marca "denegado" cuando el SO lo AFIRMA.
		// notDetermined (el caso de "aún sin autorizar") degrada a IP.
		if s := int(auth); s == clAuthDenied || s == clAuthRestricted {
			return Location{Source: SourceDenied}, nil
		}
	}
	return Location{Source: SourceUnavailable}, errNativeUnavailable
}

// RunGeoProbe es el modo `-m geoprobe`: corre en el contexto del usuario,
// consulta CoreLocation e imprime UNA línea que el daemon padre parsea. No
// escribe logs a stdout para no contaminar la salida.
func (a *Agent) RunGeoProbe() {
	loc, err := coreLocationFix(geoProbeTimeout)
	switch {
	case err == nil && validCoords(loc.Lat, loc.Long):
		fmt.Printf("FIX %.7f %.7f %d\n", loc.Lat, loc.Long, loc.AccuracyM)
	case loc.Source == SourceDenied:
		fmt.Println("DENIED")
	default:
		fmt.Println("NOFIX")
	}
}

// consoleUser devuelve uid y nombre del usuario con sesión en consola. Devuelve
// error cuando no hay nadie logueado: en el arranque o en un Mac headless
// /dev/console pertenece a root o a una cuenta de sistema (prefijo "_").
func consoleUser() (string, string, error) {
	out, err := exec.Command("/usr/bin/stat", "-f", "%u %Su", "/dev/console").Output()
	if err != nil {
		return "", "", err
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) != 2 {
		return "", "", fmt.Errorf("salida inesperada de stat: %q", string(out))
	}
	uid, name := fields[0], fields[1]
	if uid == "0" || name == "root" || strings.HasPrefix(name, "_") {
		return "", "", fmt.Errorf("sin sesión de usuario (console=%s)", name)
	}
	return uid, name, nil
}

// runGeoProbeAsUser re-ejecuta el propio binario en el contexto del usuario de
// consola. `launchctl asuser` mete el proceso en el dominio de sesión del
// usuario, que es lo que locationd exige para atenderlo.
func runGeoProbeAsUser(a *Agent, uid, username string) (Location, error) {
	exe, err := os.Executable()
	if err != nil {
		return Location{Source: SourceUnavailable}, errNativeUnavailable
	}

	ctx, cancel := context.WithTimeout(context.Background(), geoProbeBudget)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/launchctl", "asuser", uid,
		"/usr/bin/sudo", "-u", username, exe, "-m", "geoprobe")
	out, err := cmd.Output()
	if err != nil {
		a.Logger.Debugln("runGeoProbeAsUser: la sonda falló:", err)
		return Location{Source: SourceUnavailable}, errNativeUnavailable
	}
	return parseGeoProbe(string(out))
}

// parseGeoProbe interpreta la línea emitida por RunGeoProbe.
func parseGeoProbe(out string) (Location, error) {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "DENIED":
			return Location{Source: SourceDenied}, nil
		case "FIX":
			if len(fields) < 4 {
				continue
			}
			lat, err1 := strconv.ParseFloat(fields[1], 64)
			lng, err2 := strconv.ParseFloat(fields[2], 64)
			acc, err3 := strconv.Atoi(fields[3])
			if err1 != nil || err2 != nil || err3 != nil || !validCoords(lat, lng) {
				continue
			}
			return Location{Lat: lat, Long: lng, AccuracyM: acc, Source: SourceNative}, nil
		}
	}
	return Location{Source: SourceUnavailable}, errNativeUnavailable
}

// seedLocationAuthorization concede el permiso de ubicación al agente escribiendo
// en clients.plist. Devuelve true SOLO si cambió algo (y por lo tanto reinició
// locationd); de lo contrario el llamador no debe reintentar.
//
// ⚠️ Esto modifica configuración del SO del endpoint, por eso cuelga del flag
// global geo_force_location_on. Además NO es API pública de Apple: si un macOS
// futuro protege el archivo, todo esto falla en silencio y se degrada a IP.
func seedLocationAuthorization(a *Agent) bool {
	xmlDoc, err := exec.Command("/usr/bin/plutil", "-convert", "xml1", "-o", "-", geoClientsPlist).Output()
	if err != nil {
		a.Logger.Debugln("seedLocationAuthorization: no se pudo leer clients.plist:", err)
		return false
	}

	patched, changed := authorizeClients(string(xmlDoc), geoBundleID)
	if changed == 0 {
		// O ya estaba autorizado, o locationd todavía no registró al cliente
		// (la primera corrida de la sonda es la que crea la entrada).
		a.Logger.Debugln("seedLocationAuthorization: nada que cambiar en clients.plist")
		return false
	}

	tmp, err := os.CreateTemp("", "observer-locationd-*.plist")
	if err != nil {
		a.Logger.Debugln("seedLocationAuthorization: no se pudo crear temporal:", err)
		return false
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(patched); err != nil {
		tmp.Close()
		a.Logger.Debugln("seedLocationAuthorization: no se pudo escribir el temporal:", err)
		return false
	}
	tmp.Close()

	// Red de seguridad: si la inserción produjo un plist inválido, se aborta
	// antes de tocar el archivo del sistema.
	if err := exec.Command("/usr/bin/plutil", "-lint", tmpName).Run(); err != nil {
		a.Logger.Debugln("seedLocationAuthorization: el plist resultante no valida; se aborta:", err)
		return false
	}
	if err := exec.Command("/usr/bin/plutil", "-convert", "binary1", "-o", geoClientsPlist, tmpName).Run(); err != nil {
		a.Logger.Debugln("seedLocationAuthorization: no se pudo escribir clients.plist:", err)
		return false
	}

	_ = exec.Command("/usr/sbin/chown", "_locationd:_locationd", geoClientsPlist).Run()
	if err := exec.Command("/usr/bin/killall", "locationd").Run(); err != nil {
		a.Logger.Debugln("seedLocationAuthorization: no se pudo reiniciar locationd:", err)
	}
	time.Sleep(geoLocationdSettle)

	a.Logger.Debugf("seedLocationAuthorization: %d entrada(s) autorizada(s) en clients.plist\n", changed)
	return true
}
