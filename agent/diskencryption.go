package agent

// Feature 037 · Fase 1a — la parte del cifrado de disco que NO habla con WMI.
//
// ⚠️ Este archivo NO lleva etiqueta de plataforma A PROPOSITO, aunque hoy solo
// lo use el codigo de Windows: la CI corre `go test` en Linux, asi que una
// regla escondida detras de `//go:build windows` se compilaria y **no se
// ejecutaria nunca**. Mismo criterio que `comhresult.go` y `usersession.go`.
//
// Lo que vive aca es la parte de la feature que puede mentir sin que WMI tenga
// la culpa: elegir cual de los volumenes es el del sistema, y armar el sobre
// que viaja por el cable sin confundir "no se pudo leer" con "no esta cifrado".

import (
	"strings"

	rmm "github.com/braincorp-cl/observer-agent-dist/shared"
)

// Codigos de error de WMI que significan "este equipo no ofrece BitLocker".
//
// Son numeros, no texto, y esa es toda la gracia: el mensaje que devuelve WMI
// esta LOCALIZADO —medido el 2026-08-17, el mismo equipo responde "Invalid
// namespace" o "Espacio de nombres no valido" segun el idioma del sistema— y la
// flota es mixta espanol/ingles. Comparar ese texto seria exactamente la
// fragilidad que RN-A04 existe para prohibir.
const (
	wbemEInvalidNamespace uint32 = 0x8004100E
	wbemEInvalidClass     uint32 = 0x80041010
)

// esAusenciaDeProveedor traduce un codigo de WMI a la senal de RN-A07.
//
// La distincion es la razon de ser de esta feature: un equipo sin BitLocker
// (Home, Windows 7, una edicion recortada) no incumple nada, mientras que un
// equipo donde la consulta fallo es un dato que NO tenemos. Los dos terminan
// sin volumenes en la lista, y confundirlos produce el peor resultado posible:
// mostrar "sin cifrar" un equipo que quiza si lo esta (RF-07).
func esAusenciaDeProveedor(scode uint32) bool {
	return scode == wbemEInvalidNamespace || scode == wbemEInvalidClass
}

// normalizarUnidad deja una letra de unidad comparable.
//
// WMI devuelve "C:" y %SystemDrive% tambien, pero la variable de entorno puede
// venir con espacios o con la barra final si alguien la exporto a mano. Las
// letras de unidad de Windows son ASCII A-Z, y strings.ToUpper de Go no
// depende del locale del sistema, asi que la comparacion es invariante.
func normalizarUnidad(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, `\`)
	return strings.ToUpper(s)
}

// esVolumenDeSistema decide el unico campo de RN-A02 que solo el agente puede
// decidir: cual de los volumenes es el que hace al equipo cumplir o no.
//
// 🔑 Un volumen SIN LETRA nunca es el de sistema. No es un caso de borde
// exotico: la particion reservada del arranque y los volumenes de datos recien
// creados aparecen sin letra en la consulta, y tratarlos como candidatos haria
// que el veredicto del equipo dependiera del orden en que WMI los devuelve.
func esVolumenDeSistema(driveLetter, systemDrive string) bool {
	letra := normalizarUnidad(driveLetter)
	sistema := normalizarUnidad(systemDrive)
	if letra == "" || sistema == "" {
		return false
	}
	return letra == sistema
}

// volumenDeCable pasa un volumen de la forma de WMI a la del contrato.
//
// Los tres campos que quedan nulos lo hacen por razones distintas y ninguna es
// un descuido:
//
//   - DriveLetter: el volumen no tiene letra. Es normal.
//   - EncryptionPercentage: el avance NO es propiedad de la clase, sale de
//     GetConversionStatus() por COM. Nulo dice "todavia no lo leemos", que no
//     es lo mismo que cero por ciento.
//   - KeyProtectorCount / KeyProtectorTypes: mismo motivo, GetKeyProtectors()
//     es un metodo. Nulo no es "cero protectores".
//
// Los tres los rellena despues aplicarLectura (Fase 1b), que corre APARTE a
// proposito: si la tanda de COM falla, lo que ya se leyo por consulta tiene que
// llegar igual al servidor.
func volumenDeCable(v rmm.Win32_EncryptableVolume, systemDrive string) rmm.DiskEncryptionVolume {
	vol := rmm.DiskEncryptionVolume{
		DeviceID:                         v.DeviceID,
		ProtectionStatus:                 v.ProtectionStatus,
		ConversionStatus:                 v.ConversionStatus,
		EncryptionMethod:                 v.EncryptionMethod,
		PersistentVolumeID:               v.PersistentVolumeID,
		IsVolumeInitializedForProtection: v.IsVolumeInitializedForProtection,
		VolumeType:                       v.VolumeType,
		IsSystemVolume:                   esVolumenDeSistema(v.DriveLetter, systemDrive),
	}

	if letra := normalizarUnidad(v.DriveLetter); letra != "" {
		vol.DriveLetter = &letra
	}

	return vol
}

// armarDiskEncryption construye el sobre del caso feliz.
//
// La lista arranca vacia pero NO nula: un `[]` en el cable es "consultamos y no
// hay volumenes cifrables", que es un resultado legitimo. Un `null` obligaria
// al otro lado a adivinar si hubo consulta.
func armarDiskEncryption(vols []rmm.Win32_EncryptableVolume, systemDrive string) rmm.DiskEncryption {
	de := rmm.DiskEncryption{
		Soportado: true,
		Volumenes: make([]rmm.DiskEncryptionVolume, 0, len(vols)),
	}

	for _, v := range vols {
		de.Volumenes = append(de.Volumenes, volumenDeCable(v, systemDrive))
	}

	return de
}

// diskEncryptionNoSoportado es la respuesta de RN-A07: el equipo no ofrece
// BitLocker. Error nulo a proposito — no fallo nada, sencillamente no aplica.
func diskEncryptionNoSoportado() rmm.DiskEncryption {
	return rmm.DiskEncryption{
		Soportado: false,
		Volumenes: make([]rmm.DiskEncryptionVolume, 0),
	}
}

// diskEncryptionConError es la respuesta de RF-07: no sabemos.
//
// Soportado queda en true porque no tenemos evidencia de lo contrario, y el
// error viaja lleno para que la consola muestre "sin dato". Lo que jamas puede
// pasar es que esto se lea como "sin cifrar".
func diskEncryptionConError(err error) rmm.DiskEncryption {
	msg := err.Error()
	return rmm.DiskEncryption{
		Soportado: true,
		Error:     &msg,
		Volumenes: make([]rmm.DiskEncryptionVolume, 0),
	}
}

// --- Feature 037 · Fase 1b — protectores y avance de la conversion ---
//
// Lo que sigue existe porque en Win32_EncryptableVolume estos datos NO son
// propiedades: salen de GetKeyProtectors(), GetKeyProtectorType() y
// GetConversionStatus(), que son metodos y obligan a go-ole. Esa parte vive en
// diskencryption_protectores_windows.go; aca queda la unica regla que puede
// equivocarse sin que COM tenga la culpa —y la mas cara de equivocar, porque es
// donde se cumple o se incumple RN-A06.

// keyProtectorTypeDesconocido es el valor 0 de la enumeracion KeyProtectorType,
// documentado como "unknown or other protector type".
//
// Se usa cuando el protector EXISTE pero su tipo no se pudo leer, para que
// KeyProtectorCount y KeyProtectorTypes tengan siempre el mismo largo: una
// consola que muestre "3 protectores" y liste 2 tipos parece un bug de la
// consola, no un dato incompleto del agente.
const keyProtectorTypeDesconocido uint32 = 0

// protectorCrudo es un protector tal como lo devuelve COM, ANTES de decidir
// que viaja.
//
// 🔑 El ID esta en este struct para que la funcion que lo tiene en la mano sea
// exactamente la que lo TIRA. Podria haberse descartado dentro del codigo de
// COM y no aparecer nunca por aca, pero entonces RN-A06 seria una promesa que
// la CI —que corre en Linux— no puede verificar. Con el ID pasando por una
// funcion pura, el guardian de T008 prueba el descarte de verdad.
type protectorCrudo struct {
	ID        string // jamas viaja: es con lo que se le pide la clave a BitLocker
	Tipo      uint32
	TipoLeido bool
}

// lecturaProtectores es el resultado de la Fase 1b para UN volumen.
//
// Leida no es redundante con una lista vacia. Un volumen sin ningun protector
// existe de verdad —BitLocker suspendido, o cifrado sin proteccion activa— y
// vale cero; un GetKeyProtectors que fallo no vale nada. Sin este campo los dos
// llegarian al panel como "0 protectores", que es el ok falso de siempre.
//
// El porcentaje va por su cuenta: es otra invocacion COM y falla por su cuenta.
type lecturaProtectores struct {
	Leida       bool
	Protectores []protectorCrudo
	Porcentaje  *uint32
}

// aplicarLectura vuelca la Fase 1b sobre un volumen ya armado por la 1a.
//
// Muta el volumen en vez de devolver uno nuevo porque el orden importa: la 1a
// ya dejo el dato que se lee por consulta, y esto solo puede AGREGAR. Un fallo
// de COM tiene que dejar el volumen tal como estaba, con los campos de la 1b
// nulos, nunca en cero.
func aplicarLectura(vol *rmm.DiskEncryptionVolume, l lecturaProtectores) {
	vol.EncryptionPercentage = l.Porcentaje

	if !l.Leida {
		return
	}

	cantidad := len(l.Protectores)
	vol.KeyProtectorCount = &cantidad

	// Lista vacia pero NO nula, por el mismo criterio que los volumenes del
	// sobre: `[]` es "contamos y no hay", `null` es "no pudimos contar".
	tipos := make([]uint32, 0, cantidad)
	for _, p := range l.Protectores {
		if p.TipoLeido {
			tipos = append(tipos, p.Tipo)
			continue
		}
		tipos = append(tipos, keyProtectorTypeDesconocido)
	}
	vol.KeyProtectorTypes = tipos
}

// rutaDelVolumen arma la ruta WMI con la que se pide UNA instancia.
//
// 🪤 El DeviceID de un volumen es `\\?\Volume{guid}\`, todo barras invertidas, y
// en una ruta WMI las barras invertidas dentro de las comillas se escapan
// DOBLANDOLAS. Sin eso, SWbemServices.Get devuelve "invalid object path" para
// todos los volumenes de todos los equipos —un fallo total y silencioso, porque
// la Fase 1b esta escrita para no arrastrar a la 1a: el panel mostraria estado
// de cifrado correcto con "protectores: null" para siempre.
//
// Vive en el archivo sin etiqueta de plataforma justamente por eso: es una
// regla de texto, se puede probar en Linux, y es la mas facil de romper.
func rutaDelVolumen(deviceID string) string {
	escapado := strings.ReplaceAll(deviceID, `\`, `\\`)
	escapado = strings.ReplaceAll(escapado, `"`, `\"`)
	return `Win32_EncryptableVolume.DeviceID="` + escapado + `"`
}
