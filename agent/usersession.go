package agent

// Feature 028 · la regla para elegir a QUÉ sesión de Windows le hablamos.
//
// Este archivo no tiene etiqueta de plataforma, y es a propósito. La regla es lo
// que se equivocó una vez, y la CI corre `go test` sólo en Linux y macOS
// (`.github/workflows/ci.yml`): una prueba en un archivo `//go:build windows` se
// compilaría y NUNCA se ejecutaría, que es cobertura de mentira —el mismo hueco que
// tenía el gate `lint:i18n`, que recorría un archivo sin aplicarle las reglas—.
//
// Así que acá vive la DECISIÓN, portable y probada; en `usersession_windows.go`
// queda sólo la llamada a la API, que es lo que de verdad necesita Windows.

// wtsSession es una sesión de Terminal Services reducida a lo que hace falta para
// elegirla. `User` viene vacío cuando la sesión no tiene a nadie dentro.
type wtsSession struct {
	ID     uint32
	Active bool
	User   string
}

// pickUserSession elige la sesión a la que se le puede mandar un mensaje, bloquear
// o hacer sonar una alarma.
//
// Dos condiciones, y las dos hacen falta:
//
//   - Activa. Una sesión desconectada es un usuario que dejó su sesión abierta y se
//     fue; no hay pantalla donde dibujar.
//   - CON USUARIO. Una sesión puede estar activa mostrando la pantalla de inicio de
//     sesión, sin nadie adentro. Mandarle un mensaje o bloquearla no significa nada,
//     y responderle `ok` al operador sería mentirle.
//
// ⚠️ La segunda condición es la que se había perdido a medias. `wintoken.GetInteractiveToken`
// —que era quien elegía la sesión para `lock` y `alarm`— toma **la primera activa sin
// mirar si hay alguien adentro**. En un equipo con la consola en la pantalla de inicio
// de sesión y la persona conectada por RDP, `alert` iba a la sesión correcta y
// `lock`/`alarm` contestaban `no_user_session`: tres acciones, dos respuestas
// distintas, sobre el mismo equipo con un usuario dentro.
//
// Entre dos candidatas válidas se prefiere la de la CONSOLA. Puede haber más de una
// —el usuario del teclado y otro por RDP— y el orden en que las devuelve
// WTSEnumerateSessions no está especificado, así que sin este criterio la elección
// sería arbitraria. La consola es la correcta para lo que estas acciones existen: la
// persona que tiene el equipo en las manos.
func pickUserSession(sessions []wtsSession, consoleID uint32) (uint32, string, bool) {
	var (
		id    uint32
		user  string
		found bool
	)

	for _, s := range sessions {
		if !s.Active || s.User == "" {
			continue
		}

		// La consola gana de inmediato; no hace falta seguir recorriendo.
		if s.ID == consoleID {
			return s.ID, s.User, true
		}

		// La primera válida que no sea la consola queda como respaldo. Es el caso del
		// servidor administrado por RDP, donde nadie está frente al teclado.
		if !found {
			id, user, found = s.ID, s.User, true
		}
	}

	return id, user, found
}
