//go:build windows

package agent

import "errors"

// Windows: la redirección a los parlantes internos NO está implementada, y es una
// decisión, no un olvido.
//
// La API para cambiar el dispositivo de salida por omisión —`IPolicyConfig`— **no
// está documentada por Microsoft**: no aparece en el SDK, su IID cambió entre
// versiones de Windows y su contrato puede romperse en cualquier actualización.
// Escribirla exige medirla, y el 2026-07-28 se verificó que **ninguno de los
// equipos Windows disponibles tiene un segundo dispositivo de salida**
// (`LAPTOP-DSHQMP1O` sólo expone "Altavoces (Realtek(R) Audio)"), así que el
// camino del cambio sería código no ejercitado en ninguna parte, apoyado en una
// interfaz sin contrato. Esta feature lleva ocho defectos encontrados por medir lo
// que se había deducido; agregar un noveno a ciegas no corresponde.
//
// La degradación es honesta y el comportamiento observable no cambia: la alarma
// suena por la salida que tenga el equipo, que es exactamente lo que hacía antes.
// Cuando haya un Windows con audífonos y parlantes a la vez, se mide y se escribe.

var errSalidaNoSoportadaWindows = errors.New(
	"mover la salida de audio en Windows exige IPolicyConfig, que no está documentada y no se pudo medir")

func (a *Agent) defaultAudioOutput() (audioOutput, error) {
	return audioOutput{}, errSalidaNoSoportadaWindows
}

func (a *Agent) internalSpeakerOutput() (audioOutput, error) {
	return audioOutput{}, errSalidaNoSoportadaWindows
}

func (a *Agent) setDefaultAudioOutput(audioOutput) error {
	return errSalidaNoSoportadaWindows
}
