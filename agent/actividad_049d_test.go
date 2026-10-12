package agent

import (
	"encoding/json"
	"testing"
	"time"

	rmm "github.com/braincorp-cl/observer-agent-dist/shared"
)

// Feature 049-D · medición más honesta: idle retroactivo, tramos de 10 min,
// jornada/pausas por entrada real, huecos y patrones "siempre activo".

func muestra(usuario, proceso, titulo string, idle int) rmm.ActividadMuestra {
	return rmm.ActividadMuestra{Usuario: usuario, Proceso: proceso, Titulo: titulo, IdleSeg: idle}
}

// simular corre el mismo camino que BucleActividad sobre muestras ya tomadas.
func simular(ac *AcumuladorActividad, base time.Time, intervalo, umbral int, muestras []rmm.ActividadMuestra) {
	var recientes []muestraReciente
	for i, m := range muestras {
		ahora := base.Add(time.Duration(i*intervalo) * time.Second)
		ev := evaluarMuestra(m, umbral)
		ac.Agregar(ahora, m.Usuario, m.Proceso, m.Titulo, ev.activo, intervalo)
		if ev.conEntrada {
			ac.RegistrarEntrada(ahora, m.Usuario, ahora.Add(-time.Duration(ev.idleEntrada)*time.Second))
		}
		recientes = reclasificarRecientes(ac, recientes, m, ev, ahora, intervalo, umbral)
	}
}

func TestIdleRetroactivoDescuentaLoContadoAntesDelUmbral(t *testing.T) {
	ac := nuevoAcumulador("PC", "windows", "-03:00")
	base := time.Date(2026, 10, 12, 9, 0, 0, 0, time.Local)
	// Muestras cada 30 s, umbral 120 s. Escribe 2 min, después se va 3 min.
	var ms []rmm.ActividadMuestra
	for i := 0; i < 4; i++ {
		ms = append(ms, muestra("ana", "excel.exe", "Informe", 5))
	}
	for idle := 30; idle <= 180; idle += 30 {
		ms = append(ms, muestra("ana", "excel.exe", "Informe", idle))
	}
	simular(ac, base, 30, 120, ms)

	u := ac.Dias["2026-10-12"]["ana"]
	// 4 activas reales (120 s). Las de idle 30/60/90 se contaron activas y al
	// llegar a 120 se reclasifican; quedan 6 inactivas (180 s).
	if u.Activo != 120 || u.Inactivo != 180 {
		t.Fatalf("activo=%d inactivo=%d, esperaba 120/180", u.Activo, u.Inactivo)
	}
	if u.Procs["excel.exe"] != 120 || u.Titulos["Informe"] != 120 {
		t.Fatalf("procs/títulos no se descontaron: %+v %+v", u.Procs, u.Titulos)
	}
	if h := u.Horas["09"]; h.A != 120 || h.I != 180 || h.P["excel.exe"] != 120 {
		t.Fatalf("hora: %+v", h)
	}
	if tr := u.Tramos[54]; tr != [2]int{120, 300} {
		t.Fatalf("tramo 09:00: %v", tr)
	}
}

func TestSinCruzarUmbralNoSeReclasifica(t *testing.T) {
	ac := nuevoAcumulador("PC", "linux", "-03:00")
	base := time.Date(2026, 10, 12, 9, 0, 0, 0, time.Local)
	simular(ac, base, 30, 120, []rmm.ActividadMuestra{
		muestra("ana", "code", "", 5), muestra("ana", "code", "", 35),
		muestra("ana", "code", "", 65), muestra("ana", "code", "", 2),
	})
	if u := ac.Dias["2026-10-12"]["ana"]; u.Activo != 120 || u.Inactivo != 0 {
		t.Fatalf("pausa corta no debe descontarse: %+v", u)
	}
}

func TestPatronSiempreActivoCuentaReunionYNoAbrePausa(t *testing.T) {
	a := agenteActividadDePrueba(t)
	a.SetActividadSiempreActivo([]string{"teams", `\bmeet\b`, "(?<=x)inválido"})
	t.Cleanup(func() { a.SetActividadSiempreActivo(nil) })
	if !esSiempreActivo("ms-teams.exe", "") || !esSiempreActivo("chrome", "Meet - abc-defg-hij") {
		t.Fatal("teams/meet deben coincidir")
	}
	if esSiempreActivo("chrome", "Meeting notes") {
		t.Fatal(`\bmeet\b no debe coincidir con "Meeting"`)
	}
	if got := a.estadoActividadPersistible().SiempreActivo; len(got) != 2 {
		t.Fatalf("el patrón inválido debe descartarse: %v", got)
	}

	ac := nuevoAcumulador("PC", "windows", "-03:00")
	base := time.Date(2026, 10, 12, 10, 0, 0, 0, time.Local)
	ms := []rmm.ActividadMuestra{muestra("ana", "ms-teams.exe", "Reunión", 2)}
	for idle := 60; idle <= 1800; idle += 60 { // 30 min de reunión sin tocar nada
		ms = append(ms, muestra("ana", "ms-teams.exe", "Reunión", idle))
	}
	simular(ac, base, 60, 300, ms)
	u := ac.Dias["2026-10-12"]["ana"]
	if u.Inactivo != 0 || u.Activo != 31*60 {
		t.Fatalf("la reunión debe contar activa entera: activo=%d inactivo=%d", u.Activo, u.Inactivo)
	}
	if len(u.Pausas) != 0 {
		t.Fatalf("una reunión no es pausa: %v", u.Pausas)
	}
}

func TestBloqueoNoEsForzadoPorPatron(t *testing.T) {
	a := agenteActividadDePrueba(t)
	a.SetActividadSiempreActivo([]string{"teams"})
	t.Cleanup(func() { a.SetActividadSiempreActivo(nil) })
	m := muestra("ana", "ms-teams.exe", "", 900)
	m.Bloqueado = true
	if ev := evaluarMuestra(m, 300); ev.activo || ev.conEntrada {
		t.Fatalf("sesión bloqueada nunca es activa: %+v", ev)
	}
}

func TestJornadaYPausasPorEntradaReal(t *testing.T) {
	ac := nuevoAcumulador("PC", "linux", "-03:00")
	base := time.Date(2026, 10, 12, 8, 30, 0, 0, time.Local)
	var ms []rmm.ActividadMuestra
	for i := 0; i < 10; i++ { // 08:30-08:39 trabaja
		ms = append(ms, muestra("ana", "code", "", 3))
	}
	for i := 1; i <= 20; i++ { // 20 min sin entrada (almuerzo corto)
		ms = append(ms, muestra("ana", "code", "", i*60))
	}
	for i := 0; i < 5; i++ { // vuelve
		ms = append(ms, muestra("ana", "code", "", 1))
	}
	simular(ac, base, 60, 300, ms)

	res, _ := ac.ResumenDe("2026-10-12", true)
	u := res.Dias[0].Usuarios[0]
	if u.PrimerActivo != "08:29" || u.UltimoActivo != "09:03" {
		t.Fatalf("jornada %s-%s", u.PrimerActivo, u.UltimoActivo)
	}
	if len(u.Pausas) != 1 || u.Pausas[0] != [2]string{"08:39", "08:59"} {
		t.Fatalf("pausas: %v", u.Pausas)
	}
	if len(u.Tramos) == 0 || u.Tramos["51"][1] == 0 {
		t.Fatalf("tramos: %v", u.Tramos)
	}
}

func TestHuecosSeRecortanAlDiaYSePurgan(t *testing.T) {
	ac := nuevoAcumulador("PC", "windows", "-03:00")
	ayer := time.Date(2026, 10, 11, 18, 0, 0, 0, time.Local)
	hoy := time.Date(2026, 10, 12, 8, 15, 0, 0, time.Local)
	ac.Agregar(hoy, "ana", "excel.exe", "", true, 60)
	ac.RegistrarHueco(ayer, hoy, "apagado")
	ac.RegistrarHueco(hoy.Add(2*time.Hour), hoy.Add(3*time.Hour), "suspendido")
	res, _ := ac.ResumenDe("2026-10-12", true)
	h := res.Dias[0].Huecos
	if len(h) != 2 || h[0] != (rmm.ActividadHueco{Desde: "00:00", Hasta: "08:15", Motivo: "apagado"}) || h[1].Motivo != "suspendido" {
		t.Fatalf("huecos: %+v", h)
	}
	ac.PurgarAnteriores("2026-10-13")
	if len(ac.Huecos) != 0 {
		t.Fatal("los huecos de días cerrados deben purgarse")
	}
}

func TestAnotarHuecoDistingueApagadoDeSuspendido(t *testing.T) {
	a := agenteActividadDePrueba(t)
	actAccMu.Lock()
	actAcc = nuevoAcumulador("PC", "linux", "-03:00")
	actAccMu.Unlock()
	t0 := time.Date(2026, 10, 12, 9, 0, 0, 0, time.Local)
	actAcc.UltimaMuestra = t0.Unix()

	a.anotarHueco(t0.Add(40*time.Minute), 30, true) // arranque tras 40 min
	a.anotarHueco(t0.Add(41*time.Minute), 30, false)
	a.anotarHueco(t0.Add(42*time.Minute), 30, false) // jitter: nada
	a.anotarHueco(t0.Add(90*time.Minute), 30, false) // suspensión
	hs := actAcc.Huecos["2026-10-12"]
	if len(hs) != 2 || hs[0].Motivo != "apagado" || hs[1].Motivo != "suspendido" {
		t.Fatalf("huecos: %+v", hs)
	}
	a.olvidarUltimaMuestra()
	if actAcc.UltimaMuestra != 0 {
		t.Fatal("apagar el monitoreo olvida la referencia")
	}
}

func TestSiempreActivoLlegaPorConfigYServidorViejoNoTrae(t *testing.T) {
	a := agenteActividadDePrueba(t)
	t.Cleanup(func() { a.SetActividadSiempreActivo(nil) })
	var conf AgentCheckInConfig
	raw := `{"actividad_active":true,"actividad_interval_seg":30,"actividad_inactivo_seg":300,"actividad_siempre_activo":["zoom"]}`
	if err := json.Unmarshal([]byte(raw), &conf); err != nil {
		t.Fatal(err)
	}
	a.aplicarActividadDeConfig(conf)
	if !esSiempreActivo("zoom.exe", "") {
		t.Fatal("el patrón de la config debe aplicarse")
	}
	st, _ := a.loadActividadCfg()
	if len(st.SiempreActivo) != 1 {
		t.Fatalf("debe persistir: %+v", st)
	}
	var viejo AgentCheckInConfig
	_ = json.Unmarshal([]byte(`{"actividad_active":true}`), &viejo)
	a.aplicarActividadDeConfig(viejo)
	if esSiempreActivo("zoom.exe", "") {
		t.Fatal("un servidor sin la clave no deja patrones")
	}
}
