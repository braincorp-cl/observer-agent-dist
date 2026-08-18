package agent

// Feature 037 · Fase 1b (T007) — protectores y avance, por COM.
//
// Por que hay un archivo entero para tres datos: en Win32_EncryptableVolume la
// cantidad de protectores, su tipo y el porcentaje de conversion NO son
// propiedades de la clase. Son metodos —GetKeyProtectors(),
// GetKeyProtectorType() y GetConversionStatus()— y StackExchange/wmi v1.2.1
// solo consulta, no invoca. La unica via es go-ole, con el precedente vivo de
// `wua_windows.go`.
//
// 🚫 La via descartada: `manage-bde -protectors -get` devuelve TEXTO LOCALIZADO,
// que es exactamente la fragilidad que RN-A04 existe para prohibir. La flota es
// mixta espanol/ingles.
//
// 🔑 RN-A06 en una linea: de este archivo salen SOLO cantidades y codigos de
// tipo. Los identificadores de protector se usan para preguntar el tipo y
// mueren aca; el descarte lo ejecuta y lo prueba `aplicarLectura`.
//
// La regla de aislamiento de todo el archivo: cada fallo se traduce en un dato
// nulo del volumen que lo sufrio, y nunca en un error del sobre. La Fase 1a ya
// entrego estado de cifrado valido; un fallo de COM no puede degradarlo.

import (
	"fmt"
	"runtime"

	rmm "github.com/braincorp-cl/observer-agent-dist/shared"
	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

// lecturaProtectoresHabilitada apaga la Fase 1b (protectores + % por COM).
//
// 🚫 DESHABILITADA (2026-08-17): la invocacion de metodos por ExecMethod_ sobre
// Win32_EncryptableVolume CRASHEA el agente en Windows real -- access violation,
// NO un panic de Go, asi que recover() no lo atrapa (medido en DESKTOP-Q11L59A
// con 2.15.32: crash-loop cada ~30 s, sin stack en agent.log). El estado de
// cifrado (Fase 1a) es correcto y no toca este camino; el detalle de protectores
// y el porcentaje quedan en null (degradacion graciosa, RF-03/RF-05 de backlog).
// Sospecha viva: doble-release del `in` que go-ole libera al pasarlo como
// argumento a ExecMethod_. NO reactivar (poner en true) sin resolver esa memoria
// y re-medir en Q11L59A. Ver finding_fase1b_protectores_null_en_windows_real.
const lecturaProtectoresHabilitada = false

// enriquecerConProtectores completa la Fase 1b sobre un sobre ya armado.
//
// No devuelve error a proposito, igual que GetDiskEncryption y por el mismo
// motivo: el resultado de esta funcion son los campos del cable, y "no se pudo"
// ya tiene su representacion ahi (nulo). Un error devuelto solo podria
// registrarse en el log y descartarse, que es el patron que la feature evita.
func enriquecerConProtectores(de *rmm.DiskEncryption) {
	if !lecturaProtectoresHabilitada || len(de.Volumenes) == 0 {
		return
	}

	// 🪤 COM es por hilo. Sin LockOSThread el runtime de Go puede mover esta
	// goroutine a otro hilo del sistema entre dos llamadas, y ahi el
	// CoInitializeEx de mas abajo ya no vale: las invocaciones empiezan a
	// devolver CO_E_NOTINITIALIZED de forma intermitente. La libreria wmi
	// hace lo mismo puertas adentro; aca abrimos nuestra propia sesion, asi
	// que el cuidado nos toca.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		// S_FALSE significa "ya estaba inicializado en este hilo", que es
		// exito. Tratarlo como fallo era el bug de la 028 ⇒ comhresult.go.
		if e, ok := err.(*ole.OleError); !ok || (e.Code() != S_OK && e.Code() != S_FALSE) {
			return
		}
	}
	defer ole.CoUninitialize()

	svc, err := conectarNamespaceCifrado()
	if err != nil {
		return
	}
	defer svc.Release()

	for i := range de.Volumenes {
		// Un volumen sin DeviceID no se puede pedir por ruta. No deberia
		// pasar —es la clave de la clase— pero pedirlo igual armaria una
		// ruta con comillas vacias, y eso es un error por volumen.
		if de.Volumenes[i].DeviceID == "" {
			continue
		}
		aplicarLectura(&de.Volumenes[i], leerVolumen(svc, de.Volumenes[i].DeviceID))
	}
}

// conectarNamespaceCifrado abre una sesion de WMI por scripting.
//
// Es la misma que usa GetDiskEncryption para consultar, pero por otra puerta:
// wmi.QueryNamespace no expone el objeto, y para invocar metodos hace falta la
// instancia en la mano. Una sola conexion sirve para todos los volumenes.
func conectarNamespaceCifrado() (*ole.IDispatch, error) {
	unknown, err := oleutil.CreateObject("WbemScripting.SWbemLocator")
	if err != nil {
		return nil, fmt.Errorf(`oleutil.CreateObject("WbemScripting.SWbemLocator"): %w`, err)
	}
	defer unknown.Release()

	locator, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return nil, fmt.Errorf("QueryInterface(IDispatch) del locator: %w", err)
	}
	defer locator.Release()

	// "." es el equipo local. El namespace es el mismo constante que la
	// consulta de la Fase 1a, para que no puedan divergir.
	res, err := oleutil.CallMethod(locator, "ConnectServer", ".", namespaceCifrado)
	if err != nil {
		return nil, fmt.Errorf("ConnectServer(%s): %w", namespaceCifrado, err)
	}

	// 🪤 NO `res.Clear()`: ToIDispatch NO suma una referencia, así que
	// VariantClear liberaría el objeto que devolvemos y el llamador recibiría
	// un puntero muerto —"Object is not connected to server" al primer .Get()—.
	// La referencia de la VARIANT se transfiere al llamador, que hace Release.
	svc := res.ToIDispatch()
	if svc == nil {
		return nil, fmt.Errorf("ConnectServer(%s) no devolvio un objeto", namespaceCifrado)
	}

	return svc, nil
}

// leerVolumen junta lo de la Fase 1b para un volumen.
//
// Los dos datos se leen por separado y fallan por separado: un volumen bloqueado
// puede dar el porcentaje y no los protectores, o al revez. Mezclarlos en un
// solo "salio bien" perderia la mitad del dato cada vez.
func leerVolumen(svc *ole.IDispatch, deviceID string) lecturaProtectores {
	var l lecturaProtectores

	obj, err := traerVolumen(svc, deviceID)
	if err != nil {
		return l
	}
	defer obj.Release()

	l.Porcentaje = leerPorcentaje(obj)

	ids, err := leerIDsDeProtectores(obj)
	if err != nil {
		return l
	}

	l.Leida = true
	l.Protectores = make([]protectorCrudo, 0, len(ids))
	for _, id := range ids {
		p := protectorCrudo{ID: id}
		if tipo, err := leerTipoDeProtector(obj, id); err == nil {
			p.Tipo, p.TipoLeido = tipo, true
		}
		// Si el tipo no se pudo leer, el protector se cuenta igual: que
		// EXISTE es un hecho, que sea de contrasena o de TPM es un detalle.
		l.Protectores = append(l.Protectores, p)
	}

	return l
}

// traerVolumen pide la instancia por su ruta.
func traerVolumen(svc *ole.IDispatch, deviceID string) (*ole.IDispatch, error) {
	res, err := oleutil.CallMethod(svc, "Get", rutaDelVolumen(deviceID))
	if err != nil {
		return nil, fmt.Errorf("SWbemServices.Get: %w", err)
	}

	// NO `res.Clear()`: la referencia del objeto se transfiere al llamador
	// (que hace obj.Release()). Ver la nota en conectarNamespaceCifrado.
	obj := res.ToIDispatch()
	if obj == nil {
		return nil, fmt.Errorf("SWbemServices.Get no devolvio un objeto")
	}

	return obj, nil
}

// leerIDsDeProtectores invoca GetKeyProtectors() sin filtro de tipo.
//
// Devolver una lista vacia con error nulo es un resultado legitimo y distinto de
// un fallo: significa "el volumen no tiene protectores". Ver lecturaProtectores.
func leerIDsDeProtectores(obj *ole.IDispatch) ([]string, error) {
	// GetKeyProtectors filtra por KeyProtectorType, donde 0 significa TODOS.
	//
	// El cero va explicito en vez de omitir el parametro: el metodo lo declara
	// opcional, pero el ejemplo de la propia clase lo pasa, y con ExecMethod_
	// —que arma la instancia de entrada aparte— "omitido" y "cero" no son
	// obviamente lo mismo. 🟡 Este es el unico camino del archivo que todavia
	// NO se ejercito contra un Windows real; si fallara, el volumen queda con
	// protectores nulos y el resto del cable intacto.
	in, err := instanciarParametros(obj, "GetKeyProtectors")
	if err != nil {
		return nil, err
	}
	defer in.Release()

	if _, err := oleutil.PutProperty(in, "KeyProtectorType", 0); err != nil {
		return nil, fmt.Errorf("PutProperty(KeyProtectorType): %w", err)
	}

	out, err := ejecutarMetodo(obj, "GetKeyProtectors", in)
	if err != nil {
		return nil, err
	}
	defer out.Release()

	if err := verificarRetorno(out, "GetKeyProtectors"); err != nil {
		return nil, err
	}

	ids, err := oleutil.GetProperty(out, "VolumeKeyProtectorID")
	if err != nil {
		return nil, fmt.Errorf("GetProperty(VolumeKeyProtectorID): %w", err)
	}
	defer ids.Clear()

	// 🪤 Un volumen sin protectores devuelve VT_NULL, no un arreglo vacio, y
	// ToArray da nil para eso. Es exito con cero elementos —NO un fallo—
	// porque el ReturnValue de arriba ya dijo que la invocacion salio bien.
	arr := ids.ToArray()
	if arr == nil {
		return nil, nil
	}

	return arr.ToStringArray(), nil
}

// leerTipoDeProtector invoca GetKeyProtectorType(id).
//
// Es el unico metodo de los tres que lleva parametro de entrada, y por eso paga
// el ritual completo de SWbemObject: pedir la definicion del metodo, sacar su
// InParameters, instanciarlo y rellenarlo.
func leerTipoDeProtector(obj *ole.IDispatch, id string) (uint32, error) {
	in, err := instanciarParametros(obj, "GetKeyProtectorType")
	if err != nil {
		return 0, err
	}
	defer in.Release()

	if _, err := oleutil.PutProperty(in, "VolumeKeyProtectorID", id); err != nil {
		return 0, fmt.Errorf("PutProperty(VolumeKeyProtectorID): %w", err)
	}

	out, err := ejecutarMetodo(obj, "GetKeyProtectorType", in)
	if err != nil {
		return 0, err
	}
	defer out.Release()

	if err := verificarRetorno(out, "GetKeyProtectorType"); err != nil {
		return 0, err
	}

	return leerUint32(out, "KeyProtectorType")
}

// leerPorcentaje invoca GetConversionStatus() por el avance del cifrado.
//
// Devuelve nil en cualquier tropiezo, y ese nil es informacion: "todavia no lo
// sabemos". Un cero seria "0 % cifrado", que es una afirmacion muy distinta.
//
// El ConversionStatus que este metodo tambien devuelve se IGNORA a proposito: ya
// viene por consulta en la Fase 1a, y quedarse con dos fuentes para el mismo
// campo es pedir que un dia discrepen.
func leerPorcentaje(obj *ole.IDispatch) *uint32 {
	out, err := ejecutarMetodo(obj, "GetConversionStatus", nil)
	if err != nil {
		return nil
	}
	defer out.Release()

	if err := verificarRetorno(out, "GetConversionStatus"); err != nil {
		return nil
	}

	pct, err := leerUint32(out, "EncryptionPercentage")
	if err != nil {
		return nil
	}

	return &pct
}

// ejecutarMetodo invoca un metodo de WMI y devuelve el objeto de salida.
//
// 🪤 Aca esta la razon por la que este archivo usa ExecMethod_ en vez de llamar
// el metodo por su nombre: los tres metodos de la clase entregan lo que importa
// en parametros de SALIDA, y una invocacion por IDispatch pasa los argumentos
// por valor. Llamando `obj.GetKeyProtectors()` derecho, la llamada "funciona",
// devuelve 0 y los IDs se quedan del otro lado sin que nada avise. ExecMethod_
// devuelve un SWbemObject cuyas PROPIEDADES son esos parametros de salida.
func ejecutarMetodo(obj *ole.IDispatch, metodo string, in *ole.IDispatch) (*ole.IDispatch, error) {
	var res *ole.VARIANT
	var err error

	if in == nil {
		res, err = oleutil.CallMethod(obj, "ExecMethod_", metodo)
	} else {
		res, err = oleutil.CallMethod(obj, "ExecMethod_", metodo, in)
	}
	if err != nil {
		return nil, fmt.Errorf("ExecMethod_(%s): %w", metodo, err)
	}

	// NO `res.Clear()`: la referencia del SWbemObject de salida se transfiere
	// al llamador (que hace out.Release()). Ver conectarNamespaceCifrado.
	out := res.ToIDispatch()
	if out == nil {
		return nil, fmt.Errorf("ExecMethod_(%s) no devolvio parametros de salida", metodo)
	}

	return out, nil
}

// instanciarParametros arma la instancia de entrada de un metodo.
func instanciarParametros(obj *ole.IDispatch, metodo string) (*ole.IDispatch, error) {
	metodos, err := oleutil.GetProperty(obj, "Methods_")
	if err != nil {
		return nil, fmt.Errorf("GetProperty(Methods_): %w", err)
	}
	defer metodos.Clear()

	// 🪤 Los objetos intermedios (coleccion, definicion, plantilla) NO llevan
	// su propio Release: ToIDispatch no suma referencia, así que la libera el
	// Clear de la VARIANT que los contiene. Ponerle ambos era un doble-release.
	coleccion := metodos.ToIDispatch()
	if coleccion == nil {
		return nil, fmt.Errorf("Methods_ no devolvio una coleccion")
	}

	item, err := oleutil.CallMethod(coleccion, "Item", metodo)
	if err != nil {
		return nil, fmt.Errorf("Methods_.Item(%s): %w", metodo, err)
	}
	defer item.Clear()

	definicion := item.ToIDispatch()
	if definicion == nil {
		return nil, fmt.Errorf("Methods_.Item(%s) no devolvio el metodo", metodo)
	}

	entrada, err := oleutil.GetProperty(definicion, "InParameters")
	if err != nil {
		return nil, fmt.Errorf("GetProperty(InParameters) de %s: %w", metodo, err)
	}
	defer entrada.Clear()

	plantilla := entrada.ToIDispatch()
	if plantilla == nil {
		return nil, fmt.Errorf("%s no declara parametros de entrada", metodo)
	}

	res, err := oleutil.CallMethod(plantilla, "SpawnInstance_")
	if err != nil {
		return nil, fmt.Errorf("SpawnInstance_ de %s: %w", metodo, err)
	}

	// NO `res.Clear()`: transfiere la instancia de entrada al llamador, que
	// hace in.Release(). Ver conectarNamespaceCifrado.
	in := res.ToIDispatch()
	if in == nil {
		return nil, fmt.Errorf("SpawnInstance_ de %s no devolvio instancia", metodo)
	}

	return in, nil
}

// verificarRetorno lee el ReturnValue del objeto de salida.
//
// Los metodos de Win32_EncryptableVolume devuelven HRESULT propios de BitLocker
// —FVE_E_LOCKED_VOLUME en un volumen bloqueado, FVE_E_NOT_ACTIVATED en uno sin
// activar— y la invocacion COM en si sale bien. Sin este control, un volumen
// bloqueado reportaria cero protectores: la afirmacion falsa mas peligrosa que
// puede producir esta feature.
//
// El exito se juzga con la regla de COM del repo (comhresult.go): SUCCEEDED es
// hr >= 0 leido con signo, no "igual a cero".
func verificarRetorno(out *ole.IDispatch, metodo string) error {
	ret, err := leerUint32(out, "ReturnValue")
	if err != nil {
		return fmt.Errorf("%s sin ReturnValue: %w", metodo, err)
	}

	if comFailed(uintptr(ret)) {
		return fmt.Errorf("%s devolvio 0x%08X", metodo, ret)
	}

	return nil
}

// leerUint32 saca una propiedad numerica de un objeto de WMI.
func leerUint32(obj *ole.IDispatch, propiedad string) (uint32, error) {
	v, err := oleutil.GetProperty(obj, propiedad)
	if err != nil {
		return 0, fmt.Errorf("GetProperty(%s): %w", propiedad, err)
	}
	defer v.Clear()

	if v.VT == ole.VT_NULL || v.VT == ole.VT_EMPTY {
		return 0, fmt.Errorf("%s llego vacia", propiedad)
	}

	return uint32(v.Val), nil
}
