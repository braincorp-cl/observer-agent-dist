//go:build darwin && !cgo

package agent

import "errors"

// Sin cgo no hay CoreAudio, así que no hay forma de mover la salida de audio.
// El agente de producción se compila con CGO_ENABLED=1 (lo exige CoreLocation);
// esto existe para que el paquete siga compilando en un build sin cgo, igual que
// `geolocation_nocgo_darwin.go`.
//
// Devolver error acá NO cancela la alarma: `switchOutputToInternalSpeakers` lo
// registra y sigue, y la alarma suena por donde esté saliendo el audio.

var errSinCoreAudio = errors.New("compilado sin cgo: no hay CoreAudio para mover la salida de audio")

func (a *Agent) defaultAudioOutput() (audioOutput, error) { return audioOutput{}, errSinCoreAudio }

func (a *Agent) internalSpeakerOutput() (audioOutput, error) { return audioOutput{}, errSinCoreAudio }

func (a *Agent) setDefaultAudioOutput(audioOutput) error { return errSinCoreAudio }
