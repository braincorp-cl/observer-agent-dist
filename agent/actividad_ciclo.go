package agent

// Feature 049 · ciclo de muestreo de actividad. Corre en su propia goroutine
// (la lanza AgentSvc), separada del bucle de geo/lostmode para no tocar ese
// `select` crítico. Cada tick:
//   1. toma una muestra en la sesión del usuario (self-exec `-m actividad-muestra`,
//      mismo patrón que la captura: el servicio corre como root y baja al usuario),
//   2. decide activo/inactivo con el umbral de inactividad,
//   3. la acumula en el resumen del día (persistido en disco para sobrevivir
//      reinicios),
//   4. periódicamente sube el resumen al servidor.
//
// Resistente a manipulación: el muestreo lo hace el servicio, no un proceso que
// el usuario pueda matar.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	rmm "github.com/braincorp-cl/observer-agent-dist/shared"
)

// Cada cuánto se sube el resumen del día en curso (parcial).
const actividadSubirCada = 10 * time.Minute

var (
	actAccMu sync.Mutex
	actAcc   *AcumuladorActividad
)

// MuestraActividadComoUsuario re-ejecuta el agente en la sesión del usuario y
// devuelve la muestra. Es el puente servicio(root) → usuario.
func (a *Agent) MuestraActividadComoUsuario(conTitulo bool) (rmm.ActividadMuestra, error) {
	var m rmm.ActividadMuestra
	exe, err := os.Executable()
	if err != nil {
		return m, err
	}
	args := []string{"-m", "actividad-muestra"}
	if conTitulo {
		args = append(args, "titulo")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd, err := a.newUserSessionCmd(ctx, exe, args...)
	if err != nil {
		return m, err
	}
	out, err := cmd.Output()
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(out, &m); err != nil {
		return m, fmt.Errorf("muestra ilegible: %w", err)
	}
	return m, nil
}

func procesoReal(p string) bool {
	switch p {
	case "", "(escritorio)", "(desconocido)":
		return false
	}
	return true
}

func tzOffset() string {
	_, off := time.Now().Zone()
	sign := "+"
	if off < 0 {
		sign = "-"
		off = -off
	}
	return fmt.Sprintf("%s%02d:%02d", sign, off/3600, (off%3600)/60)
}

func (a *Agent) cargarAcumulador() *AcumuladorActividad {
	data, err := os.ReadFile(a.actividadDataPath())
	if err == nil {
		var ac AcumuladorActividad
		if json.Unmarshal(data, &ac) == nil && ac.Dias != nil {
			ac.TZ = tzOffset()
			return &ac
		}
	}
	return nuevoAcumulador(a.Hostname, soActividad(), tzOffset())
}

func (a *Agent) guardarAcumulador(ac *AcumuladorActividad) {
	path := a.actividadDataPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	data, err := json.Marshal(ac)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
}

func (a *Agent) subirResumenActividad(res rmm.ActividadResumen) error {
	url := fmt.Sprintf("/api/v3/%s/activity/", a.AgentID)
	r, err := a.rClient.R().SetBody(res).Post(url)
	if err != nil {
		return err
	}
	if r.StatusCode() >= 300 {
		return fmt.Errorf("servidor rechazó la actividad: %d", r.StatusCode())
	}
	return nil
}

// BucleActividad es la goroutine de muestreo. No termina nunca: el ticker sigue
// y el interruptor se re-evalúa en cada tick (mismo criterio que el ticker de geo).
func (a *Agent) BucleActividad(nc *nats.Conn) {
	a.retirarActividad048()

	actAccMu.Lock()
	actAcc = a.cargarAcumulador()
	actAccMu.Unlock()

	_, intervalSeg, _, _ := a.getActividad()
	ticker := time.NewTicker(time.Duration(intervalSeg) * time.Second)
	defer ticker.Stop()

	ultimaSubida := time.Now()
	diaActual := time.Now().Format("2006-01-02")
	var ultimaCaptura time.Time
	// 049-D: muestras recientes contadas activas sólo por el umbral de idle, por
	// si hay que reclasificarlas, y si es la primera vuelta tras arrancar (un
	// hueco desde la última muestra persistida = equipo apagado).
	var recientes []muestraReciente
	primeraVuelta := true
	// 049-E: lectura anterior de los contadores de teclas y mouse.
	var lector lectorEntrada

	for {
		select {
		case <-actChanged:
			// Re-armar el ticker con la cadencia nueva sin esperar al próximo tick.
			_, nuevo, _, _ := a.getActividad()
			ticker.Stop()
			ticker = time.NewTicker(time.Duration(nuevo) * time.Second)
		case <-ticker.C:
			active, interval, inactivo, conTitulo := a.getActividad()
			if !active {
				recientes = nil
				lector = lectorEntrada{}
				a.entradaDetener()
				a.olvidarUltimaMuestra()
				continue
			}
			a.entradaIniciar()
			ahora := time.Now()
			a.anotarHueco(ahora, interval, primeraVuelta)
			primeraVuelta = false

			m, err := a.MuestraActividadComoUsuario(conTitulo)
			if err != nil {
				a.Logger.Debugln("actividad: no se pudo muestrear:", err)
				continue
			}
			ev := evaluarMuestra(m, inactivo)
			activo := ev.activo

			capturasMin, red := a.getActividadExtras()
			var fotoRed map[string]appRed
			if red {
				fotoRed = muestraRedPorApp()
			}

			// Con la sesión bloqueada no se atribuye tiempo a la app de bloqueo.
			proc, tit, url := m.Proceso, m.Titulo, m.URL
			if m.Bloqueado {
				proc, tit, url = "", "", ""
			}
			dTeclas, dMouse := lector.delta(entradaAcumulada(m))

			actAccMu.Lock()
			actAcc.Agregar(ahora, m.Usuario, proc, tit, activo, interval)
			actAcc.AgregarURL(ahora, m.Usuario, url, activo, interval)
			if !m.Bloqueado {
				actAcc.AgregarEntrada(ahora, m.Usuario, dTeclas, dMouse)
			}
			actAcc.AgregarRed(ahora, m.Usuario, fotoRed, interval)
			if ev.conEntrada {
				actAcc.RegistrarEntrada(ahora, m.Usuario, ahora.Add(-time.Duration(ev.idleEntrada)*time.Second))
			}
			recientes = reclasificarRecientes(actAcc, recientes, m, ev, ahora, interval, inactivo)
			a.guardarAcumulador(actAcc)
			actAccMu.Unlock()

			// 049-C: captura periódica, sólo con el usuario activo.
			if capturasMin > 0 && activo && ahora.Sub(ultimaCaptura) >= time.Duration(capturasMin)*time.Minute {
				ultimaCaptura = ahora
				go a.capturaActividad(m)
			}

			// Cierre de día: subir el día recién cerrado y purgar.
			if hoy := ahora.Format("2006-01-02"); hoy != diaActual {
				a.subirDia(diaActual, false)
				actAccMu.Lock()
				actAcc.PurgarAnteriores(hoy)
				a.guardarAcumulador(actAcc)
				actAccMu.Unlock()
				diaActual = hoy
				ultimaSubida = ahora
			}

			// Subida periódica del día en curso (parcial).
			if ahora.Sub(ultimaSubida) >= actividadSubirCada {
				a.subirDia(diaActual, true)
				ultimaSubida = ahora
			}
		}
	}
}

// --- 049-D -------------------------------------------------------------------

type muestraReciente struct {
	ts                            time.Time
	usuario, proceso, titulo, url string
	intervalo                     int
}

type evaluacion struct {
	activo bool
	// forzado: activa sólo por un patrón "siempre activo" (reunión).
	forzado bool
	// conEntrada: la muestra informa la hora de la última entrada real, que
	// fue hace idleEntrada segundos (0 si es forzada: la reunión cuenta como
	// entrada continua).
	conEntrada  bool
	idleEntrada int
	// cruzoUmbral: el idle llegó al umbral; lo contado activo después de la
	// última entrada no lo era.
	cruzoUmbral bool
}

func evaluarMuestra(m rmm.ActividadMuestra, inactivoSeg int) evaluacion {
	var ev evaluacion
	sana := m.Error == "" && !m.Bloqueado
	base := sana && procesoReal(m.Proceso)
	porIdle := m.IdleSeg >= 0 && m.IdleSeg < inactivoSeg
	ev.activo = base && porIdle
	if base && !porIdle && esSiempreActivo(m.Proceso, m.Titulo) {
		ev.activo, ev.forzado = true, true
	}
	switch {
	case ev.forzado:
		ev.conEntrada = true
	case sana && m.IdleSeg >= 0:
		ev.conEntrada, ev.idleEntrada = true, m.IdleSeg
	}
	ev.cruzoUmbral = m.Error == "" && m.IdleSeg >= inactivoSeg && !ev.forzado
	return ev
}

// reclasificarRecientes mantiene la ventana de muestras activas-por-umbral y,
// al cruzar el umbral, pasa a inactivas las posteriores a la última entrada.
// El llamador tiene tomado actAccMu.
func reclasificarRecientes(ac *AcumuladorActividad, recientes []muestraReciente, m rmm.ActividadMuestra, ev evaluacion, ahora time.Time, intervalo, inactivoSeg int) []muestraReciente {
	if ev.cruzoUmbral {
		ultima := ahora.Add(-time.Duration(m.IdleSeg) * time.Second)
		hoy := ahora.Format("2006-01-02")
		for _, r := range recientes {
			if r.usuario == m.Usuario && r.ts.After(ultima) && r.ts.Format("2006-01-02") == hoy {
				ac.Reclasificar(r.ts, r.usuario, r.proceso, r.titulo, r.intervalo)
				ac.ReclasificarURL(r.ts, r.usuario, r.url, r.intervalo)
			}
		}
		return nil
	}
	if ev.activo && !ev.forzado {
		recientes = append(recientes, muestraReciente{ts: ahora, usuario: m.Usuario, proceso: m.Proceso, titulo: m.Titulo, url: m.URL, intervalo: intervalo})
	}
	// Sólo puede reclasificarse lo que está dentro del umbral.
	limite := ahora.Add(-time.Duration(inactivoSeg+2*intervalo) * time.Second)
	i := 0
	for i < len(recientes) && recientes[i].ts.Before(limite) {
		i++
	}
	return recientes[i:]
}

// umbralHueco: un salto menor es jitter o un reinicio del servicio.
func umbralHueco(intervaloSeg int) time.Duration {
	u := time.Duration(3*intervaloSeg) * time.Second
	if u < 5*time.Minute {
		u = 5 * time.Minute
	}
	return u
}

// anotarHueco registra el tramo sin muestreo desde la muestra anterior. La
// resta es con hora de pared (time.Unix no lleva reloj monotónico), que es la
// que avanza durante una suspensión.
func (a *Agent) anotarHueco(ahora time.Time, intervaloSeg int, primeraVuelta bool) {
	actAccMu.Lock()
	defer actAccMu.Unlock()
	if actAcc.UltimaMuestra > 0 {
		prev := time.Unix(actAcc.UltimaMuestra, 0)
		if ahora.Sub(prev) > umbralHueco(intervaloSeg) {
			motivo := "suspendido"
			if primeraVuelta {
				motivo = "apagado"
			}
			actAcc.RegistrarHueco(prev, ahora, motivo)
		}
	}
	actAcc.UltimaMuestra = ahora.Unix()
}

// olvidarUltimaMuestra: con el monitoreo apagado no hay hueco que medir al
// volver a encenderlo.
func (a *Agent) olvidarUltimaMuestra() {
	actAccMu.Lock()
	defer actAccMu.Unlock()
	if actAcc.UltimaMuestra != 0 {
		actAcc.UltimaMuestra = 0
		a.guardarAcumulador(actAcc)
	}
}

func (a *Agent) subirDia(fecha string, parcial bool) {
	actAccMu.Lock()
	res, ok := actAcc.ResumenDe(fecha, parcial)
	actAccMu.Unlock()
	if !ok {
		return
	}
	if err := a.subirResumenActividad(res); err != nil {
		a.Logger.Debugln("actividad: subida falló:", err)
		return
	}
	a.Logger.Debugln("actividad: resumen subido", fecha, "parcial:", parcial)
}
