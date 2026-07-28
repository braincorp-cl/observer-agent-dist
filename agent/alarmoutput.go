package agent

// Feature 028 · Fase 2 — a qué parlante sale la alarma.
//
// 📌 El motivo, y salió de una medición: el 2026-07-28 la alarma sonó **al 100 %
// en los AirPods** del dueño del equipo, porque eran su salida por omisión. En un
// equipo cuya salida son audífonos, "sonido máximo" es lo peor de los dos mundos:
// **no sirve para encontrar el equipo** —el sonido va a los oídos de quien los
// lleve, no a la sala— y **puede lastimar a esa persona**. Prey tiene el mismo
// problema y tampoco lo trata.
//
// ⚠️ El cambio de salida va **ANTES** de lanzar el reproductor, al revés que el
// volumen. Un flujo de audio ya abierto se queda en el dispositivo con el que
// nació: cambiar la salida después movería los sonidos del sistema y dejaría la
// alarma sonando igual en los audífonos.
//
// Misma disciplina que el volumen: se guarda lo que había, se restaura en el
// mismo teardown, y **no poder cambiarla NO cancela la alarma** — un equipo que
// se está buscando tiene que sonar aunque la salida no se pueda mover.

// audioOutput identifica un dispositivo de salida. El `ID` es opaco y sólo lo
// entiende la implementación de cada plataforma: un `AudioDeviceID` en macOS, el
// `node.name` de PipeWire en Linux.
type audioOutput struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Known distingue "no había nada que cambiar" de "esta es la salida previa".
	// Sin esto, restaurar con el cero de Go dejaría el equipo apuntando a un
	// dispositivo inexistente.
	Known bool `json:"known"`
}

// switchOutputToInternalSpeakers pone la salida por omisión en los parlantes
// internos y devuelve la que había, para poder volver.
//
// Devuelve un `audioOutput` vacío —o sea `Known=false`— en todos los casos en que
// no hay nada que restaurar: si no se pudo averiguar la salida actual, si el
// equipo no tiene parlantes internos reconocibles, si **ya** estaba en ellos, o si
// el cambio falló. Que devuelva vacío no es un error: es "no toqué nada".
func (a *Agent) switchOutputToInternalSpeakers() audioOutput {
	prev, err := a.defaultAudioOutput()
	if err != nil {
		a.Logger.Debugln("switchOutputToInternalSpeakers: no se pudo leer la salida actual:", err)
		return audioOutput{}
	}

	internal, err := a.internalSpeakerOutput()
	if err != nil {
		a.Logger.Debugln("switchOutputToInternalSpeakers: no se encontraron parlantes internos:", err)
		return audioOutput{}
	}

	// Ya está donde queremos: no se toca nada y, sobre todo, no se devuelve un
	// estado "previo" que después restauraría un cambio que nunca ocurrió.
	if internal.ID == prev.ID {
		a.Logger.Debugln("switchOutputToInternalSpeakers: la salida ya son los parlantes internos:", prev.Name)
		return audioOutput{}
	}

	if err := a.setDefaultAudioOutput(internal); err != nil {
		a.Logger.Debugln("switchOutputToInternalSpeakers: no se pudo cambiar la salida:", err)
		return audioOutput{}
	}

	a.Logger.Debugf("switchOutputToInternalSpeakers: salida movida de %q a %q para la alarma",
		prev.Name, internal.Name)
	return prev
}

// restoreAudioOutput devuelve la salida a donde estaba. Sin estado conocido no
// hace nada: mejor dejar la salida donde está que apuntarla a un dispositivo que
// quizá ya no existe —unos audífonos se desconectan—.
func (a *Agent) restoreAudioOutput(o audioOutput) {
	if !o.Known {
		return
	}

	if err := a.restoreDefaultAudioOutput(o); err != nil {
		// En Error y no en Debug: haberle movido la salida de audio a alguien y no
		// habérsela devuelto es justo lo que hay que poder leer después.
		a.Logger.Errorln("restoreAudioOutput: no se pudo devolver la salida previa:", err)
		return
	}

	a.Logger.Debugf("restoreAudioOutput: salida devuelta a %q", o.Name)
}

// restoreDefaultAudioOutput tolera que el dispositivo previo haya desaparecido
// mientras sonaba la alarma, que es el caso normal con audífonos Bluetooth: se
// apagan, se alejan, se los lleva otra persona. Que no se pueda volver a un
// dispositivo ausente no es una falla que valga la pena gritar dos veces.
func (a *Agent) restoreDefaultAudioOutput(o audioOutput) error {
	return a.setDefaultAudioOutput(o)
}
