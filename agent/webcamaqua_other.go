//go:build !darwin
// +build !darwin

package agent

// Feature 030 · Fase 2 · T016 — el ayudante Aqua sólo existe en macOS.
//
// El despacho de modos de main.go es portable y no lleva etiquetas de
// plataforma, así que necesita que el método exista en todas. En el resto de los
// sistemas el agente saca la foto en su propio proceso y no hay ningún ayudante
// que despertar: este modo no tiene nada que hacer y no imprime nada, porque
// nadie lo va a invocar — lo despierta launchd, que sólo existe en macOS.
func (a *Agent) RunWebcamAqua() {}

// descargarAyudanteCamara no tiene nada que descargar fuera de macOS.
func (a *Agent) descargarAyudanteCamara() {}
