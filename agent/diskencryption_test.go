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

// --- Feature 037 · T008 — guardianes de la Fase 1b ---

// idDeProtectorReal es un identificador de protector con la forma que devuelve
// GetKeyProtectors() en un equipo de verdad. NO es material de clave por si
// mismo, y justamente por eso es el caso interesante: es lo que un descuido
// dejaria pasar al cable pensando "total, es un GUID".
//
// Con ese identificador en la mano, cualquiera con acceso al equipo le pide a
// BitLocker la contrasena de recuperacion. Publicarlo en el inventario de la
// flota seria repartir la mitad de la llave.
const idDeProtectorReal = "{35F2A29A-8CE1-4E9D-9E60-8FE4A5C3B9C1}"

// TestAplicarLecturaDistingueCeroDeNoLeido congela la razon de ser de la Fase
// 1b: "no tiene protectores" y "no pudimos contarlos" son dos cosas distintas,
// y la segunda jamas puede viajar como cero.
func TestAplicarLecturaDistingueCeroDeNoLeido(t *testing.T) {
	noLeida := volumenDeCable(rmm.Win32_EncryptableVolume{DeviceID: "vol-1"}, "C:")
	aplicarLectura(&noLeida, lecturaProtectores{Leida: false})

	if noLeida.KeyProtectorCount != nil {
		t.Errorf("una lectura que fallo tiene que dejar la cantidad nula, llego %d", *noLeida.KeyProtectorCount)
	}
	if noLeida.KeyProtectorTypes != nil {
		t.Errorf("una lectura que fallo tiene que dejar los tipos nulos, llego %v", noLeida.KeyProtectorTypes)
	}

	sinProtectores := volumenDeCable(rmm.Win32_EncryptableVolume{DeviceID: "vol-2"}, "C:")
	aplicarLectura(&sinProtectores, lecturaProtectores{Leida: true})

	if sinProtectores.KeyProtectorCount == nil {
		t.Fatal("un volumen sin protectores es un dato REAL: la cantidad tiene que viajar en cero, no nula")
	}
	if *sinProtectores.KeyProtectorCount != 0 {
		t.Errorf("se esperaba cero protectores, llego %d", *sinProtectores.KeyProtectorCount)
	}

	b, err := json.Marshal(sinProtectores)
	if err != nil {
		t.Fatalf("no se pudo serializar: %v", err)
	}
	if !strings.Contains(string(b), `"key_protector_types":[]`) {
		t.Errorf("contamos y no hay: la lista tiene que ser [], no null. Llego %s", b)
	}
}

// TestAplicarLecturaCuentaLosProtectoresSinTipo protege la coherencia entre los
// dos campos. Un protector cuyo tipo no se pudo leer EXISTE igual, y dejarlo
// fuera de la lista produciria un "3 protectores" con dos tipos listados, que
// se lee como bug de la consola en vez de dato incompleto del agente.
func TestAplicarLecturaCuentaLosProtectoresSinTipo(t *testing.T) {
	vol := volumenDeCable(rmm.Win32_EncryptableVolume{DeviceID: "vol-3"}, "C:")
	aplicarLectura(&vol, lecturaProtectores{
		Leida: true,
		Protectores: []protectorCrudo{
			{ID: idDeProtectorReal, Tipo: 3, TipoLeido: true}, // TPM
			{ID: "{otro}", Tipo: 0, TipoLeido: false},         // el tipo no se pudo leer
			{ID: "{tercero}", Tipo: 8, TipoLeido: true},       // contrasena de recuperacion
		},
	})

	if vol.KeyProtectorCount == nil || *vol.KeyProtectorCount != 3 {
		t.Fatalf("se esperaban 3 protectores contados, llego %v", vol.KeyProtectorCount)
	}
	if len(vol.KeyProtectorTypes) != *vol.KeyProtectorCount {
		t.Errorf("la cantidad y la lista de tipos tienen que tener el mismo largo: %d contra %d",
			*vol.KeyProtectorCount, len(vol.KeyProtectorTypes))
	}
	if vol.KeyProtectorTypes[1] != keyProtectorTypeDesconocido {
		t.Errorf("un tipo que no se pudo leer viaja como 0 (desconocido u otro), llego %d", vol.KeyProtectorTypes[1])
	}
}

// TestNingunMaterialDeClaveEnElPayload es el guardian de RN-A06 (T008).
//
// Serializa el sobre completo por el camino real —volumenDeCable +
// aplicarLectura, con identificadores de protector realistas adentro— y exige
// que ninguno de esos identificadores aparezca en el cable. Del bloque de
// protectores solo pueden salir dos cosas: cuantos son y de que tipo.
//
// El control positivo es la version ingenua: un struct que empuja el
// protectorCrudo al json tal cual. Si el guardian no distinguiera las dos, no
// estaria comprobando nada.
func TestNingunMaterialDeClaveEnElPayload(t *testing.T) {
	crudos := []protectorCrudo{
		{ID: idDeProtectorReal, Tipo: 3, TipoLeido: true},
		{ID: "{0F2B7C11-4444-4A0A-9F31-000000000002}", Tipo: 8, TipoLeido: true},
	}

	vol := volumenDeCable(rmm.Win32_EncryptableVolume{
		DeviceID:         `\\?\Volume{11111111-2222-3333-4444-555555555555}\`,
		DriveLetter:      "C:",
		ProtectionStatus: 1,
		ConversionStatus: 1,
		EncryptionMethod: 6,
	}, "C:")
	aplicarLectura(&vol, lecturaProtectores{Leida: true, Protectores: crudos})

	sobre := rmm.DiskEncryption{Soportado: true, Volumenes: []rmm.DiskEncryptionVolume{vol}}
	b, err := json.Marshal(sobre)
	if err != nil {
		t.Fatalf("no se pudo serializar el sobre: %v", err)
	}
	cable := string(b)

	for _, p := range crudos {
		if strings.Contains(cable, p.ID) {
			t.Errorf("RN-A06: el identificador del protector %q viajo en el cable: %s", p.ID, cable)
		}
	}

	// Lo que SI tiene que estar: la cuenta y los tipos.
	if !strings.Contains(cable, `"key_protector_count":2`) {
		t.Errorf("la cantidad de protectores tiene que viajar (RF-01): %s", cable)
	}
	if !strings.Contains(cable, `"key_protector_types":[3,8]`) {
		t.Errorf("los tipos tienen que viajar como codigos crudos (RN-A05): %s", cable)
	}

	// Control positivo: la version que no piensa en RN-A06 filtra el ID, y
	// esta prueba tiene que ser capaz de verlo.
	type volumenIngenuo struct {
		rmm.DiskEncryptionVolume
		Protectores []protectorCrudo `json:"protectores"`
	}
	ingenuo, err := json.Marshal(volumenIngenuo{DiskEncryptionVolume: vol, Protectores: crudos})
	if err != nil {
		t.Fatalf("no se pudo serializar el control: %v", err)
	}
	if !strings.Contains(string(ingenuo), idDeProtectorReal) {
		t.Fatal("el control esta mal armado: la version ingenua TIENE que filtrar el identificador para que la divergencia pruebe algo")
	}
}

// TestNingunaClaveDelCableNombraMaterialDeClave es la otra mitad de T008.
//
// La prueba de arriba mira los VALORES de hoy; esta mira los NOMBRES de manana:
// si alguien agrega al contrato un campo llamado recovery_password o
// numerical_password, se pone roja antes de que exista el codigo que lo llene.
// Es el guardian que sobrevive a los cambios que todavia no se escribieron.
func TestNingunaClaveDelCableNombraMaterialDeClave(t *testing.T) {
	prohibidas := []string{
		"password", "passphrase", "contrasena", "recovery", "recuperacion",
		"clave", "secret", "numerical", "startup_key", "external_key",
		"key_material", "protector_id", "volume_key", "keyfile",
	}

	nombraMaterial := func(claves []string) (string, bool) {
		for _, k := range claves {
			for _, mala := range prohibidas {
				if strings.Contains(k, mala) {
					return k, true
				}
			}
		}
		return "", false
	}

	vol := volumenDeCable(rmm.Win32_EncryptableVolume{DeviceID: "vol-1"}, "C:")
	aplicarLectura(&vol, lecturaProtectores{Leida: true, Protectores: []protectorCrudo{{ID: idDeProtectorReal}}})

	b, err := json.Marshal(vol)
	if err != nil {
		t.Fatalf("no se pudo serializar: %v", err)
	}
	var mapa map[string]json.RawMessage
	if err := json.Unmarshal(b, &mapa); err != nil {
		t.Fatalf("no se pudo releer: %v", err)
	}

	claves := make([]string, 0, len(mapa))
	for k := range mapa {
		claves = append(claves, k)
	}
	sort.Strings(claves)

	if k, hay := nombraMaterial(claves); hay {
		t.Errorf("RN-A06: la clave %q del contrato nombra material de clave. Claves: %v", k, claves)
	}

	// Control positivo: la regla tiene que atrapar el campo que RN-A06
	// prohibe. Sin esto, una lista de prohibidas mal escrita pasaria igual.
	if _, hay := nombraMaterial([]string{"device_id", "recovery_password"}); !hay {
		t.Fatal("el control esta mal armado: la regla tiene que atrapar recovery_password")
	}
}

// TestRutaDelVolumen protege el fallo total mas silencioso de la Fase 1b: sin
// doblar las barras invertidas, SWbemServices.Get no encuentra NINGUN volumen y
// los protectores quedan nulos para siempre, con el resto del cable correcto.
func TestRutaDelVolumen(t *testing.T) {
	const deviceID = `\\?\Volume{11111111-2222-3333-4444-555555555555}\`
	const esperada = `Win32_EncryptableVolume.DeviceID="\\\\?\\Volume{11111111-2222-3333-4444-555555555555}\\"`

	got := rutaDelVolumen(deviceID)
	if got != esperada {
		t.Errorf("la ruta WMI escapa las barras doblandolas.\n  llego:    %s\n  esperada: %s", got, esperada)
	}

	// Control positivo: la ruta sin escapar —la que se escribe sola con un
	// Sprintf— tiene que ser DISTINTA de la buena.
	ingenua := `Win32_EncryptableVolume.DeviceID="` + deviceID + `"`
	if got == ingenua {
		t.Fatal("la ruta quedo sin escapar: es exactamente el fallo que esta prueba vigila")
	}

	// Una comilla en el identificador cerraria la ruta antes de tiempo. No
	// deberia pasar nunca, y por eso mismo no puede quedar sin cubrir.
	if r := rutaDelVolumen(`vol"raro`); r != `Win32_EncryptableVolume.DeviceID="vol\"raro"` {
		t.Errorf("la comilla tiene que quedar escapada, llego %s", r)
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
