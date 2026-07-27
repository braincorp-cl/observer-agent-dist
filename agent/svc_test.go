package agent

import "testing"

// TestGeoTickIntervalPiso congela el piso de la captura de ubicación. Es un límite
// de privacidad, no de rendimiento: sin él, un `checkin_geo` bajo —puesto a mano o
// por un error de tipeo en el servidor— convertiría la flota en un rastreador.
func TestGeoTickIntervalPiso(t *testing.T) {
	casos := []struct {
		nombre    string
		desdeConf int
		quiero    int
	}{
		{"cero (campo ausente en el JSON) sube al piso", 0, geoMinInterval},
		{"negativo sube al piso", -1, geoMinInterval},
		{"justo bajo el piso sube al piso", geoMinInterval - 1, geoMinInterval},
		{"el piso exacto se respeta", geoMinInterval, geoMinInterval},
		{"por encima del piso se respeta tal cual", 1800, 1800},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := geoTickInterval(c.desdeConf); got != c.quiero {
				t.Errorf("geoTickInterval(%d) = %d, se esperaba %d", c.desdeConf, got, c.quiero)
			}
		})
	}
}

// TestRepliegueApagaLaGeoPeroNoLaEntierra es el test que habría atrapado el bug.
//
// El repliegue se usa cuando el agente no pudo leer la config del servidor, que en
// un portátil es de lo más común: arranca antes de que haya DNS. Medido en un Mac el
// 2026-07-27: 2 h 30 min sin un solo punto de ubicación, con el interruptor
// encendido en el servidor todo el tiempo, porque el ticker se detenía para siempre.
//
// Las dos mitades importan. Que la geo venga **apagada** es correcto: es una feature
// sensible y sin respuesta del servidor no se sabe qué quiere el operador. Que el
// intervalo sea **corto** es lo que garantiza que el agente se dé cuenta pronto de
// la config real, en vez de quedarse media hora a ciegas.
func TestRepliegueApagaLaGeoPeroNoLaEntierra(t *testing.T) {
	f := checkInConfFallback()

	if f.GeoEnabled {
		t.Error("el repliegue no debe encender la geo: sin config del servidor no se " +
			"sabe si el operador la quiere")
	}
	if f.GeoForceOn {
		t.Error("el repliegue no debe encender el force-on del sensor")
	}

	// El número exacto no importa; que sea pronto, sí. Media hora a ciegas fue el bug.
	const techoDeCeguera = 10 * 60
	if got := geoTickInterval(f.Geo); got > techoDeCeguera {
		t.Errorf("el intervalo del repliegue es %d s: demasiado para descubrir la config "+
			"real (techo %d s). Con el ticker re-evaluando en cada tick, un valor largo "+
			"es tiempo ciego y nada más", got, techoDeCeguera)
	}

	// Y el resto de los check-ins tiene que seguir vivo: la lección del bug es que el
	// repliegue degradaba UNA feature a cero mientras las demás seguían andando, y esa
	// asimetría es lo que lo hacía invisible.
	for _, c := range []struct {
		nombre string
		valor  int
	}{
		{"Hello", f.Hello}, {"AgentInfo", f.AgentInfo}, {"WinSvc", f.WinSvc},
		{"PubIP", f.PubIP}, {"Disks", f.Disks}, {"SW", f.SW}, {"WMI", f.WMI},
		{"SyncMesh", f.SyncMesh}, {"Geo", f.Geo},
	} {
		if c.valor <= 0 {
			t.Errorf("el repliegue dejó %s en %d: un intervalo no positivo deja ese "+
				"check-in sin cadencia definida", c.nombre, c.valor)
		}
	}
}
