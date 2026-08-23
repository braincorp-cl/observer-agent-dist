package agent

import (
	"bytes"
	"context"
	"os/exec"
	"sync/atomic"
	"time"
)

// Feature 041 · RF-05 (T016) · asociación a red WiFi ABIERTA para mantener
// conectividad de ubicación fuera de la geocerca.
//
// Gate (RN-06): sólo se intenta cuando el equipo está FUERA de la geocerca Y sin
// conectividad utilizable, sólo a redes ABIERTAS (sin credenciales), y saltando
// portales cautivos (T017). SIN lista negra de SSIDs (descartada 2026-08-23: el
// NFR §6 prohíbe transportar SSIDs por el config). Nunca toca credenciales
// guardadas del usuario.
//
// El backend por SO se registra en init() (patrón de baseline.go): Windows y
// Linux lo implementan; macOS NO (RF-10, no-op deliberado), así que allí
// openWifiBackendImpl queda nil y el orquestador es un no-op silencioso.
//
// Sensible por diseño: no debe habilitarse en flota sin la línea de política de
// uso aceptable + revisión legal (T022). El gate de outside_geofence lo acota —
// no es un auto-connect indiscriminado— pero la exposición MITM en redes abiertas
// existe (mitigada porque el tráfico al backend va por TLS).

// openWifiBackend es el contrato por SO. Sólo asociación: la clasificación de
// conectividad/portal es cross-platform (openwifi_captive.go).
type openWifiBackend interface {
	// escanearAbiertas devuelve los SSID de redes ABIERTAS (sin cifrado)
	// visibles. Redes con cualquier seguridad (WPA/WEP/802.1X) se excluyen: no
	// tenemos —ni queremos— credenciales.
	escanearAbiertas(a *Agent) []string
	// conectar asocia a la red abierta ssid. Error si no se pudo asociar.
	conectar(a *Agent, ssid string) error
	// desconectar deshace la asociación que hicimos (y limpia el perfil temporal
	// creado), para no dejar al equipo re-conectándose solo a una red que resultó
	// cautiva o sin salida.
	desconectar(a *Agent, ssid string)
}

// openWifiBackendImpl lo fija el init() del archivo por-SO. nil = SO sin soporte.
var openWifiBackendImpl openWifiBackend

// openWifiEnCurso evita solapar dos intentos: el intento hace IO de red (escaneo
// + asociación + sonda) y puede durar decenas de segundos; el poll no debe
// encadenarlos.
var openWifiEnCurso atomic.Bool

// intentarConectarWifiAbierta corre el gate y, si corresponde, intenta asociarse
// a una red abierta no-cautiva. Best-effort: cualquier fallo se loguea y no
// interrumpe nada. Idempotente por poll: si ya hay conectividad utilizable, sale
// sin tocar la WiFi.
//
// `habilitado` es el toggle global del servidor (T034, `CoreSettings.open_wifi_enabled`,
// ON por omisión). Es el PRIMER gate: RF-05 es parte del módulo de perdidos/robados
// (uso corporativo cubierto por el acta de entrega), y el toggle es un OPT-OUT para el
// cliente que desestima ese módulo. Con el flag en false esta función es un no-op
// inmediato en toda la flota, sin tener que desinstalar ni degradar el agente.
func (a *Agent) intentarConectarWifiAbierta(habilitado bool) {
	if !habilitado {
		return // T034: RF-05 desactivado globalmente (opt-out del cliente)
	}
	if openWifiBackendImpl == nil {
		return // macOS (RF-10) u otro SO sin backend registrado
	}
	// Gate RN-06: sólo fuera de geocerca.
	if !a.getOutsideGeofence() {
		return
	}
	if !openWifiEnCurso.CompareAndSwap(false, true) {
		a.Logger.Debugln("openwifi: un intento anterior sigue en curso, se salta")
		return
	}
	defer openWifiEnCurso.Store(false)

	// Gate RN-06: sólo sin conectividad utilizable. Si ya hay salida a Internet
	// (la red del sitio, un tethering, etc.) no se toca la WiFi.
	if sondearConectividad(a) == conectividadUtilizable {
		return
	}

	abiertas := openWifiBackendImpl.escanearAbiertas(a)
	if len(abiertas) == 0 {
		a.Logger.Debugln("openwifi: fuera de geocerca y sin conectividad, pero no hay redes abiertas visibles")
		return
	}

	for _, ssid := range abiertas {
		if err := openWifiBackendImpl.conectar(a, ssid); err != nil {
			a.Logger.Debugln("openwifi: no se pudo asociar a una red abierta:", err.Error())
			continue
		}
		switch sondearConectividad(a) {
		case conectividadUtilizable:
			a.Logger.Infoln("openwifi: asociado a una red abierta no-cautiva (conectividad de ubicación restaurada)")
			return
		case conectividadCautiva:
			// T017: portal cautivo ⇒ desconectar, NUNCA auto-autenticar.
			a.Logger.Infoln("openwifi: la red abierta tiene portal cautivo; se desconecta y NO se auto-autentica")
			openWifiBackendImpl.desconectar(a, ssid)
		default:
			a.Logger.Debugln("openwifi: la red abierta no da salida a Internet; se desconecta")
			openWifiBackendImpl.desconectar(a, ssid)
		}
	}
}

// runCapture corre un comando con timeout y devuelve stdout+stderr combinados.
// Helper local de openwifi (Windows/Linux) para no arrastrar la maquinaria de
// CMDShell (streaming, runas, NATS) que acá no hace falta.
func runCapture(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}
