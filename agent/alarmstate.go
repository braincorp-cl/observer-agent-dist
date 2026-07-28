package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

// Feature 028 · Fase 2 — estado de la alarma que sobrevive al proceso.
//
// Se persisten dos cosas, por razones distintas:
//
//   - El VOLUMEN GUARDADO, siempre que "sonido máximo" esté activo. Si el agente
//     muere a mitad de alarma —o lo mata un reinicio— el equipo se queda al 100 %
//     y el valor original se perdería para siempre. El archivo es lo único que
//     permite devolverlo al arrancar.
//   - La ALARMA ETERNA, por decisión del usuario del 2026-07-27: sobrevive al
//     reinicio del equipo y del agente.
//
// ⚠️ Consecuencia asumida de esa decisión: el peor caso de la feature pasa a ser
// un equipo que vuelve a sonar solo tras el reinicio y cuyo único interruptor de
// apagado sigue siendo el `DELETE` por NATS. Si el equipo no reconecta nunca, no
// hay forma de callarlo desde la consola. Está aceptado a conciencia: el caso de
// uso es robo, no molestia.
//
// La alarma con DURACIÓN no se persiste. Su plazo se cuenta desde que arrancó, y
// rearmarla tras un reinicio la haría sonar por un tiempo que ya transcurrió.

// AlarmOptions son los modos con que el servidor pide la alarma.
//
// Forever va como campo propio y NO como `Seconds = 0`: PlayAlarm ya interpreta
// `<= 0` como "usa el default de 30 s", así que codificar la eterna con un cero
// daría una alarma de 30 segundos sin ningún aviso.
type AlarmOptions struct {
	Seconds   int
	Forever   bool
	MaxVolume bool
}

// volumeState es el par (nivel, silenciado) del dispositivo de salida por
// omisión, tal como estaba antes de que la alarma lo tocara.
type volumeState struct {
	// Level va de 0 a 1. Se guarda normalizado y no en la escala nativa de cada
	// SO (AppleScript usa 0-100, `IAudioEndpointVolume` 0-1, `amixer` porcentajes)
	// para que el archivo signifique lo mismo en los tres.
	Level float64 `json:"level"`
	Muted bool    `json:"muted"`
	// MuteKnown distingue "no estaba silenciado" de "este dispositivo no expone
	// silencio". En macOS `output muted of (get volume settings)` devuelve
	// `missing value` en varios dispositivos de salida, y en esos el silencio no
	// es un estado aparte: volumen 0 ES el silencio. Con MuteKnown=false la
	// restauración toca sólo el nivel y no inventa un `unmute` que el usuario
	// nunca pidió.
	MuteKnown bool `json:"mute_known"`
	// Known distingue "el usuario tenía el volumen en 0" de "no se pudo leer".
	// Sin este campo, un fallo de lectura restauraría el equipo a volumen 0 —que
	// es el cero de Go— y eso es peor que no restaurar nada: dejaría un equipo
	// mudo sin que nadie lo haya pedido.
	Known bool `json:"known"`
}

// persistedAlarm es lo que queda en disco entre ejecuciones.
//
// Los dos campos son independientes a propósito, y el teardown se apoya en eso:
// primero se reescribe el archivo con Forever=false —para que un corte de luz a
// mitad del apagado no resucite la alarma— conservando el volumen pendiente, y
// sólo se borra el archivo entero cuando la restauración ya ocurrió. Al revés
// —borrar primero— un fallo entre el borrado y la restauración dejaría el equipo
// al 100 % con el valor original perdido.
type persistedAlarm struct {
	// Forever: hay una alarma eterna que debe rearmarse al arrancar.
	Forever bool `json:"forever"`
	// MaxVolume: la alarma corría con sonido máximo.
	MaxVolume bool `json:"max_volume"`
	// Volume: el estado previo, pendiente de restaurar.
	Volume volumeState `json:"volume"`
	// Output: la salida de audio previa, pendiente de restaurar. Se persiste por
	// el mismo motivo que el volumen: si el equipo se reinicia con la alarma
	// eterna sonando, nadie más sabe a qué dispositivo había que volver.
	Output audioOutput `json:"output"`
}

// alarmDataDir es el directorio de los archivos de la alarma: el estado y la sirena
// generada.
//
// En unix es `/opt/observeragent`, que el desinstalador ya borra con RemoveAll: un
// archivo que puede rearmar una alarma no debe sobrevivir a la desinstalación del
// agente. En Windows es `%PROGRAMDATA%\ObserverRMM`, que pese al nombre del campo
// (WinTmpDir) no es un temporal del sistema y persiste entre reinicios.
func (a *Agent) alarmDataDir() string {
	if runtime.GOOS == "windows" {
		return a.WinTmpDir
	}
	return nixAgentDir
}

// alarmStatePath es la ruta del archivo de estado.
func (a *Agent) alarmStatePath() string {
	return filepath.Join(a.alarmDataDir(), "alarm.json")
}

// saveAlarmState escribe el estado de forma atómica.
//
// Temporal + rename y no escritura directa: este archivo se reescribe justo
// antes de matar el árbol de sonido, y un corte a mitad de un `os.WriteFile`
// dejaría un JSON truncado que al arrancar no se puede leer. Un JSON ilegible
// se comporta como "no había nada", o sea que se perdería el volumen guardado.
func (a *Agent) saveAlarmState(st persistedAlarm) {
	path := a.alarmStatePath()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		a.Logger.Debugln("saveAlarmState: no se pudo crear el directorio:", err)
		return
	}

	data, err := json.Marshal(st)
	if err != nil {
		a.Logger.Debugln("saveAlarmState: no se pudo serializar:", err)
		return
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		a.Logger.Debugln("saveAlarmState: no se pudo escribir:", err)
		return
	}

	if err := os.Rename(tmp, path); err != nil {
		a.Logger.Debugln("saveAlarmState: no se pudo renombrar:", err)
		_ = os.Remove(tmp)
	}
}

// loadAlarmState lee el estado. El segundo valor es false cuando no hay nada
// que hacer: archivo ausente, ilegible o sin ningún pendiente.
func (a *Agent) loadAlarmState() (persistedAlarm, bool) {
	var st persistedAlarm

	data, err := os.ReadFile(a.alarmStatePath())
	if err != nil {
		return st, false
	}

	if err := json.Unmarshal(data, &st); err != nil {
		a.Logger.Debugln("loadAlarmState: JSON ilegible, se descarta:", err)
		a.clearAlarmState()
		return st, false
	}

	if !st.Forever && !st.Volume.Known {
		return st, false
	}

	return st, true
}

func (a *Agent) clearAlarmState() {
	if err := os.Remove(a.alarmStatePath()); err != nil && !os.IsNotExist(err) {
		a.Logger.Debugln("clearAlarmState:", err)
	}
}

// ResumeAlarm rearma lo que quedó pendiente de una ejecución anterior. Se llama
// una vez, al arrancar el servicio.
//
// Dos casos, y el orden importa:
//
//  1. Había una alarma ETERNA ⇒ vuelve a sonar. El volumen guardado se REUSA
//     tal cual y NO se vuelve a leer del sistema: tanto Windows como macOS
//     conservan el volumen del dispositivo entre reinicios, así que el equipo
//     sigue al 100 % y una lectura nueva guardaría ese 100 % como si fuera la
//     preferencia del usuario. Ese es justo el "ok falso" que la feature ya
//     conoce: leer una variable que uno mismo contaminó.
//  2. No había alarma pero sí un volumen pendiente ⇒ el agente murió a mitad
//     de alarma. Se restaura y se limpia.
func (a *Agent) ResumeAlarm() {
	st, ok := a.loadAlarmState()
	if !ok {
		return
	}

	if st.Forever {
		a.Logger.Infoln("ResumeAlarm: rearmando la alarma eterna que dejó la ejecución anterior")

		if err := a.playAlarmWithSavedVolume(AlarmOptions{
			Forever:   true,
			MaxVolume: st.MaxVolume,
		}, st.Volume, st.Output); err != nil {
			a.Logger.Errorln("ResumeAlarm:", err.Error())
			// La alarma no arrancó, pero el volumen guardado sigue siendo válido
			// y hay que devolverlo: si no, el equipo queda al máximo y en
			// silencio, que es el peor de los dos mundos.
			a.restoreSavedVolume(st.Volume)
			a.restoreAudioOutput(st.Output)
			a.clearAlarmState()
		}
		return
	}

	a.Logger.Infoln("ResumeAlarm: restaurando el volumen y la salida que dejó una alarma interrumpida")
	a.restoreSavedVolume(st.Volume)
	a.restoreAudioOutput(st.Output)
	a.clearAlarmState()
}
