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
//     GetConversionStatus() por COM ⇒ Fase 1b. Nulo dice "todavia no lo
//     leemos", que no es lo mismo que cero por ciento.
//   - KeyProtectorCount / KeyProtectorTypes: mismo motivo, GetKeyProtectors()
//     es un metodo ⇒ Fase 1b. Nulo no es "cero protectores".
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
