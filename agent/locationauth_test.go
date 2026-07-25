package agent

import (
	"strings"
	"testing"
)

// Feature 025 · tests de la manipulación de clients.plist.
//
// Es la pieza más delicada de la feature: se edita un archivo del sistema en
// macOS. Los tests viven en un archivo sin build tag a propósito, para que
// corran en CI Linux aunque el proveedor real solo compile en darwin+cgo.

const testBundleID = "cl.observer.agent"

// entrada tal como la escribe locationd para un binario sin bundle .app:
// clave "<uuid>:i<bundleid>:", Requirement por cdhash y un blob <data>.
const clientsFixture = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>4DF05D9A-961D-4729-9A94-E07148506673:icom.apple.Safari:</key>
	<dict>
		<key>BundleId</key>
		<string>com.apple.Safari</string>
		<key>Authorized</key>
		<true/>
	</dict>
	<key>4DF05D9A-961D-4729-9A94-E07148506673:icl.observer.agent:</key>
	<dict>
		<key>BundleId</key>
		<string>cl.observer.agent</string>
		<key>ClientStorageToken</key>
		<data>
		9s02nUDaq+1z/0bTHPEBqg==
		</data>
		<key>Executable</key>
		<string>/usr/local/bin/observer-agent</string>
		<key>Registered</key>
		<true/>
		<key>Requirement</key>
		<string>cdhash H"7f15b4d58e85c29035fb9c41570c53eca57510f9"</string>
	</dict>
	<key>MigrationVersionNumber</key>
	<real>20.1</real>
</dict>
</plist>
`

func TestAuthorizeClientsConcedeElPermiso(t *testing.T) {
	out, changed := authorizeClients(clientsFixture, testBundleID)
	if changed != 1 {
		t.Fatalf("esperaba 1 entrada modificada, hubo %d", changed)
	}
	if !strings.Contains(out, "<key>Authorized</key>\n\t\t<true/>") {
		t.Error("no se insertó Authorized=true")
	}
	// El blob binario y el resto de los campos deben sobrevivir intactos: es la
	// razón de editar el XML como texto en vez de reserializar el documento.
	if !strings.Contains(out, "9s02nUDaq+1z/0bTHPEBqg==") {
		t.Error("se perdió el blob <data> de ClientStorageToken")
	}
	if !strings.Contains(out, `cdhash H"7f15b4d58e85c29035fb9c41570c53eca57510f9"`) {
		t.Error("se perdió el Requirement")
	}
	if !strings.Contains(out, "<key>MigrationVersionNumber</key>") {
		t.Error("se perdieron claves de nivel superior")
	}
}

func TestAuthorizeClientsNoTocaOtrosClientes(t *testing.T) {
	out, _ := authorizeClients(clientsFixture, testBundleID)
	safari := out[strings.Index(out, "icom.apple.Safari"):strings.Index(out, "icl.observer.agent")]
	if strings.Count(safari, "<key>Authorized</key>") != 1 {
		t.Error("se alteró la entrada de Safari")
	}
	if strings.Contains(safari, "<key>Registered</key>") {
		t.Error("se agregó Registered a un cliente ajeno")
	}
}

func TestAuthorizeClientsEsIdempotente(t *testing.T) {
	once, changed := authorizeClients(clientsFixture, testBundleID)
	if changed != 1 {
		t.Fatalf("primera pasada: esperaba 1, hubo %d", changed)
	}
	twice, changed := authorizeClients(once, testBundleID)
	if changed != 0 {
		t.Errorf("segunda pasada debía ser no-op, cambió %d", changed)
	}
	if twice != once {
		t.Error("la segunda pasada modificó el documento")
	}
	// Idempotencia real: sin cambios el llamador no reinicia locationd.
	if strings.Count(twice, "<key>Authorized</key>") != 2 {
		t.Error("Authorized quedó duplicado")
	}
}

func TestAuthorizeClientsReemplazaValoresPrevios(t *testing.T) {
	// locationd escribe "Registered" a veces como <string></string>; si no se
	// reconoce esa forma, la clave queda duplicada y el plist deja de validar.
	doc := strings.Replace(clientsFixture,
		"<key>Registered</key>\n\t\t<true/>",
		"<key>Registered</key>\n\t\t<string></string>", 1)
	out, changed := authorizeClients(doc, testBundleID)
	if changed != 1 {
		t.Fatalf("esperaba 1 entrada modificada, hubo %d", changed)
	}
	if strings.Contains(out, "<key>Registered</key>\n\t\t<string></string>") {
		t.Error("no se reemplazó el Registered en formato string")
	}
	entry := out[strings.Index(out, "icl.observer.agent"):]
	if strings.Count(entry, "<key>Registered</key>") != 1 {
		t.Error("Registered quedó duplicado")
	}
}

func TestAuthorizeClientsFalsoSeCorrige(t *testing.T) {
	doc := strings.Replace(clientsFixture,
		"<key>Registered</key>\n\t\t<true/>",
		"<key>Registered</key>\n\t\t<false/>", 1)
	out, changed := authorizeClients(doc, testBundleID)
	if changed != 1 {
		t.Fatalf("esperaba 1 entrada modificada, hubo %d", changed)
	}
	entry := out[strings.Index(out, "icl.observer.agent"):]
	if strings.Contains(entry, "<false/>") {
		t.Error("quedó un valor en false")
	}
}

func TestAuthorizeClientsDictAnidado(t *testing.T) {
	// SubIdentities con un dict adentro: el cierre del dict externo debe
	// localizarse contando anidamiento, no con el primer </dict> que aparezca.
	doc := strings.Replace(clientsFixture,
		"<key>Executable</key>",
		"<key>SubIdentities</key>\n\t\t<array>\n\t\t\t<dict>\n\t\t\t\t<key>x</key>\n\t\t\t\t<true/>\n\t\t\t</dict>\n\t\t</array>\n\t\t<key>Executable</key>", 1)
	out, changed := authorizeClients(doc, testBundleID)
	if changed != 1 {
		t.Fatalf("esperaba 1 entrada modificada, hubo %d", changed)
	}
	if !strings.Contains(out, "<key>SubIdentities</key>") {
		t.Error("se perdió SubIdentities")
	}
	if !strings.Contains(out, "<key>MigrationVersionNumber</key>") {
		t.Error("se truncó el documento tras un dict anidado")
	}
}

func TestAuthorizeClientsDictVacio(t *testing.T) {
	doc := strings.Replace(clientsFixture,
		"<key>4DF05D9A-961D-4729-9A94-E07148506673:icl.observer.agent:</key>\n\t<dict>",
		"<key>4DF05D9A-961D-4729-9A94-E07148506673:icl.observer.agent:</key>\n\t<dict/>\n\t<key>relleno</key>\n\t<dict>", 1)
	out, changed := authorizeClients(doc, testBundleID)
	if changed != 1 {
		t.Fatalf("esperaba 1 entrada modificada, hubo %d", changed)
	}
	if strings.Contains(out, "<dict/>") {
		t.Error("no se expandió el dict vacío")
	}
}

func TestAuthorizeClientsSinCoincidencia(t *testing.T) {
	out, changed := authorizeClients(clientsFixture, "cl.observer.inexistente")
	if changed != 0 {
		t.Errorf("no debía cambiar nada, cambió %d", changed)
	}
	if out != clientsFixture {
		t.Error("el documento fue modificado sin coincidencias")
	}
}

func TestAuthorizeClientsBundleVacio(t *testing.T) {
	// Guarda contra el peor caso: un bundleID vacío haría match con TODAS las
	// claves y autorizaría a media máquina.
	out, changed := authorizeClients(clientsFixture, "")
	if changed != 0 || out != clientsFixture {
		t.Error("un bundleID vacío no debe modificar nada")
	}
}
