package agent

// Feature 037 · Fase 1a — la consulta WMI del estado de BitLocker.
//
// La logica que puede equivocarse sin ayuda de WMI vive en
// `diskencryption.go`, sin etiqueta de plataforma, para que la CI la pruebe de
// verdad. Aca queda solo lo que exige un Windows delante.

import (
	"errors"
	"os"

	"github.com/StackExchange/wmi"
	rmm "github.com/braincorp-cl/observer-agent-dist/shared"
	ole "github.com/go-ole/go-ole"
)

// namespaceCifrado es donde vive de verdad el estado de BitLocker.
//
// 📌 Medido el 2026-08-17 en tres equipos de la flota (Windows 10 Pro 22H2,
// Windows 11 Pro 23H2 y Windows 11 IoT Enterprise LTSC 24H2): un barrido
// recursivo de todo `root` encuentra este namespace y ningun otro de cifrado.
// El plan de la feature apuntaba a `root\Microsoft\Windows\BitLocker`, que NO
// EXISTE en ninguna de las tres — el nombre venia de suponer que las cadenas
// que devuelve Get-BitLockerVolume salian de una clase distinta, cuando en
// realidad las produce el cmdlet.
const namespaceCifrado = `root\CIMV2\Security\MicrosoftVolumeEncryption`

// GetDiskEncryption devuelve el bloque `disk_encryption` que viaja dentro del
// mensaje `agent-wmi`. No recibe ni devuelve error a proposito: las tres
// salidas posibles —no soportado, fallo de consulta, y datos— son parte del
// contrato y tienen que llegar al servidor distinguibles entre si.
//
// Es la diferencia deliberada con las GetWin32_* vecinas de `wmi_windows.go`,
// que devuelven `([]interface{}, error)` y cuyo error el llamador registra en
// el log y descarta. Para el inventario de video eso es aceptable; para una
// feature de cumplimiento, un error descartado se veria en el panel como un
// equipo sin cifrar (RF-07).
func GetDiskEncryption() rmm.DiskEncryption {
	var dst []rmm.Win32_EncryptableVolume

	q := wmi.CreateQuery(&dst, "")
	if err := wmi.QueryNamespace(q, &dst, namespaceCifrado); err != nil {
		if scode, ok := codigoWMI(err); ok && esAusenciaDeProveedor(scode) {
			return diskEncryptionNoSoportado()
		}
		return diskEncryptionConError(err)
	}

	// %SystemDrive% es la unica fuente local de "cual es el volumen del
	// sistema". Si por lo que sea viniera vacia, esVolumenDeSistema no marca
	// ninguno: preferimos un equipo sin veredicto a un veredicto inventado.
	de := armarDiskEncryption(dst, os.Getenv("SystemDrive"))

	// Fase 1b: los protectores y el avance NO son propiedades de la clase, hay
	// que invocarlos por COM. Va DESPUES y sin poder fallar hacia afuera, para
	// que un tropiezo de COM deje los campos de la 1b nulos y no toque nada de
	// lo que ya se leyo por consulta.
	enriquecerConProtectores(&de)

	return de
}

// codigoWMI saca el codigo WBEM de un error de COM.
//
// 🪤 El HRESULT de arriba NO sirve: cuando ConnectServer falla, la llamada por
// IDispatch devuelve DISP_E_EXCEPTION (0x80020009) y el codigo que interesa
// —WBEM_E_INVALID_NAMESPACE, por ejemplo— queda adentro del EXCEPINFO. go-ole
// lo guarda como subError, asi que hay que bajar un nivel antes de mirar.
//
// 🟡 Este camino esta escrito contra la estructura de go-ole v1.3.0 y todavia
// NO se ejercito contra un Windows sin el proveedor de BitLocker; se mide en
// T006 contra el equipo Windows 7 de la flota, que es justo esa condicion. Si
// la extraccion fallara, el `ok=false` cae en `diskEncryptionConError`, que es
// el lado seguro: la feature reportaria "sin dato" en vez de "no soportado",
// nunca "sin cifrar".
func codigoWMI(err error) (uint32, bool) {
	var oleErr *ole.OleError
	if !errors.As(err, &oleErr) {
		return 0, false
	}

	if ex, ok := oleErr.SubError().(ole.EXCEPINFO); ok {
		if scode := ex.SCODE(); scode != 0 {
			return scode, true
		}
	}

	return uint32(oleErr.Code()), true
}
