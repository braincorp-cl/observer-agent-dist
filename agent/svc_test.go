package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/go-resty/resty/v2"
)

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

// TestGetCheckInConfFromAPICopiaTodosLosCampos es el test que habría atrapado el
// bug medido en terreno el 2026-08-13.
//
// `GetCheckInConfFromAPI` copiaba la respuesta del servidor campo por campo, con
// una lista de 22 asignaciones a mano. `lost_mode_webcam` —el interruptor global
// de la foto de webcam— viajaba correctamente desde el servidor, se verificó
// contra staging, pero nadie lo había agregado a esa lista. El agente lo
// descartaba al copiar y el campo valía `false` para siempre: la foto de webcam
// de la Fase 2 era un no-op de punta a punta, en las tres plataformas y con
// cualquier configuración. Encender el interruptor en la consola no hacía nada y
// no había forma de notarlo desde afuera.
//
// Lo que hace peligrosa a esa clase de error es que es SILENCIOSA por
// construcción: agregar un campo al struct y olvidarlo en la copia compila, pasa
// todas las pruebas y no deja ni una línea de log. El dato llega en el JSON y se
// tira.
//
// Por eso este test no comprueba `LostModeWebcam`: comprueba, por reflexión, que
// NINGÚN campo exportado se quede en su valor cero cuando el servidor los mandó
// todos con valor. Un campo nuevo queda protegido el día que se agrega al struct,
// sin que nadie tenga que acordarse de venir a escribir su caso acá.
func TestGetCheckInConfFromAPICopiaTodosLosCampos(t *testing.T) {
	tipo := reflect.TypeOf(AgentCheckInConfig{})

	// El JSON se arma desde las etiquetas del propio struct, para que un campo
	// nuevo entre solo. Los valores son arbitrarios; lo único que importa es que
	// ninguno sea el valor cero de su tipo.
	cuerpo := map[string]any{}
	for i := 0; i < tipo.NumField(); i++ {
		campo := tipo.Field(i)
		etiqueta := campo.Tag.Get("json")
		if etiqueta == "" || etiqueta == "-" {
			// `fromServer` no viaja por la red: lo pone la propia función.
			continue
		}
		switch campo.Type.Kind() {
		case reflect.Bool:
			cuerpo[etiqueta] = true
		case reflect.Int:
			cuerpo[etiqueta] = 7
		case reflect.String:
			cuerpo[etiqueta] = "algo"
		default:
			t.Fatalf("campo %s de tipo %s no contemplado en este test", campo.Name, campo.Type)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cuerpo)
	}))
	defer srv.Close()

	a := &Agent{Logger: loggerMudo(), AgentID: "abc123", rClient: resty.New().SetBaseURL(srv.URL)}

	conf := a.GetCheckInConfFromAPI()

	if !conf.fromServer {
		t.Fatal("fromServer quedó en false con el servidor respondiendo: el repliegue apagaría el modo perdido de un caso en curso")
	}

	valor := reflect.ValueOf(conf)
	for i := 0; i < tipo.NumField(); i++ {
		campo := tipo.Field(i)
		if campo.Tag.Get("json") == "" || campo.Tag.Get("json") == "-" {
			continue
		}
		if valor.Field(i).IsZero() {
			t.Errorf("el campo %s (json:%q) llegó del servidor con valor y la copia lo descartó",
				campo.Name, campo.Tag.Get("json"))
		}
	}
}
