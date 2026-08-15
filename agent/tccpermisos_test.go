package agent

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Feature 030 · Fase 2 · T016b/T016c.
//
// Lo que se prueba acá es la parte que decide A QUIÉN se molesta y CUÁNDO, que
// es donde están los errores caros de esta tarea: un diálogo de más en el
// momento equivocado (un equipo robado) delata el caso entero, y uno de menos
// deja un Mac sin poder aportar evidencia sin que nadie se entere.

// TestIdentidadBinarioCambiaConElBinario es el corazón de T016c: el disparador
// del re-pedido no es "hubo un update" sino "el binario es otro", que es
// exactamente el evento que re-ancla (o rompe) las filas de TCC.
func TestIdentidadBinarioCambiaConElBinario(t *testing.T) {
	base := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)

	mismo := identidadBinario("2.15.19", 12345, base)
	if mismo != identidadBinario("2.15.19", 12345, base) {
		t.Fatal("la misma versión, tamaño y mtime deben dar la misma identidad")
	}

	casos := map[string]string{
		"otra versión": identidadBinario("2.15.20", 12345, base),
		"otro tamaño":  identidadBinario("2.15.19", 12346, base),
		"otro mtime":   identidadBinario("2.15.19", 12345, base.Add(time.Second)),
	}

	for nombre, ident := range casos {
		if ident == mismo {
			t.Errorf("%s debería producir otra identidad: el update reemplaza el binario "+
				"y con él el cdhash contra el que TCC compara", nombre)
		}
	}
}

// TestAlinearEstadoTCCOlvidaAlCambiarElBinario: lo aprendido de un binario no
// vale para el siguiente. Si se conservara, un agente actualizado creería que
// tiene permisos que TCC acaba de denegar y no volvería a pedir nunca.
func TestAlinearEstadoTCCOlvidaAlCambiarElBinario(t *testing.T) {
	previo := estadoTCC{
		Identidad: "2.15.18|100|1",
		Servicios: map[string]intentoTCC{
			tccServicioCamara: {Concedido: true, Rondas: 1, Aviso: true},
		},
	}

	st, cambio := alinearEstadoTCC(previo, "2.15.19|200|2")
	if !cambio {
		t.Error("cambiar de identidad debe reportarse: es el disparador de T016c")
	}
	if len(st.Servicios) != 0 {
		t.Errorf("el estado del binario anterior debe descartarse, quedó: %+v", st.Servicios)
	}

	igual, cambio := alinearEstadoTCC(st, "2.15.19|200|2")
	if cambio {
		t.Error("la misma identidad no es un cambio")
	}
	if igual.Servicios == nil {
		t.Error("el mapa debe quedar utilizable, no nil")
	}
}

// TestAlinearEstadoTCCPrimeraVezNoEsCambio: en una instalación nueva no hay
// identidad previa, así que no corresponde reportar "el binario cambió" — no
// hay ningún permiso que se haya podido perder todavía.
func TestAlinearEstadoTCCPrimeraVezNoEsCambio(t *testing.T) {
	if _, cambio := alinearEstadoTCC(estadoTCC{}, "2.15.19|200|2"); cambio {
		t.Error("sin estado previo no hay update del que venir")
	}
}

func TestDebeCorrerRondaTCC(t *testing.T) {
	ahora := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	casos := []struct {
		nombre   string
		reg      intentoTCC
		forzar   bool
		esperado bool
	}{
		{"nunca se pidió", intentoTCC{}, false, true},
		{"ya concedido", intentoTCC{Concedido: true, Rondas: 1}, false, false},
		{"ya concedido, aunque se fuerce", intentoTCC{Concedido: true}, true, false},
		{"tope de rondas alcanzado", intentoTCC{Rondas: tccMaxRondasPorIdentidad, Ultima: ahora.Add(-72 * time.Hour)}, false, false},
		{"tope alcanzado pero hay alguien esperando", intentoTCC{Rondas: tccMaxRondasPorIdentidad}, true, true},
		{"se pidió hace un rato", intentoTCC{Rondas: 1, Ultima: ahora.Add(-time.Hour)}, false, false},
		{"se pidió hace más de un día", intentoTCC{Rondas: 1, Ultima: ahora.Add(-25 * time.Hour)}, false, true},
	}

	for _, c := range casos {
		if got := debeCorrerRondaTCC(c.reg, ahora, c.forzar); got != c.esperado {
			t.Errorf("%s: debeCorrerRondaTCC = %v, se esperaba %v", c.nombre, got, c.esperado)
		}
	}
}

// TestConcedidoNoSePideNiForzando protege la propiedad que evita el acoso: una
// vez que el permiso está, no hay motivo para volver a mostrar un diálogo, y
// forzar la ronda (instalación o pedido manual) no es motivo suficiente.
func TestConcedidoNoSePideNiForzando(t *testing.T) {
	if debeCorrerRondaTCC(intentoTCC{Concedido: true}, time.Now(), true) {
		t.Error("un permiso concedido no se vuelve a pedir")
	}
}

// TestMensajeAvisoTCCNoMandaAAjustesComoSiFuncionara: está MEDIDO que apagar y
// encender el interruptor de Ajustes NO re-ancla el requisito de TCC —reescribe
// la fila conservando el cdhash viejo—, así que un mensaje que le pida eso a la
// persona la manda a hacer algo que no sirve y cierra el caso en falso.
func TestMensajeAvisoTCCNoMandaAAjustesComoSiFuncionara(t *testing.T) {
	_, cuerpo := mensajeAvisoTCC([]string{tccServicioCamara, tccServicioPantalla})

	for _, prohibido := range []string{"Ajustes", "interruptor", "active", "actívelo"} {
		if strings.Contains(strings.ToLower(cuerpo), strings.ToLower(prohibido)) {
			t.Errorf("el aviso menciona %q: el interruptor de Ajustes no re-ancla el csreq "+
				"y prometerlo cierra el problema en falso", prohibido)
		}
	}

	if !strings.Contains(cuerpo, "Cámara") || !strings.Contains(cuerpo, "Grabación de pantalla") {
		t.Errorf("el aviso debe nombrar los permisos que faltan, quedó: %q", cuerpo)
	}
}

// TestMensajeAvisoTCCDiceQueSePierde: un aviso que sólo pide un permiso se lee
// como una molestia del sistema y se cierra sin leer. El que dice qué deja de
// funcionar es el que alguien reporta a soporte.
func TestMensajeAvisoTCCDiceQueSePierde(t *testing.T) {
	_, cuerpo := mensajeAvisoTCC([]string{tccServicioCamara})

	if !strings.Contains(cuerpo, "evidencia") {
		t.Errorf("el aviso debe decir qué se pierde, quedó: %q", cuerpo)
	}
}

// TestPanelAjustesTCCApuntaAlPanelExacto: abrir "Privacidad y seguridad" a
// secas deja a la persona buscando entre veinte secciones.
func TestPanelAjustesTCCApuntaAlPanelExacto(t *testing.T) {
	if !strings.HasSuffix(panelAjustesTCC(tccServicioCamara), "Privacy_Camera") {
		t.Error("la cámara debe abrir su propio panel")
	}
	if !strings.HasSuffix(panelAjustesTCC(tccServicioPantalla), "Privacy_ScreenCapture") {
		t.Error("la grabación de pantalla debe abrir su propio panel")
	}
}

// TestTccutilSoloSeInvocaContraElBundleDelAyudante fija el hallazgo de terreno
// que hizo posible el paso 2 de la escalada, y la trampa que lo rodea.
//
// Historia corta, porque el matiz es todo: `tccutil reset` se archivo el
// 2026-08-14 como imposible, tras fallar con `rc=64 · No such bundle
// identifier` en sus cuatro formas contra el binario del servicio. El
// 2026-08-15 se midio que el diagnostico estaba incompleto —
// `com.apple.screensharing.agent`, un identificador de Apple real, falla
// EXACTAMENTE IGUAL— porque `tccutil` resuelve por LaunchServices y solo acepta
// un `.app`. Con el ayudante empaquetado y registrado, responde
// `Successfully reset`.
//
// De ahi el invariante que este test protege: `tccutil` puede invocarse, pero
// SOLO contra el identificador del bundle. Contra el binario del servicio o
// contra `cl.observer.agent` vuelve al rc=64 de siempre, y el sintoma seria un
// paso que dice que reparo y no reparo nada.
func TestTccutilSoloSeInvocaContraElBundleDelAyudante(t *testing.T) {
	fuente, err := os.ReadFile("tccpermisos_darwin.go")
	if err != nil {
		t.Fatalf("no se pudo leer la fuente de la ronda: %v", err)
	}

	visto := false
	for _, linea := range strings.Split(string(fuente), "\n") {
		codigo := strings.TrimSpace(linea)
		if strings.HasPrefix(codigo, "//") {
			continue // las menciones en comentarios son justamente el registro
		}
		if !strings.Contains(codigo, "tccutil") {
			continue
		}
		visto = true
		if strings.Contains(codigo, "nixAgentBin") || strings.Contains(codigo, "tccIdentificadorAgente") {
			t.Errorf("tccutil invocado contra el servicio, que esta medido que "+
				"devuelve rc=64: %q", codigo)
		}
	}

	// Control positivo: si el paso 2 desaparece, este test no puede quedar en
	// verde por no haber mirado nada.
	if !visto {
		t.Error("la ronda ya no invoca tccutil: el paso de reparacion se perdio")
	}
}

// TestLaPantallaEsperaLaConcesion fija el segundo arreglo del 2026-08-15.
//
// `CGRequestScreenCaptureAccess` no espera a la persona: abre el dialogo y
// contesta "todavia no". Concluir con ese valor produjo un aviso falso en
// terreno --la ronda se rindio a las 20:27:53 y la concesion aparecio a las
// 20:28:11--, asi que los dos pedidos de la escalada tienen que pasar por la
// espera y no por el pedido pelado.
func TestLaPantallaEsperaLaConcesion(t *testing.T) {
	fuente, err := os.ReadFile("tccpermisos_darwin.go")
	if err != nil {
		t.Fatalf("no se pudo leer la fuente de la ronda: %v", err)
	}
	m := string(fuente)

	inicio := strings.Index(m, "func (a *Agent) escalarPermisoTCC(")
	if inicio < 0 {
		t.Fatal("no esta escalarPermisoTCC: la escalada cambio de nombre")
	}
	cuerpo := m[inicio:]
	if fin := strings.Index(cuerpo, "\n// pedirYEsperarTCC"); fin > 0 {
		cuerpo = cuerpo[:fin]
	}

	pedidos := 0
	for _, linea := range strings.Split(cuerpo, "\n") {
		codigo := strings.TrimSpace(linea)
		if strings.HasPrefix(codigo, "//") {
			continue
		}
		if strings.Contains(codigo, "a.pedirPermisoTCC(") {
			t.Errorf("la escalada pide sin esperar la concesion: %q", codigo)
		}
		if strings.Contains(codigo, "a.pedirYEsperarTCC(") {
			pedidos++
		}
	}

	// Control positivo: los dos pasos que piden --el dialogo y el que sigue al
	// reset-- tienen que seguir ahi. Si alguno se pierde, el test no puede
	// quedar en verde por no haber mirado nada.
	if pedidos != 2 {
		t.Errorf("la escalada pide %d veces con espera; se esperaban 2 (dialogo y post-reset)", pedidos)
	}
}

// TestLaPantallaSeResetSinPreguntarleAlEstado fija el tercer arreglo del
// 2026-08-15, que es el que resucita la reparacion.
//
// En la pantalla el estado NO distingue "nunca se pregunto" de "hay una fila
// vieja que ya no matchea el csreq": las dos se leen como no-determinado. Con el
// paso 2 condicionado a que el estado sea otra cosa, la reparacion nunca corria
// -- justo en el unico caso para el que existe.
func TestLaPantallaSeResetSinPreguntarleAlEstado(t *testing.T) {
	fuente, err := os.ReadFile("tccpermisos_darwin.go")
	if err != nil {
		t.Fatalf("no se pudo leer la fuente de la ronda: %v", err)
	}
	m := string(fuente)

	cuerpo := func(firma string) string {
		i := strings.Index(m, firma)
		if i < 0 {
			t.Fatalf("no esta %q", firma)
		}
		resto := m[i+len(firma):]
		if fin := strings.Index(resto, "\n}\n"); fin > 0 {
			return resto[:fin]
		}
		return resto
	}

	// La escalada ya no puede condicionar el reset por el estado: esa decision
	// se mudo a valeLaPenaResetear, que sabe que hay un servicio donde el estado
	// no sirve para decidir.
	escalada := cuerpo("func (a *Agent) escalarPermisoTCC(servicio string, reg intentoTCC, ahora time.Time) intentoTCC {")
	for _, linea := range strings.Split(escalada, "\n") {
		codigo := strings.TrimSpace(linea)
		if strings.HasPrefix(codigo, "//") {
			continue
		}
		if strings.Contains(codigo, "tccEstadoNoDeterminado") && strings.Contains(codigo, "resetear") {
			t.Errorf("el paso 2 sigue condicionado por el estado: %q", codigo)
		}
	}

	// Y la rama de la pantalla no puede mirar el estado para nada.
	decision := cuerpo("func (a *Agent) valeLaPenaResetear(servicio string, estado int) bool {")
	rama := decision
	if i := strings.Index(rama, "tccServicioPantalla"); i >= 0 {
		rama = rama[i:]
		if fin := strings.Index(rama, "\t}"); fin > 0 {
			rama = rama[:fin]
		}
	} else {
		t.Fatal("valeLaPenaResetear ya no distingue la pantalla: el estado volvio a decidir por ella")
	}
	if strings.Contains(rama, "estado") {
		t.Errorf("la rama de la pantalla vuelve a mirar el estado: %q", strings.TrimSpace(rama))
	}
	if !strings.Contains(rama, "ejecutarResetTCC") {
		t.Error("la pantalla ya no ejecuta el reset acotado al bundle")
	}
}

// TestTccutilRecibeLaClaveDelServicioYNoElNombreEnEspanol fija el defecto que
// dejo muerta la reparacion en un Mac en espanol.
//
// `nombreServicioTCC` devuelve "Grabacion de pantalla", que es el texto del
// aviso. `tccutil` espera la CLAVE ("ScreenCapture"). Con el nombre en espanol,
// tccd responde `Invalid Service Name: kTCCServiceGrabacion de pantalla` y
// tccutil lo informa como si no hubiera podido con el bundle.
func TestTccutilRecibeLaClaveDelServicioYNoElNombreEnEspanol(t *testing.T) {
	fuente, err := os.ReadFile("tccpermisos_darwin.go")
	if err != nil {
		t.Fatalf("no se pudo leer la fuente de la ronda: %v", err)
	}

	visto := false
	for _, linea := range strings.Split(string(fuente), "\n") {
		codigo := strings.TrimSpace(linea)
		if strings.HasPrefix(codigo, "//") {
			continue
		}
		if !strings.Contains(codigo, "\"reset\"") {
			continue
		}
		visto = true
		if strings.Contains(codigo, "nombreServicioTCC") {
			t.Errorf("tccutil recibe el nombre para la persona, no la clave: %q", codigo)
		}
		if !strings.Contains(codigo, "servicio") {
			t.Errorf("tccutil no recibe el servicio: %q", codigo)
		}
	}

	// Control positivo: si el paso de reset desaparece, este test no puede
	// quedar en verde por no haber mirado nada.
	if !visto {
		t.Error("ya no hay invocacion de reset: el paso de reparacion se perdio")
	}
}
