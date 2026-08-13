//go:build windows
// +build windows

package agent

import (
	"fmt"
	"image/jpeg"
	"os"
	"time"

	ole "github.com/go-ole/go-ole"
	"golang.org/x/sys/windows/registry"
)

// Feature 030 · Fase 2 · foto de webcam en Windows (T014).
//
// A DIFERENCIA DE LA CAPTURA DE PANTALLA, ACÁ NO HAY PUENTE DE SESIÓN. La
// pantalla obliga a re-ejecutarse dentro de la sesión del usuario porque un
// BitBlt desde la sesión 0 entrega negro; la cámara es un dispositivo y Media
// Foundation la abre desde el servicio. Es la misma asimetría que en Linux, y
// tiene la misma consecuencia buena: la foto funciona en la pantalla de inicio
// de sesión, antes de que quien se llevó el equipo entre a ninguna cuenta.
//
// EL PERMISO SE LEE, NO SE CAMBIA — y en la práctica casi nunca hace falta.
// Windows guarda el consentimiento de cámara en
// `CapabilityAccessManager\ConsentStore\webcam`, y el agente corre como SYSTEM,
// así que técnicamente PODRÍA escribirlo. No lo hace: encender el acceso a la
// cámara de la máquina de alguien es una decisión de gobernanza, no un detalle de
// implementación.
//
// 🔑 MEDIDO EN TERRENO el 2026-08-13: con el ConsentStore en `Deny`, la captura
// **funcionó igual**. Ese registro gobierna a las aplicaciones del USUARIO (UWP y
// de escritorio), no a un servicio corriendo como SYSTEM, que abre el dispositivo
// por debajo de esa capa. La consecuencia para la feature es la que importa: un
// equipo robado cuyo dueño «apagó la cámara» en Configuración IGUAL entrega la
// foto. Se lee el valor sólo para poder ponerle nombre a `permiso_denegado` en el
// caso —improbable pero no imposible— de que Media Foundation sí devuelva acceso
// denegado; nunca para decidir si intentar la foto.

// consentStoreWebcam es donde Windows guarda si las aplicaciones de escritorio
// pueden usar la cámara. El valor es texto: "Allow" o "Deny".
const consentStoreWebcam = `SOFTWARE\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\webcam`

// permisoDeCamaraNegado dice si Windows tiene la cámara cerrada por política.
//
// Devuelve `false` ante cualquier duda —clave ausente, valor inesperado, error
// de lectura— y esa asimetría es deliberada: el que decide de verdad es Media
// Foundation al abrir el dispositivo. Esto sólo existe para poder ponerle nombre
// al fracaso cuando ya ocurrió. Adivinar que está negado y no intentar la foto
// sería peor: perdería la evidencia de un equipo donde sí se podía.
func permisoDeCamaraNegado() bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, consentStoreWebcam, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()

	v, _, err := k.GetStringValue("Value")
	if err != nil {
		return false
	}
	// Comparación invariante y en ASCII: el valor lo escribe Windows en inglés
	// aunque el equipo esté en español, y así está medido en la flota.
	return v == "Deny"
}

func (a *Agent) capturarWebcam(nombre string) WebcamShot {
	// COM por hilo, igual que en el control de volumen de la 028. Se pide el
	// modelo multihilo porque acá no hay bomba de mensajes que atender.
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		// Que ya esté inicializado en otro modo no es un fallo: se sigue.
		a.Logger.Debugln("capturarWebcam: CoInitializeEx:", err)
	} else {
		defer ole.CoUninitialize()
	}

	img, motivo, err := capturarConMediaFoundation(cuadrosDeCalentamiento)
	if err != nil {
		a.Logger.Debugln("capturarWebcam:", err)
	}
	if img == nil {
		if motivo == "" || motivo == webcamFailed {
			// El registro no decide, pero sí explica: si el consentimiento está
			// en Deny, ése es el motivo verdadero y no un fallo genérico.
			if permisoDeCamaraNegado() {
				a.Logger.Infoln("capturarWebcam: el ConsentStore de Windows tiene la cámara en Deny")
				return WebcamShot{Reason: webcamDenied}
			}
			motivo = webcamFailed
		}
		return WebcamShot{Reason: motivo}
	}

	destino := destinoFotoWebcam(nombre)
	f, err := os.OpenFile(destino, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		a.Logger.Debugln("capturarWebcam: no se pudo crear el archivo:", err)
		return WebcamShot{Reason: webcamFailed}
	}

	// Se codifica acá y no en la capa común porque Windows es la única
	// plataforma donde el agente tiene los píxeles en la mano: en Linux y macOS
	// el archivo lo escribe la herramienta del sistema.
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 85}); err != nil {
		f.Close()
		_ = os.Remove(destino)
		a.Logger.Debugln("capturarWebcam: no se pudo codificar el JPEG:", err)
		return WebcamShot{Reason: webcamFailed}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(destino)
		return WebcamShot{Reason: webcamFailed}
	}

	return WebcamShot{Path: destino}
}

// RunWebcamCapture es el modo `-m webcamcapture`.
//
// No lo usa el ciclo del modo perdido —la captura corre en el propio servicio—:
// existe para poder MEDIR la cámara en un equipo real sin tener que marcarlo
// como perdido, que sube capturas de pantalla y ubicación de alguien. Fue la
// herramienta con la que se probó T014 en terreno, y se queda porque el próximo
// que dude de esta plataforma va a necesitar exactamente lo mismo.
//
// Imprime una sola línea, con el mismo contrato que `-m screencapture`.
func (a *Agent) RunWebcamCapture(nombre string) {
	if nombre == "" {
		nombre = webcamFilename(time.Now())
	}

	shot := a.CaptureWebcam()
	if !shot.OK() {
		fmt.Println(prefijoCapturaFallo + shot.Reason)
		return
	}
	fmt.Println(prefijoCapturaOK + shot.Path)
}
