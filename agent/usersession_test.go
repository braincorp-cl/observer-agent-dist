package agent

import "testing"

// Feature 028 · pruebas de la regla que elige la sesión de Windows.
//
// Sin etiqueta de plataforma a propósito: la CI corre `go test` en Linux y macOS, así
// que una prueba marcada `//go:build windows` se compilaría y no se ejecutaría nunca.
// La regla es portable; la API no.

func TestSeEligeLaSesionConUsuarioYNoLaPantallaDeLogin(t *testing.T) {
	// El caso exacto de la inconsistencia: la consola está en la pantalla de inicio
	// de sesión —activa, sin nadie adentro— y la persona trabaja por RDP.
	// `wintoken.GetInteractiveToken` tomaba la sesión 1 y `lock`/`alarm` respondían
	// `no_user_session` con un usuario dentro del equipo.
	sesiones := []wtsSession{
		{ID: 1, Active: true, User: ""},        // consola, pantalla de login
		{ID: 2, Active: true, User: "jtamayo"}, // la persona, por RDP
	}

	id, user, ok := pickUserSession(sesiones, 1)

	if !ok {
		t.Fatal("no se eligió ninguna sesión, y había un usuario conectado por RDP")
	}
	if id != 2 || user != "jtamayo" {
		t.Errorf("se eligió la sesión %d de %q; se esperaba la 2 de jtamayo", id, user)
	}
}

func TestSeIgnoranLasSesionesDesconectadas(t *testing.T) {
	// Un usuario que dejó su sesión abierta y se fue: no hay pantalla donde dibujar.
	sesiones := []wtsSession{
		{ID: 3, Active: false, User: "quien.se.fue"},
	}

	if _, _, ok := pickUserSession(sesiones, 1); ok {
		t.Error("se eligió una sesión desconectada")
	}
}

func TestSinNadieAdentroNoSeEligeNada(t *testing.T) {
	// Equipo en la pantalla de inicio de sesión. Tiene que fallar: responder `ok`
	// sobre un mensaje que nadie vio, o sobre una pantalla que ya estaba cerrada, es
	// la mentira que los códigos de error existen para evitar.
	sesiones := []wtsSession{
		{ID: 0, Active: false, User: ""}, // la sesión de los servicios
		{ID: 1, Active: true, User: ""},  // consola, pantalla de login
	}

	if id, user, ok := pickUserSession(sesiones, 1); ok {
		t.Errorf("se eligió la sesión %d de %q sin nadie adentro", id, user)
	}
}

// Entre dos sesiones válidas gana la consola: es la persona que tiene el equipo en
// las manos, que es para quien existen el bloqueo y la alarma. El orden en que
// WTSEnumerateSessions devuelve las sesiones no está especificado, así que la prueba
// pone la consola SEGUNDA — si la regla fuera "la primera válida", elegiría mal.
func TestEntreDosValidasGanaLaConsola(t *testing.T) {
	sesiones := []wtsSession{
		{ID: 5, Active: true, User: "soporte.remoto"}, // RDP, primera en la lista
		{ID: 1, Active: true, User: "duena.del.equipo"},
	}

	id, user, ok := pickUserSession(sesiones, 1)

	if !ok {
		t.Fatal("no se eligió ninguna sesión")
	}
	if id != 1 || user != "duena.del.equipo" {
		t.Errorf("se eligió la sesión %d de %q; la consola es la 1", id, user)
	}
}

// Y si la consola no tiene a nadie, la de RDP sirve igual: es el caso del servidor
// administrado en remoto, donde nunca hay nadie frente al teclado.
func TestSiLaConsolaEstaVaciaSirveLaDeRdp(t *testing.T) {
	sesiones := []wtsSession{
		{ID: 1, Active: true, User: ""},
		{ID: 7, Active: true, User: "admin"},
	}

	id, user, ok := pickUserSession(sesiones, 1)

	if !ok || id != 7 || user != "admin" {
		t.Errorf("pickUserSession = (%d, %q, %v); se esperaba la sesión 7 de admin", id, user, ok)
	}
}

func TestSinSesionesNoHayNadaQueElegir(t *testing.T) {
	if _, _, ok := pickUserSession(nil, 1); ok {
		t.Error("se eligió una sesión de una lista vacía")
	}
}
