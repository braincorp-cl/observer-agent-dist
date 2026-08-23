package agent

import (
	"bytes"
	"context"
	"os/exec"
	"sync"
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

// Feature 041 · T036 · capa C · dead-man de conectividad (arranque en frío).
//
// El openwifi de geocerca (RN-06) depende de que el SERVIDOR nos marque
// `outside_geofence` — y para eso el equipo necesita red. Un equipo salido o
// robado a una zona sin red conocida nunca podría recibir esa marca, así que
// nunca dispararía el openwifi: no bootstrapea el contacto. El dead-man cierra ese
// hueco: si acumulamos suficiente tiempo SIN salida a Internet, intentamos WiFi
// abierta por nuestra cuenta, sin esperar la marca del server.
//
// NO agrega geocerca local (descartado por alcance): no razona sobre POSICIÓN,
// sólo sobre AUSENCIA DE RED sostenida. Gobernado por el mismo toggle `open_wifi`
// (querer reconexión anti-robo incluye el caso "el server no pudo marcarme").

// deadManConectividad es cuánto tiempo sin salida a Internet debe pasar antes de
// que la capa C intente WiFi abierta sola. Constante por ahora (promovible a
// config server si hace falta afinar por flota); el on/off ya lo da el toggle.
const deadManConectividad = 30 * time.Minute

// Estado del dead-man: marca del último momento con conectividad utilizable. Se
// resetea en cada sonda exitosa y se consulta cuando la sonda falla.
var (
	ultimaConectividadMu   sync.Mutex
	ultimaConectividadSet  bool
	ultimaConectividadUnix int64
)

// marcarConectividadUtilizable registra que ahora hay salida a Internet: resetea
// el reloj del dead-man.
func (a *Agent) marcarConectividadUtilizable() {
	ultimaConectividadMu.Lock()
	ultimaConectividadUnix = time.Now().Unix()
	ultimaConectividadSet = true
	ultimaConectividadMu.Unlock()
}

// deadManConectividadDisparado indica si se acumuló deadManConectividad sin una
// sola sonda con salida a Internet. La PRIMERA observación sin conectividad fija
// la línea base y devuelve false: se exige ausencia SOSTENIDA, no un solo fallo
// (no dispara por un hipo de red ni en el primer poll de un arranque offline).
func (a *Agent) deadManConectividadDisparado() bool {
	ultimaConectividadMu.Lock()
	defer ultimaConectividadMu.Unlock()
	now := time.Now().Unix()
	if !ultimaConectividadSet {
		ultimaConectividadSet = true
		ultimaConectividadUnix = now
		return false
	}
	return time.Duration(now-ultimaConectividadUnix)*time.Second >= deadManConectividad
}

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
	if !openWifiEnCurso.CompareAndSwap(false, true) {
		a.Logger.Debugln("openwifi: un intento anterior sigue en curso, se salta")
		return
	}
	defer openWifiEnCurso.Store(false)

	// La sonda va SIEMPRE (incluso dentro de la geocerca): con salida a Internet se
	// registra el contacto —resetea el reloj del dead-man de capa C (T036)— y no se
	// toca la WiFi. Si ya hay salida (red del sitio, tethering, etc.) no hay nada
	// que reconectar.
	if sondearConectividad(a) == conectividadUtilizable {
		a.marcarConectividadUtilizable()
		return
	}

	// Sin conectividad utilizable. Dos disparadores INDEPENDIENTES:
	//   - Geocerca (RN-06): el server nos marcó fuera del sitio.
	//   - Dead-man (T036 · capa C): ausencia de red sostenida (arranque en frío),
	//     aunque el server nunca haya podido marcarnos fuera.
	fueraGeocerca := a.getOutsideGeofence()
	deadMan := a.deadManConectividadDisparado()
	if !fueraGeocerca && !deadMan {
		return
	}
	if deadMan && !fueraGeocerca {
		a.Logger.Infoln("openwifi: dead-man de conectividad disparado (sin salida a Internet sostenida); se intenta WiFi abierta pese a no haber marca de geocerca (arranque en frío)")
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
