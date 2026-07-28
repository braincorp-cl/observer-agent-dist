package agent

import (
	"errors"
	"time"
)

// Feature 028 · Fase 2 — control del volumen de salida durante la alarma.
//
// Cada SO implementa dos primitivas y nada más:
//
//	readVolume() (volumeState, error)   // lee nivel y silencio actuales
//	setVolume(volumeState) error        // aplica un nivel y un silencio
//
// Subir al máximo no es una tercera primitiva: es `setVolume(maxVolumeState)`.
// Que subir y restaurar compartan la misma función es deliberado — si la
// restauración usara un camino propio, sería un camino que sólo se ejercita en
// el apagado y que ninguna prueba del encendido tocaría.
//
// Diferencia deliberada con Prey (`lib/agent/actions/alarm/index.js`): Prey sube
// el volumen y NO lo restaura nunca. Nosotros guardamos el par previo y lo
// devolvemos en el mismo teardown que apaga el sonido.

// errVolumeUnsupported: no hay forma de tocar el volumen en este equipo. NO es
// un fallo de la alarma: la alarma suena igual, al volumen que tenga el usuario.
// Degradar así es intencional — que no se pueda subir el volumen no es razón
// para no hacer sonar un equipo que se está buscando.
var errVolumeUnsupported = errors.New("no hay forma de controlar el volumen de salida en este equipo")

// maxVolumeState es "al máximo y sin silenciar".
var maxVolumeState = volumeState{Level: 1, Muted: false, MuteKnown: true, Known: true}

// volumeReassertInterval es cada cuánto se reafirma el volumen máximo mientras
// la alarma suena. Si la persona baja el volumen o silencia el equipo, se le
// vuelve a subir. Es el mismo comportamiento de Prey (1000 ms) y la razón es la
// misma: sin reafirmación, bajar el volumen desactiva la alarma sin permiso.
//
// En macOS y Linux cada reafirmación es un proceso corto de vida propia (hijo
// directo del agente, que termina solo); en Windows es una llamada COM en el
// propio proceso. Ninguna de las dos agrega un nivel al árbol de procesos, que
// es la condición que mantiene a Windows inmune al bug del stop huérfano
// (ver `killUserSessionCmd` y `KillProc`, que sólo baja un nivel).
const volumeReassertInterval = time.Second

// raiseVolumeForAlarm sube el volumen al máximo y devuelve el estado previo para
// poder restaurarlo.
//
// Si no se pudo leer el estado previo NO se sube nada. Subir a ciegas dejaría un
// equipo al 100 % sin ninguna forma de devolverlo, que es exactamente el peor
// caso que la Fase 2 tiene que evitar.
func (a *Agent) raiseVolumeForAlarm() volumeState {
	prev, err := a.readVolume()
	if err != nil {
		a.Logger.Debugln("raiseVolumeForAlarm: no se pudo leer el volumen actual:", err)
		return volumeState{}
	}

	if err := a.setVolume(maxVolumeState); err != nil {
		a.Logger.Debugln("raiseVolumeForAlarm: no se pudo subir el volumen:", err)
		return volumeState{}
	}

	a.Logger.Debugf("raiseVolumeForAlarm: volumen previo %.2f (silenciado=%v), ahora al máximo",
		prev.Level, prev.Muted)

	return prev
}

// restoreSavedVolume devuelve el equipo al estado guardado. Sin estado conocido
// no hace nada: mejor dejar el volumen donde está que fijarlo en el cero de Go.
func (a *Agent) restoreSavedVolume(v volumeState) {
	if !v.Known {
		return
	}

	if err := a.setVolume(v); err != nil {
		a.Logger.Errorln("restoreSavedVolume: no se pudo devolver el volumen previo:", err)
		return
	}

	a.Logger.Debugf("restoreSavedVolume: volumen devuelto a %.2f (silenciado=%v)", v.Level, v.Muted)
}

// clampLevel acota a [0,1]. Los niveles vienen de parsear salida de comandos
// externos, y un porcentaje por encima de 100 —PulseAudio los permite— pasado
// tal cual a la API de Windows es un error de rango, no un volumen alto.
func clampLevel(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
