package agent

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	rmm "github.com/braincorp-cl/observer-agent-dist/shared"
)

// Feature 037 · T004 — guardianes de la Fase 1a.
//
// Cada prueba de este archivo lleva su CONTROL POSITIVO: una regla alternativa
// plausible —la que se habria escrito sin pensarlo mucho— contra la que el caso
// tiene que divergir. Sin eso, una prueba que pasa no demuestra nada: podria
// estar pasando con cualquier implementacion, incluida una equivocada.

// reglaIngenuaPorTipo es la regla que el diseno estuvo a punto de adoptar:
// deducir el volumen de sistema del campo VolumeType, donde 0 es el volumen del
// sistema operativo. Existe SOLO como control: si algun dia el codigo real se
// pareciera a esto, las pruebas de abajo tienen que ponerse rojas.
func reglaIngenuaPorTipo(v rmm.Win32_EncryptableVolume) bool {
	return v.VolumeType == 0
}

func TestEsVolumenDeSistema(t *testing.T) {
	casos := []struct {
		nombre      string
		letra       string
		systemDrive string
		esperado    bool
	}{
		{"el caso normal, medido en terreno", "C:", "C:", true},
		{"minuscula: WMI y la variable de entorno no siempre coinciden", "c:", "C:", true},
		{"con espacios alrededor", " C: ", "C:", true},
		{"con la barra final que a veces trae %SystemDrive%", "C:", `C:\`, true},
		{"otro volumen con letra", "D:", "C:", false},
		{"SIN LETRA: la particion reservada de arranque", "", "C:", false},
		{"sin letra y sin unidad de sistema", "", "", false},
		{"si no sabemos cual es la unidad del sistema, no marcamos ninguna", "C:", "", false},
	}

	for _, c := range casos {
		if got := esVolumenDeSistema(c.letra, c.systemDrive); got != c.esperado {
			t.Errorf("%s: esVolumenDeSistema(%q, %q) = %v, se esperaba %v",
				c.nombre, c.letra, c.systemDrive, got, c.esperado)
		}
	}
}

// TestControlPositivoVolumenSinLetra es el que da valor al resto.
//
// El escenario es un equipo con dos volumenes cifrables donde el de arranque no
// tiene letra. La regla buena mira la letra contra %SystemDrive%; la ingenua
// mira VolumeType. Si las dos coincidieran en todos los casos, la prueba de
// arriba no estaria comprobando nada.
func TestControlPositivoVolumenSinLetra(t *testing.T) {
	reservado := rmm.Win32_EncryptableVolume{
		DeviceID:    `\\?\Volume{00000000-0000-0000-0000-000000000001}\`,
		DriveLetter: "",
		VolumeType:  0,
	}

	buena := esVolumenDeSistema(reservado.DriveLetter, "C:")
	ingenua := reglaIngenuaPorTipo(reservado)

	if buena {
		t.Fatal("un volumen sin letra jamas puede ser el de sistema")
	}
	if !ingenua {
		t.Fatal("el control esta mal armado: la regla ingenua tiene que decir que SI para que la divergencia pruebe algo")
	}
	if buena == ingenua {
		t.Fatal("las dos reglas coinciden: esta prueba no discrimina y no es un guardian")
	}
}

func TestEsAusenciaDeProveedor(t *testing.T) {
	casos := []struct {
		nombre   string
		scode    uint32
		esperado bool
	}{
		{"WBEM_E_INVALID_NAMESPACE: el equipo no trae BitLocker", 0x8004100E, true},
		{"WBEM_E_INVALID_CLASS: el namespace esta pero la clase no", 0x80041010, true},
		{"WBEM_E_ACCESS_DENIED: hay proveedor, falta permiso", 0x80041003, false},
		{"WBEM_E_FAILED: fallo generico del proveedor", 0x80041001, false},
		{"E_ACCESSDENIED de Windows", 0x80070005, false},
		{"RPC_S_SERVER_UNAVAILABLE: el servicio WMI esta caido", 0x800706BA, false},
		{"sin codigo", 0x00000000, false},
	}

	for _, c := range casos {
		if got := esAusenciaDeProveedor(c.scode); got != c.esperado {
			t.Errorf("%s: esAusenciaDeProveedor(0x%08X) = %v, se esperaba %v",
				c.nombre, c.scode, got, c.esperado)
		}
	}
}

// TestControlPositivoAccesoDenegado protege la distincion mas cara de la
// feature. La regla ingenua —"cualquier error de la familia 0x8004 significa
// que el equipo no soporta BitLocker"— convertiria un permiso faltante en un
// equipo "sin cifrar" en el panel, que es exactamente el ok falso de RF-07.
func TestControlPositivoAccesoDenegado(t *testing.T) {
	const accesoDenegado uint32 = 0x80041003

	reglaIngenuaPorFamilia := func(scode uint32) bool {
		return scode&0xFFFF0000 == 0x80040000
	}

	if esAusenciaDeProveedor(accesoDenegado) {
		t.Fatal("acceso denegado NO es ausencia de proveedor: hay dato, no lo podemos leer")
	}
	if !reglaIngenuaPorFamilia(accesoDenegado) {
		t.Fatal("el control esta mal armado: la regla por familia tiene que tragarse este codigo")
	}
}

func TestVolumenDeCableNoInventaCeros(t *testing.T) {
	sinLetra := volumenDeCable(rmm.Win32_EncryptableVolume{DeviceID: "vol-1"}, "C:")

	if sinLetra.DriveLetter != nil {
		t.Errorf("un volumen sin letra tiene que viajar con drive_letter nulo, llego %q", *sinLetra.DriveLetter)
	}
	if sinLetra.IsSystemVolume {
		t.Error("un volumen sin letra no puede quedar marcado como el de sistema")
	}
	if sinLetra.EncryptionPercentage != nil {
		t.Error("el avance sale de GetConversionStatus() y es Fase 1b: tiene que viajar nulo, no cero")
	}
	if sinLetra.KeyProtectorCount != nil {
		t.Error("la cantidad de protectores es Fase 1b: nula, porque cero significaria 'sin protectores'")
	}

	conLetra := volumenDeCable(rmm.Win32_EncryptableVolume{
		DeviceID:         "vol-2",
		DriveLetter:      "c:",
		ProtectionStatus: 1,
		ConversionStatus: 1,
		EncryptionMethod: 6,
		VolumeType:       0,
	}, "C:")

	if conLetra.DriveLetter == nil || *conLetra.DriveLetter != "C:" {
		t.Errorf("la letra tiene que viajar normalizada, llego %v", conLetra.DriveLetter)
	}
	if !conLetra.IsSystemVolume {
		t.Error("C: contra %SystemDrive%=C: es el volumen de sistema")
	}
	if conLetra.EncryptionMethod != 6 {
		t.Errorf("el metodo viaja como codigo crudo (RN-A05), llego %d", conLetra.EncryptionMethod)
	}
}

// TestSobreDistingueLosTresEstados congela RN-A03 y RF-07: "no soporta",
// "no se pudo leer" y "no hay volumenes" son tres cosas distintas, y ninguna
// puede degradar a "sin cifrar".
func TestSobreDistingueLosTresEstados(t *testing.T) {
	noSoportado := diskEncryptionNoSoportado()
	if noSoportado.Soportado {
		t.Error("no soportado tiene que decir soportado=false")
	}
	if noSoportado.Error != nil {
		t.Error("no soportado NO es un error: nada fallo, la funcion no aplica")
	}

	conError := diskEncryptionConError(errors.New("fallo la consulta"))
	if !conError.Soportado {
		t.Error("un fallo de consulta no autoriza a declarar el equipo sin soporte")
	}
	if conError.Error == nil {
		t.Fatal("un fallo de consulta tiene que viajar con la causa (RF-07)")
	}

	vacio := armarDiskEncryption(nil, "C:")
	if !vacio.Soportado || vacio.Error != nil || len(vacio.Volumenes) != 0 {
		t.Errorf("consultamos y no hay volumenes cifrables: es un resultado legitimo, llego %+v", vacio)
	}
}

// TestFormaDelCable es el guardian de GAP-051: si alguien renombra una clave
// json, el espejo de natsapi deja de matchear y el UPDATE no actualiza nada,
// SIN error. Congelar los nombres aca cuesta una prueba y ahorra un silencio.
func TestFormaDelCable(t *testing.T) {
	b, err := json.Marshal(armarDiskEncryption([]rmm.Win32_EncryptableVolume{{DeviceID: "vol-1"}}, "C:"))
	if err != nil {
		t.Fatalf("no se pudo serializar el sobre: %v", err)
	}

	var sobre map[string]json.RawMessage
	if err := json.Unmarshal(b, &sobre); err != nil {
		t.Fatalf("no se pudo releer el sobre: %v", err)
	}

	for _, clave := range []string{"soportado", "error", "volumenes"} {
		if _, ok := sobre[clave]; !ok {
			t.Errorf("falta la clave %q en el sobre: %s", clave, b)
		}
	}

	var volumenes []map[string]json.RawMessage
	if err := json.Unmarshal(sobre["volumenes"], &volumenes); err != nil {
		t.Fatalf("volumenes no es una lista: %v", err)
	}
	if len(volumenes) != 1 {
		t.Fatalf("se esperaba un volumen, llegaron %d", len(volumenes))
	}

	esperadas := []string{
		"conversion_status",
		"device_id",
		"drive_letter",
		"encryption_method",
		"encryption_percentage",
		"is_system_volume",
		"is_volume_initialized_for_protection",
		"key_protector_count",
		"key_protector_types",
		"persistent_volume_id",
		"protection_status",
		"volume_type",
	}

	llegaron := make([]string, 0, len(volumenes[0]))
	for k := range volumenes[0] {
		llegaron = append(llegaron, k)
	}
	sort.Strings(llegaron)

	if strings.Join(llegaron, ",") != strings.Join(esperadas, ",") {
		t.Errorf("las claves del volumen cambiaron.\n  llegaron: %v\n  esperadas: %v", llegaron, esperadas)
	}
}

// TestListaVaciaViajaComoListaVacia: un `null` en el cable obligaria al otro
// lado a adivinar si hubo consulta. Tiene que ser `[]`.
func TestListaVaciaViajaComoListaVacia(t *testing.T) {
	for nombre, sobre := range map[string]rmm.DiskEncryption{
		"no soportado":       diskEncryptionNoSoportado(),
		"con error":          diskEncryptionConError(errors.New("x")),
		"consulta sin filas": armarDiskEncryption(nil, "C:"),
	} {
		b, err := json.Marshal(sobre)
		if err != nil {
			t.Fatalf("%s: %v", nombre, err)
		}
		if !strings.Contains(string(b), `"volumenes":[]`) {
			t.Errorf("%s: la lista vacia tiene que serializar como [], llego %s", nombre, b)
		}
	}
}
