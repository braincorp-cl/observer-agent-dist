//go:build darwin
// +build darwin

package agent

// Feature 030 · Fase 1 · captura de pantalla en macOS (T013), rehecha en T016.
//
// EL MURO, dicho desde el día uno (decisión 2 del jefe, 2026-07-28): en macOS
// ≥ 10.15 la captura de pantalla exige el permiso TCC "Grabación de pantalla",
// y ese permiso **ningún MDM lo puede conceder**. El perfil PPPC de Apple sólo
// sabe NEGAR pantalla, cámara y micrófono; concederlos requiere que una persona
// lo haga a mano, frente al equipo.
//
// Y el modo en que no funciona es el peligroso — `screencapture` **no falla**:
// devuelve 0 y escribe un PNG con el fondo de escritorio y sin una sola ventana,
// o directamente negro. Un caso alimentado con eso parece tener evidencia y no
// la tiene.
//
// Por eso acá hay DOS verificaciones y no una:
//
//  1. `CGPreflightScreenCaptureAccess`, ANTES de capturar: si el permiso no está
//     concedido se reporta `permiso_denegado` y no se captura nada. Vive dentro
//     del ayudante, que es quien tiene la identidad que TCC juzga.
//  2. La verificación de imagen en negro que hace `CaptureScreen` para los tres
//     SO, DESPUÉS: por si la primera dijera que sí y el resultado igual saliera
//     vacío.
//
// ✅ CON TERRENO desde el 2026-08-13, y no salió como se esperaba: el permiso SÍ
// se puede conceder, pero concedido a un ejecutable suelto **muere en la
// siguiente actualización** y deja una fila que no se puede arreglar. Eso es lo
// que resolvió T016 moviendo la captura a un bundle con identificador propio.

// capturarPantalla se la encarga al ayudante Aqua.
//
// 🔑 EL CAMBIO DE T016, Y NO ES SÓLO DE PLOMERÍA: lo que se movió es el SUJETO
// del permiso. Antes `screencapture` corría como hijo del servicio vía
// `launchctl asuser`, así que TCC lo juzgaba como
// `/opt/observeragent/observeragent` — un ejecutable suelto, cuya fila de
// `access` se rompe en cada actualización y que **no se puede reparar**, porque
// `tccutil` no sabe nombrar a algo que no es un `.app` y SIP impide borrar la
// fila a mano. Medido el 2026-08-15 en el Mac de terreno: la fila decía
// "permitido" con el csreq de julio, Ajustes mostraba el interruptor encendido y
// la captura devolvía `could not create image from display`.
//
// Ahora el sujeto es el bundle del ayudante, que tiene identificador propio.
// Cuesta una concesión —la primera vez hay que autorizarlo a él— y a cambio el
// permiso pasa a ser reparable para siempre. Ver capturaaqua.go.
func (a *Agent) capturarPantalla(nombre string) ScreenShot {
	ruta, motivo := a.capturarPorAyudante(capturaTipoPantalla, nombre)
	if motivo != "" {
		return ScreenShot{Reason: motivo}
	}
	return ScreenShot{Path: ruta}
}

// motivoDePantallaNegra: en macOS el caso "negra" casi siempre es el permiso
// TCC, y ese ya se detecta ANTES de capturar (el preflight del ayudante). Lo que
// quede después es negro sin explicación conocida, así que se reporta como tal
// en vez de adivinar.
func (a *Agent) motivoDePantallaNegra() string {
	return captureBlank
}

// usuarioDeLaSesionGrafica devuelve el usuario de consola (el de /dev/console),
// que es quien tiene la pantalla que se captura.
func (a *Agent) usuarioDeLaSesionGrafica() string {
	_, username, err := consoleUser()
	if err != nil {
		return ""
	}
	return username
}
