//go:build darwin
// +build darwin

package agent

import (
	"context"
	"time"
)

// Feature 028 · Fase 2 — volumen de salida en macOS.
//
// Se hace con AppleScript y no con CoreAudio por cgo: `set volume` es la misma
// vía que usa Prey (`osascript -e "set Volume 10"`) y evita atar esta parte a
// `CGO_ENABLED=1`. La geolocalización nativa ya obliga a compilar macOS con cgo
// (feature 025), pero el volumen no tiene por qué sumarse a esa dependencia: un
// binario `CGO_ENABLED=0` pierde CoreLocation y debe conservar la alarma entera.
//
// Corre dentro de la sesión del usuario de consola —`launchctl asuser` + `sudo
// -u`— porque el volumen de salida es una preferencia por usuario: desde la
// sesión del sistema el `osascript` no tiene a quién aplicárselo.
//
// El parseo de la salida y la construcción del script viven en `volumeparse.go`,
// sin etiqueta de plataforma, para que se puedan probar fuera de un Mac.

// volumeCmdTimeout acota cada invocación. Un `osascript` puede colgarse fuera de
// la sesión gráfica —ya pasó con `lsappinfo` en esta misma feature— y esto corre
// una vez por segundo mientras la alarma suena: sin plazo, un cuelgue acumularía
// un proceso nuevo cada segundo.
const volumeCmdTimeout = 10 * time.Second

func (a *Agent) readVolume() (volumeState, error) {
	ctx, cancel := context.WithTimeout(context.Background(), volumeCmdTimeout)
	defer cancel()

	// Una sola invocación para las dos lecturas: leerlas por separado abre una
	// ventana en la que el usuario cambia una y no la otra, y deja guardado como
	// "estado previo" un par que nunca existió.
	cmd, err := a.newUserSessionCmd(ctx, "/usr/bin/osascript",
		"-e", "set s to (get volume settings)",
		"-e", `return ((output volume of s) as text) & "," & ((output muted of s) as text)`)
	if err != nil {
		return volumeState{}, err
	}

	out, err := cmd.Output()
	if err != nil {
		return volumeState{}, err
	}

	return parseMacVolume(string(out))
}

func (a *Agent) setVolume(v volumeState) error {
	ctx, cancel := context.WithTimeout(context.Background(), volumeCmdTimeout)
	defer cancel()

	cmd, err := a.newUserSessionCmd(ctx, "/usr/bin/osascript", "-e", macSetVolumeScript(v))
	if err != nil {
		return err
	}

	return cmd.Run()
}
