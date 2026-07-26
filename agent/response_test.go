package agent

import (
	"errors"
	"strings"
	"testing"
)

// Feature 028 · pruebas de las utilidades compartidas de lock / alert / alarm.

func TestTruncateRespetaRunes(t *testing.T) {
	// El caso que motiva la función: la flota es hispanohablante. Recortar por
	// bytes partiría una `ñ` o una vocal acentuada en dos y el diálogo mostraría
	// un carácter de reemplazo al final.
	in := "ñññññ"
	got := truncate(in, 3)

	if got != "ñññ" {
		t.Errorf("truncate(%q, 3) = %q", in, got)
	}
	if !isValidUTF8(got) {
		t.Errorf("el recorte partió un carácter multibyte: %q", got)
	}
}

func TestTruncateNoTocaLoQueCabe(t *testing.T) {
	in := "Equipo bloqueado por el área de TI"
	if got := truncate(in, alertMaxTitleLen); got != in {
		t.Errorf("se modificó un texto que cabía: %q", got)
	}
}

func TestShellQuoteCierraLaInyeccion(t *testing.T) {
	// Aunque hoy sólo se cite rutas de una lista fija, la función tiene que
	// resistir el caso hostil: una comilla simple que intente cerrar el literal.
	got := shellQuote("/tmp/a'; rm -rf /; echo '")

	if strings.HasPrefix(got, "'") == false || strings.HasSuffix(got, "'") == false {
		t.Fatalf("el resultado no quedó entrecomillado: %q", got)
	}
	// Ninguna comilla simple puede quedar suelta: todas tienen que estar
	// escapadas como '\''.
	if strings.Contains(strings.ReplaceAll(got[1:len(got)-1], `'\''`, ""), "'") {
		t.Errorf("quedó una comilla sin escapar: %q", got)
	}
}

func TestFirstExistingDevuelveVacioSiNoHayNada(t *testing.T) {
	if got := firstExisting([]string{"/no/existe/a.wav", "/no/existe/b.oga"}); got != "" {
		t.Errorf("se esperaba cadena vacía, llegó %q", got)
	}
}

func TestFirstExistingEligeElPrimeroQueEsta(t *testing.T) {
	// /etc/hostname existe en cualquier unix; sirve como sustituto de un sonido.
	got := firstExisting([]string{"/no/existe/a.wav", "/etc/hostname"})

	if got != "/etc/hostname" {
		t.Errorf("no eligió la ruta que sí existe: %q", got)
	}
}

func TestShowAlertRechazaMensajeVacio(t *testing.T) {
	// Un `alert` sin cuerpo abriría un diálogo en blanco en el equipo del
	// usuario. Se corta antes de tocar el SO, así que este test corre en
	// cualquier plataforma sin necesidad de sesión gráfica.
	a := &Agent{}

	if err := a.ShowAlert("Título", "   "); err == nil {
		t.Error("se esperaba error con un mensaje que sólo tiene espacios")
	}
}

func TestResponseCodeMapeaLosSentinelas(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, RespOK},
		{errNoUserSession, RespNoUserSession},
		{errNoDialogTool, RespNoDialogTool},
		{errNoAudioPlayer, RespNoAudioPlayer},
		{errLockUnavailable, RespLockUnavailable},
		{errEmptyMessage, RespEmptyMessage},
	}

	for _, c := range cases {
		if got := ResponseCode(c.err); got != c.want {
			t.Errorf("ResponseCode(%v) = %q, se esperaba %q", c.err, got, c.want)
		}
	}
}

func TestResponseCodeNoFiltraTextoDesconocido(t *testing.T) {
	// Un error inesperado tiene que colapsar a "error" y NO devolver su texto.
	// Es la garantía que sostiene el i18n: si un mensaje en español del agente se
	// escapara hasta la consola, se le mostraría sin traducir a un operador que la
	// tiene en inglés.
	got := ResponseCode(errors.New("fallo interno con detalles en español"))

	if got != RespError {
		t.Errorf("ResponseCode devolvió %q en vez de %q", got, RespError)
	}
	if strings.Contains(got, "español") {
		t.Errorf("se filtró el texto del error al servidor: %q", got)
	}
}

// TestCodigosSonEstables fija los códigos por contrato: son claves de i18n en la
// consola (`es.json`/`en.json`). Renombrar uno acá sin tocar el frontend deja al
// operador viendo el código crudo en pantalla.
func TestCodigosSonEstables(t *testing.T) {
	want := map[string]string{
		"ok":               RespOK,
		"no_user_session":  RespNoUserSession,
		"no_dialog_tool":   RespNoDialogTool,
		"no_audio_player":  RespNoAudioPlayer,
		"lock_unavailable": RespLockUnavailable,
		"empty_message":    RespEmptyMessage,
		"error":            RespError,
	}

	for literal, constant := range want {
		if literal != constant {
			t.Errorf("el código cambió: se esperaba %q y la constante vale %q", literal, constant)
		}
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}
