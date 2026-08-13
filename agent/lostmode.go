package agent

import (
	"fmt"
	"net/http"
	"os"
	"strconv"

	nats "github.com/nats-io/nats.go"
)

// Feature 030 · Fase 1 · el ciclo de captura del modo perdido (T011).
//
// Mientras el equipo está marcado como perdido, cada tick de geo deja de ser
// sólo un punto y pasa a ser un LOTE: la ubicación del momento y la captura de
// la pantalla, subidas juntas y numeradas por el servidor como un ciclo. Es lo
// que la consola muestra después como una línea de la línea de tiempo del caso.
//
// TRES DECISIONES QUE VALE LA PENA DEJAR ESCRITAS:
//
//  1. **Se mide la ubicación UNA sola vez** y el mismo punto se publica por NATS
//     (para el mapa de siempre) y se adjunta a la evidencia (para el caso). Con
//     dos mediciones el mapa y la evidencia podrían discrepar, y en macOS cada
//     medición cuesta hasta 40 s.
//
//  2. **Un ciclo sin imagen se sube igual, con su motivo.** `sin_sesion` dice
//     que nadie ha abierto sesión; `wayland_no_soportado` dice que este equipo
//     no va a dar capturas nunca. Las dos cosas son información del caso, y
//     callarlas dejaría al operador sin poder distinguirlas de un equipo
//     apagado.
//
//  3bis. **LA WEBCAM TIENE SU PROPIO INTERRUPTOR, apagado de fábrica.** No basta
//     con que el equipo esté marcado como perdido: fotografiar una cara es
//     cualitativamente más sensible que ubicar un activo (ADR-025), y actualizar
//     el producto no puede empezar a hacerlo solo. El interruptor es global y
//     vive en el servidor; el agente lo lee de la config y NO lo guarda en disco.
//
//  3. **NO se guarda evidencia en cola cuando no hay red.** Es deliberado y va
//     contra la intuición: parece mejor acumular las capturas y subirlas al
//     reconectar. Pero ese archivo queda EN EL EQUIPO QUE ALGUIEN SE LLEVÓ, sin
//     cifrar —el cifrado en reposo es de la Fase 3— y con fotos de la pantalla
//     de una persona. Perder el ciclo es barato; dejar un depósito de evidencia
//     en manos de quien tiene el equipo no lo es (ADR-025).

// EjecutarCicloModoPerdido mide, captura y sube el lote de un ciclo.
//
// Corre en su propia goroutine (la lanza el bucle del servicio con la misma
// guardia que la captura de geo normal), así que puede tomarse su tiempo sin
// atrasar `agent-hello` ni hacer que el equipo aparezca OFFLINE justo cuando
// más importa.
func (a *Agent) EjecutarCicloModoPerdido(nc *nats.Conn) {
	loc := a.GetLocation()

	// El punto va por NATS igual que siempre: el modo perdido no reemplaza el
	// recorrido del mapa, lo intensifica.
	a.PublishGeo(nc, loc)

	shot := a.CaptureScreen()
	if shot.OK() {
		// Pase lo que pase con la subida, la imagen no se queda en el equipo.
		defer func() {
			if err := os.Remove(shot.Path); err != nil && !os.IsNotExist(err) {
				a.Logger.Debugln("ciclo de modo perdido: no se pudo borrar la captura local:", err)
			}
		}()
	} else {
		a.Logger.Debugln("ciclo de modo perdido: sin captura de pantalla, motivo:", shot.Reason)
	}

	foto := a.capturarWebcamDelCiclo()
	if foto.OK() {
		defer func() {
			if err := os.Remove(foto.Path); err != nil && !os.IsNotExist(err) {
				a.Logger.Debugln("ciclo de modo perdido: no se pudo borrar la foto local:", err)
			}
		}()
	}

	a.subirEvidencia(loc, shot, foto)
}

// capturarWebcamDelCiclo respeta el interruptor global antes de abrir la cámara.
//
// Devuelve una pieza VACÍA —sin ruta y sin motivo— cuando la webcam está
// apagada, y eso es distinto de un motivo de fallo: apagada no significa que se
// intentó y no se pudo. Sin esta distinción, la línea de tiempo de una flota que
// nunca activó la webcam se llenaría de ciclos diciendo que la cámara falló.
func (a *Agent) capturarWebcamDelCiclo() WebcamShot {
	if !a.LostModeWebcam {
		return WebcamShot{}
	}

	foto := a.CaptureWebcam()
	if !foto.OK() {
		a.Logger.Debugln("ciclo de modo perdido: sin foto de webcam, motivo:", foto.Reason)
	}
	return foto
}

// camposEvidencia arma los campos del multipart.
//
// Vive aparte de la subida —y sin depender del Agent— para poder probarla: los
// nombres de estos campos son un contrato con el servidor y equivocarse en uno
// no rompe nada visible. La subida seguiría contestando 200 y la evidencia
// llegaría sin ubicación, que es el peor resultado posible: un caso que parece
// completo y no lo está.
func camposEvidencia(loc Location, shot ScreenShot, foto WebcamShot, sessionUser string) map[string]string {
	campos := map[string]string{
		// El reloj del EQUIPO. El servidor guarda además el suyo: entre los dos
		// puede haber horas si el equipo estuvo sin red, y para un caso que puede
		// terminar en una denuncia la diferencia importa.
		"captured_at": strconv.FormatInt(loc.CapturedAt.Unix(), 10),
		// Quién tenía la sesión abierta en ese momento. Puede venir vacío —nadie
		// ha entrado— y eso también es un dato del caso.
		"session_user": sessionUser,
	}

	// Las coordenadas viajan sólo si hay fix. Mandar ceros haría que el servidor
	// tuviera que adivinar si son una posición o la ausencia de una (Null Island
	// es el artefacto clásico), y ese es justamente el tipo de dato fabricado que
	// ADR-025 no admite.
	if loc.HasFix() {
		campos["lat"] = strconv.FormatFloat(loc.Lat, 'f', 7, 64)
		campos["lng"] = strconv.FormatFloat(loc.Long, 'f', 7, 64)
		campos["accuracy_m"] = strconv.Itoa(loc.AccuracyM)
	}
	// La fuente va SIEMPRE, incluso en `denied` o `unavailable`: sin coordenadas
	// no hay fila de geo, pero la pieza de pantalla igual queda diciendo por qué
	// no se supo dónde estaba el equipo.
	if loc.Source != "" {
		campos["source"] = loc.Source
	}

	if !shot.OK() {
		campos["screen_reason"] = shot.Reason
	}

	// El motivo de la webcam viaja SÓLO cuando se intentó y no se pudo. Con la
	// webcam apagada la pieza va vacía (ver capturarWebcamDelCiclo) y el campo no
	// se manda: el servidor no crea fila de webcam, y la línea de tiempo no dice
	// nada de una cámara que nadie pidió usar.
	if !foto.OK() && foto.Reason != "" {
		campos["webcam_reason"] = foto.Reason
	}

	return campos
}

// usuarioDeLaEvidencia responde "¿de quién era la pantalla que se capturó?".
//
// Se prefiere el dueño de la SESIÓN GRÁFICA sobre `LoggedOnUser()`, y no es un
// detalle de precisión: son dos preguntas distintas. `LoggedOnUser()` lee utmp
// —quién inició sesión en el sistema— y en terreno (2026-08-12, cliente-rmm con
// autologin) devolvió vacío mientras había una sesión de escritorio abierta y
// funcionando. La evidencia quedaba sin atribución justo en el equipo del que sí
// se pudo capturar la pantalla.
//
// El respaldo por utmp se conserva para el caso inverso: un equipo sin
// escritorio donde igual hay alguien conectado por consola o SSH.
func (a *Agent) usuarioDeLaEvidencia() string {
	if u := a.usuarioDeLaSesionGrafica(); u != "" {
		return u
	}
	return a.LoggedOnUser()
}

// subirEvidencia manda el lote al servidor.
func (a *Agent) subirEvidencia(loc Location, shot ScreenShot, foto WebcamShot) {
	url := fmt.Sprintf("/api/v3/%s/lostmode/evidence/", a.AgentID)

	req := a.rClient.R().SetFormData(camposEvidencia(loc, shot, foto, a.usuarioDeLaEvidencia()))
	if shot.OK() {
		req = req.SetFile("screen", shot.Path)
	}
	if foto.OK() {
		req = req.SetFile("webcam", foto.Path)
	}

	r, err := req.Post(url)
	if err != nil {
		// Sin red no hay reintento ni cola (ver la decisión 3 del encabezado).
		a.Logger.Debugln("ciclo de modo perdido: no se pudo subir la evidencia:", err)
		return
	}

	// 409 = el servidor dice que este equipo ya no está marcado. Es el canal más
	// rápido que existe para enterarse de que el caso se cerró —más que el
	// polling de config— y apagarse acá evita seguir capturando la pantalla de
	// alguien cuyo caso ya terminó. El estado en disco se limpia solo, porque
	// SetLostMode borra el archivo al apagar.
	if r.StatusCode() == http.StatusConflict {
		a.Logger.Infoln("ciclo de modo perdido: el servidor informa que el equipo ya no está marcado, se detiene la captura")
		a.SetLostMode(false, 0)
		return
	}

	if r.StatusCode() >= 300 {
		a.Logger.Debugln("ciclo de modo perdido: el servidor rechazó la evidencia:", r.StatusCode(), string(r.Body()))
		return
	}

	a.Logger.Debugln("ciclo de modo perdido: evidencia subida")
}
