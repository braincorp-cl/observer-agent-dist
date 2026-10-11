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
	actAccMu.Lock()
	actAcc = a.cargarAcumulador()
	actAccMu.Unlock()

	_, intervalSeg, _, _ := a.getActividad()
	ticker := time.NewTicker(time.Duration(intervalSeg) * time.Second)
	defer ticker.Stop()

	ultimaSubida := time.Now()
	diaActual := time.Now().Format("2006-01-02")
	var ultimaCaptura time.Time

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
				continue
			}
			m, err := a.MuestraActividadComoUsuario(conTitulo)
			if err != nil {
				a.Logger.Debugln("actividad: no se pudo muestrear:", err)
				continue
			}
			activo := m.Error == "" && m.IdleSeg >= 0 && m.IdleSeg < inactivo && !m.Bloqueado && procesoReal(m.Proceso)
			ahora := time.Now()

			capturasMin, red := a.getActividadExtras()
			var fotoRed map[string]appRed
			if red {
				fotoRed = muestraRedPorApp()
			}

			actAccMu.Lock()
			actAcc.Agregar(ahora, m.Usuario, m.Proceso, m.Titulo, activo, interval)
			actAcc.AgregarRed(ahora, m.Usuario, fotoRed, interval)
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
