package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// Feature 030 · Fase 2 · T016 — pruebas del contrato con el ayudante Aqua.
//
// Corren en Linux, que es donde la CI ejecuta `go test`. Eso no es una
// casualidad ni un descuido: es la razón entera de que webcamaqua.go no lleve
// etiqueta de plataforma. Lo que se prueba acá es el texto del plist y el
// formato de la respuesta, o sea las dos cosas que en un equipo real fallan sin
// hacer ruido — un plist mal armado no carga y `kickstart` dice que no existe el
// trabajo; una respuesta mal leída deja una fila de evidencia sin foto.

func TestPlistDelAyudanteApuntaAlBinarioInstalado(t *testing.T) {
	contenido := webcamAquaPlistContenido(nixAgentBin)

	// La ruta es LO que hace que el permiso de TCC sirva: TCC indexa a un
	// ejecutable suelto por su ruta, así que el ayudante comparte la fila del
	// servicio sólo si es el mismo binario en el mismo sitio. Una copia en otro
	// lado compilaría igual y pediría el permiso de nuevo.
	if !strings.Contains(contenido, "<string>"+nixAgentBin+"</string>") {
		t.Fatalf("el plist no apunta a %s:\n%s", nixAgentBin, contenido)
	}

	if !strings.Contains(contenido, "<string>"+modoWebcamAqua+"</string>") {
		t.Fatalf("el plist no invoca el modo %q:\n%s", modoWebcamAqua, contenido)
	}

	// El ayudante corre sin privilegios y el log por omision es de root: sin
	// esto, cada linea de log es un error de escritura contra
	// /var/log/observeragent.log.
	if !strings.Contains(contenido, "<string>-logto</string>") ||
		!strings.Contains(contenido, "<string>stdout</string>") {
		t.Error("el ayudante debe loguear a stdout: no puede escribir el log de root")
	}

	if !strings.Contains(contenido, "<string>"+webcamAquaLabel+"</string>") {
		t.Fatalf("el plist no lleva el Label %q", webcamAquaLabel)
	}
}

func TestPlistDelAyudanteNoEsResidente(t *testing.T) {
	contenido := webcamAquaPlistContenido(nixAgentBin)

	// Un ayudante con acceso a la cámara colgado permanentemente de la sesión de
	// cada usuario es exactamente lo que ADR-025 no quiere prometer. Sólo corre
	// cuando el servicio lo despierta.
	for _, clave := range []string{"RunAtLoad", "KeepAlive"} {
		i := strings.Index(contenido, "<key>"+clave+"</key>")
		if i < 0 {
			t.Fatalf("falta la clave %s en el plist", clave)
		}
		resto := contenido[i:]
		if len(resto) > 80 {
			resto = resto[:80]
		}
		if !strings.Contains(resto, "<false/>") {
			t.Errorf("%s deberia ser false; el ayudante no es un residente", clave)
		}
	}

	if !strings.Contains(contenido, "<string>Aqua</string>") {
		t.Error("falta LimitLoadToSessionType=Aqua, que es la razon de existir del ayudante")
	}
}

func TestPlistDelAyudanteNoRedirigeSalida(t *testing.T) {
	contenido := webcamAquaPlistContenido(nixAgentBin)

	// launchd abre esos archivos con el usuario del trabajo: en un Mac con dos
	// cuentas el segundo usuario se topa con un archivo que no puede abrir. Ver
	// la nota "una que se descartó" en webcamAquaPlistContenido.
	for _, clave := range []string{"StandardOutPath", "StandardErrorPath"} {
		if strings.Contains(contenido, clave) {
			t.Errorf("el plist no debe llevar %s: rompe la captura del segundo usuario", clave)
		}
	}
}

func TestRespuestaAquaSoloAceptaElPedidoEnCurso(t *testing.T) {
	// `kickstart` vuelve en cuanto launchd acepta el encargo, no cuando el
	// trabajo termina. Sin el nonce, la respuesta de un ciclo anterior se leeria
	// como la de este y se subiria la foto de hace cinco minutos con la marca de
	// tiempo de ahora.
	buena, err := json.Marshal(webcamAquaRespuesta{ID: "abc123"})
	if err != nil {
		t.Fatal(err)
	}

	casos := []struct {
		nombre string
		datos  string
		id     string
		motivo string
		valida bool
	}{
		{"exito", string(buena), "abc123", "", true},
		{"motivo del ayudante", `{"id":"abc123","motivo":"permiso_denegado"}`, "abc123", webcamDenied, true},
		{"nonce ajeno", `{"id":"otro","motivo":""}`, "abc123", "", false},
		{"sin nonce", `{"motivo":""}`, "abc123", "", false},
		{"json a medio escribir", `{"id":"abc12`, "abc123", "", false},
		{"archivo vacio", ``, "abc123", "", false},
		{"motivo con espacios", `{"id":"abc123","motivo":"  camara_ocupada  "}`, "abc123", webcamBusy, true},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			r, valida := parsearRespuestaAqua([]byte(c.datos), c.id)
			if valida != c.valida {
				t.Fatalf("valida = %v, se esperaba %v", valida, c.valida)
			}
			if r.Motivo != c.motivo {
				t.Errorf("motivo = %q, se esperaba %q", r.Motivo, c.motivo)
			}
		})
	}
}

func TestBuzonDelAyudanteNoViveEnTmpCompartido(t *testing.T) {
	// Por ahi viaja la ruta de un archivo que el servicio va a subir como
	// evidencia. En `/tmp`, que cualquiera puede escribir, otro usuario del
	// equipo podria dejar un pedido o una respuesta falsa.
	if !strings.HasPrefix(webcamAquaDir, nixAgentDir+"/") {
		t.Fatalf("el buzon quedo fuera del directorio del agente: %s", webcamAquaDir)
	}
	if strings.HasPrefix(webcamAquaDir, "/tmp") || strings.HasPrefix(webcamAquaDir, "/private/tmp") {
		t.Fatalf("el buzon no puede vivir en un directorio escribible por cualquiera: %s", webcamAquaDir)
	}
}

func TestModoDelAyudanteNoSeLlamaComoElInstrumento(t *testing.T) {
	// Si fueran el mismo nombre, el ayudante se despertaria a si mismo: en macOS
	// `-m webcamcapture` pasa por el camino de produccion, que es despertar al
	// ayudante. Bucle infinito y ningun error visible.
	if modoWebcamAqua == modoCapturaPantalla {
		t.Fatal("el modo del ayudante colisiona con el de la captura de pantalla")
	}
	if modoWebcamAqua == "webcamcapture" {
		t.Fatal("el ayudante se despertaria a si mismo en un bucle")
	}
}
