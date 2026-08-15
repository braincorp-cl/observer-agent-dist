package agent

import (
	"encoding/json"
	"os"
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
	contenido := capturaAquaPlistContenido()

	// 🔑 El programa tiene que ser el ejecutable DENTRO DEL BUNDLE. Apuntar al
	// binario del servicio dejaria al ayudante sin identidad de bundle, y con
	// ella se pierde lo unico que hace reparable su permiso: que `tccutil` pueda
	// nombrarlo. Compilaria igual y fallaria en el primer update.
	if !strings.Contains(contenido, "<string>"+capturaAquaAppExe+"</string>") {
		t.Fatalf("el plist no apunta al ejecutable del bundle (%s):\n%s", capturaAquaAppExe, contenido)
	}
	if strings.Contains(contenido, "<string>"+nixAgentBin+"</string>") {
		t.Fatal("el plist apunta al binario del servicio: el ayudante perderia su identidad de bundle")
	}

	if !strings.Contains(contenido, "<string>"+modoCapturaAqua+"</string>") {
		t.Fatalf("el plist no invoca el modo %q:\n%s", modoCapturaAqua, contenido)
	}

	// El ayudante corre sin privilegios y el log por omision es de root: sin
	// esto, cada linea de log es un error de escritura contra
	// /var/log/observeragent.log.
	if !strings.Contains(contenido, "<string>-logto</string>") ||
		!strings.Contains(contenido, "<string>stdout</string>") {
		t.Error("el ayudante debe loguear a stdout: no puede escribir el log de root")
	}

	if !strings.Contains(contenido, "<string>"+capturaAquaID+"</string>") {
		t.Fatalf("el plist no lleva el Label %q", capturaAquaID)
	}
}

func TestPlistDelAyudanteNoEsResidente(t *testing.T) {
	contenido := capturaAquaPlistContenido()

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
	contenido := capturaAquaPlistContenido()

	// launchd abre esos archivos con el usuario del trabajo: en un Mac con dos
	// cuentas el segundo usuario se topa con un archivo que no puede abrir. Ver
	// la nota "una que se descartó" en capturaAquaPlistContenido.
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
	buena, err := json.Marshal(capturaAquaRespuesta{ID: "abc123"})
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

func TestInfoPlistDelBundleTieneLoQueMacOSExige(t *testing.T) {
	info := capturaAquaInfoPlist()

	// Sin CFBundleIdentifier no hay bundle, y sin bundle `tccutil` no puede
	// nombrar al ayudante: el permiso roto seria definitivo.
	if !strings.Contains(info, "<string>"+capturaAquaID+"</string>") {
		t.Fatalf("el Info.plist no declara el identificador %q", capturaAquaID)
	}
	// macOS EXIGE esta clave para poder pedir la camara: sin ella mata al proceso
	// que lo pide, sin dejar rastro util.
	if !strings.Contains(info, "NSCameraUsageDescription") {
		t.Error("falta NSCameraUsageDescription: macOS mata al proceso que pide la camara sin ella")
	}
	// Un icono apareciendo en el Dock en cada ciclo avisa a quien tenga el equipo.
	if !strings.Contains(info, "LSUIElement") {
		t.Error("falta LSUIElement: el ayudante no debe aparecer en el Dock")
	}
	if !strings.Contains(info, "<key>CFBundleExecutable</key>") {
		t.Error("falta CFBundleExecutable")
	}
}

func TestBuzonDelAyudanteNoViveEnTmpCompartido(t *testing.T) {
	// Por ahi viaja la ruta de un archivo que el servicio va a subir como
	// evidencia. En `/tmp`, que cualquiera puede escribir, otro usuario del
	// equipo podria dejar un pedido o una respuesta falsa.
	if !strings.HasPrefix(capturaAquaDir, nixAgentDir+"/") {
		t.Fatalf("el buzon quedo fuera del directorio del agente: %s", capturaAquaDir)
	}
	if strings.HasPrefix(capturaAquaDir, "/tmp") || strings.HasPrefix(capturaAquaDir, "/private/tmp") {
		t.Fatalf("el buzon no puede vivir en un directorio escribible por cualquiera: %s", capturaAquaDir)
	}
}

func TestModoDelAyudanteNoSeLlamaComoElInstrumento(t *testing.T) {
	// Si fueran el mismo nombre, el ayudante se despertaria a si mismo: en macOS
	// `-m webcamcapture` pasa por el camino de produccion, que es despertar al
	// ayudante. Bucle infinito y ningun error visible.
	if modoCapturaAqua == modoCapturaPantalla {
		t.Fatal("el modo del ayudante colisiona con el de la captura de pantalla")
	}
	for _, instrumento := range []string{"webcamcapture", "screencapture"} {
		if modoCapturaAqua == instrumento {
			t.Fatalf("el ayudante se despertaria a si mismo en un bucle (%s)", instrumento)
		}
	}
}

// TestObjetivoCDelAyudanteConsultaSinPedir es el guardian que reemplaza al de los
// scripts de AppleScript que T016 elimino.
//
// Lee el `.m` como TEXTO porque la CI corre en Linux y ahi ese archivo no se
// compila: sin esto, el unico veredicto sobre el codigo que decide si se abre un
// dialogo llegaria del job de macOS, y solo cuando alguien lo mire.
//
// Lo que protege es la distincion mas cara de esta feature: CONSULTAR el permiso
// y PEDIRLO son dos operaciones distintas, y confundirlas abre un dialogo del
// sistema en la cara de quien tiene un equipo robado.
func TestObjetivoCDelAyudanteConsultaSinPedir(t *testing.T) {
	fuente, err := os.ReadFile("captura_darwin.m")
	if err != nil {
		t.Fatalf("no se pudo leer el Objective-C del ayudante: %v", err)
	}
	m := string(fuente)

	for _, pieza := range []string{
		"authorizationStatusForMediaType:", // el estado de la camara
		"AVMediaTypeVideo",
		"CGPreflightScreenCaptureAccess", // el estado de la pantalla
		"CGRequestScreenCaptureAccess",   // el pedido, que va aparte
	} {
		if !strings.Contains(m, pieza) {
			t.Errorf("el ayudante ya no usa %q", pieza)
		}
	}

	// La captura NO puede pedir permiso: si `observer_webcam_capturar` llamara a
	// `requestAccessForMediaType`, un ciclo de modo perdido mostraria el dialogo
	// y delataria al ladron. El pedido vive en el otro archivo, detras de las
	// guardias de la ronda.
	captura := m[strings.Index(m, "int observer_webcam_capturar("):]
	if strings.Contains(captura, "requestAccessForMediaType") {
		t.Error("la captura pide el permiso: abriria un dialogo durante un ciclo de modo perdido")
	}
}
