//go:build darwin
// +build darwin

package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Feature 030 · Fase 2 · T016 — el lado macOS del ayudante Aqua.
//
// El porqué está entero en webcamaqua.go, que es el archivo que la CI sí mira.
// Acá está el cómo: crear el buzón, dejar el plist, despertar al ayudante y
// esperar su respuesta.

const (
	// webcamAquaPlazo es lo que el servicio espera al ayudante.
	//
	// La cuenta: abrir la cámara en macOS tarda entre uno y tres segundos
	// (`AVCaptureSession` negocia el formato con el controlador), más los 20
	// cuadros de calentamiento a 30 por segundo, más codificar y escribir. Con
	// 30 s sobra tres veces y, sobre todo, el ciclo de modo perdido no se queda
	// colgado: pasado el plazo reporta motivo y sigue con la ubicación y la
	// pantalla, que valen igual sin la foto.
	webcamAquaPlazo = 30 * time.Second

	// webcamAquaSondeo es cada cuánto se mira si ya hay respuesta. `kickstart`
	// vuelve en cuanto launchd acepta el encargo, no cuando el trabajo termina,
	// así que no hay nada que esperar salvo el archivo.
	webcamAquaSondeo = 200 * time.Millisecond
)

// capturarWebcam en macOS no toca la cámara: se la encarga al ayudante Aqua.
//
// 🔑 ESTE ES EL CAMBIO DE T016 Y ES TODO EL CAMBIO. La versión anterior buscaba
// `imagesnap` o `ffmpeg` y los lanzaba con `launchctl asuser`. Dos errores en
// uno: las herramientas no vienen con el sistema (y en el Mac medido no estaban),
// y aunque hubieran estado, `asuser` no da contexto Aqua — habrían devuelto
// cuadros vacíos sin error, que es peor que fallar. Ver webcamaqua.go.
func (a *Agent) capturarWebcam(nombre string) WebcamShot {
	uid, _, err := consoleUser()
	if err != nil {
		// Acá macOS ROMPE la promesa que webcam.go hace para las otras
		// plataformas: sin sesión gráfica abierta no hay contexto Aqua y no hay
		// foto, aunque la cámara sea un dispositivo del sistema y el permiso
		// esté concedido. Se dice `sin_sesion` —el mismo código que ya usa la
		// captura de pantalla— y no `permiso_denegado`, porque son cosas
		// distintas y quien sigue el caso necesita saber cuál: al equipo sin
		// sesión no le falta un permiso, le falta que alguien entre.
		a.Logger.Debugln("capturarWebcam: sin sesión de consola:", err)
		return WebcamShot{Reason: webcamNoSession}
	}

	if err := a.prepararBuzonCamara(uid); err != nil {
		a.Logger.Debugln("capturarWebcam: no se pudo preparar el buzón:", err)
		return WebcamShot{Reason: webcamFailed}
	}

	destino := filepath.Join(webcamAquaDir, filepath.Base(nombre))
	pedido := webcamAquaPedido{
		ID:       nonceCamara(),
		Destino:  destino,
		Descarte: cuadrosDeCalentamiento,
	}

	if err := a.dejarPedidoCamara(pedido, uid); err != nil {
		a.Logger.Debugln("capturarWebcam: no se pudo dejar el pedido:", err)
		return WebcamShot{Reason: webcamFailed}
	}

	if err := a.despertarAyudanteCamara(uid); err != nil {
		a.Logger.Debugln("capturarWebcam: no se pudo despertar al ayudante:", err)
		_ = os.Remove(destino)
		return WebcamShot{Reason: webcamFailed}
	}

	motivo, listo := a.esperarRespuestaCamara(pedido.ID)
	if !listo {
		_ = os.Remove(destino)
		return WebcamShot{Reason: motivo}
	}

	// El `exit 0` no prueba el efecto, y acá ni siquiera hay un `exit` que mirar:
	// hay un archivo que otro proceso dijo haber escrito. Se comprueba que exista
	// y que pese algo antes de darlo por bueno; lo de mirar los píxeles lo hace
	// `CaptureWebcam`, igual que en las otras plataformas.
	info, err := os.Stat(destino)
	if err != nil || info.Size() == 0 {
		a.Logger.Debugln("capturarWebcam: el ayudante dijo OK y no dejó archivo utilizable")
		_ = os.Remove(destino)
		return WebcamShot{Reason: webcamFailed}
	}

	return WebcamShot{Path: destino}
}

// prepararBuzonCamara deja el directorio y el plist en su sitio.
//
// Se hace en cada captura y no sólo en la instalación, a propósito: el
// directorio vive bajo `/opt/observeragent` y un `-m install` sobre una
// instalación previa lo borra entero, y el plist puede faltar en cualquier
// equipo que venga de una versión anterior a T016. Reponerlos acá cuesta dos
// llamadas al sistema y evita la clase de fallo que sólo aparece en los equipos
// actualizados, que son justo los que nadie prueba.
func (a *Agent) prepararBuzonCamara(uid string) error {
	if err := os.MkdirAll(webcamAquaDir, 0755); err != nil {
		return err
	}

	// Se barren las fotos que quedaron de ciclos anteriores. En el camino normal
	// las borra el ciclo después de subirlas, pero si el agente muere entre la
	// captura y la subida la foto de una cara se queda en disco sin que nadie la
	// reclame. ADR-025 fija plazos de retención para lo que llega al servidor; en
	// el equipo no debería quedar nada, y este barrido es lo que lo garantiza.
	if entradas, err := os.ReadDir(webcamAquaDir); err == nil {
		for _, e := range entradas {
			if strings.HasPrefix(e.Name(), "webcam-") {
				_ = os.Remove(filepath.Join(webcamAquaDir, e.Name()))
			}
		}
	}

	deseado := webcamAquaPlistContenido(nixAgentBin)
	actual, err := os.ReadFile(webcamAquaPlist)
	if err != nil || string(actual) != deseado {
		if err := os.WriteFile(webcamAquaPlist, []byte(deseado), 0644); err != nil {
			return err
		}
		// El plist cambió: hay que recargarlo o launchd sigue con el anterior.
		// El `bootout` puede fallar porque no estaba cargado, y eso no es error.
		a.correrLaunchctl(context.Background(), "bootout", "gui/"+uid+"/"+webcamAquaLabel)
	}

	return nil
}

// dejarPedidoCamara escribe el encargo y prepara los dos archivos que el
// ayudante va a escribir.
//
// LOS CREA EL SERVICIO, no el ayudante, y con 0600 desde el primer instante.
// Es la diferencia entre que la foto de una cara nazca cerrada o que exista un
// intervalo —corto, pero real— en el que cualquiera con una sesión en el equipo
// puede leerla. El `chown` al usuario de consola es lo que permite que el
// ayudante, que corre sin privilegios, pueda escribir dentro.
func (a *Agent) dejarPedidoCamara(p webcamAquaPedido, uid string) error {
	nuid, err := strconv.Atoi(uid)
	if err != nil {
		return err
	}

	datos, err := json.Marshal(p)
	if err != nil {
		return err
	}

	// El pedido lo lee el ayudante y lo escribe root: 0644 y sin chown.
	if err := os.WriteFile(webcamAquaPedidoPath(), datos, 0644); err != nil {
		return err
	}

	for _, ruta := range []string{p.Destino, webcamAquaRespuestaPath()} {
		_ = os.Remove(ruta)
		if err := os.WriteFile(ruta, nil, 0600); err != nil {
			return err
		}
		// El grupo se deja como está (-1): el archivo es 0600, así que el grupo
		// no da acceso a nadie.
		if err := os.Chown(ruta, nuid, -1); err != nil {
			return err
		}
	}

	return nil
}

// despertarAyudanteCamara le pide a launchd que corra el trabajo ahora.
//
// `-k` mata una corrida anterior que siguiera viva: si el ciclo previo se colgó
// esperando a una cámara que otro programa tenía tomada, arrastrar ese proceso
// dejaría este pedido sin atender y el motivo sería un plazo agotado que no
// explica nada.
//
// El `bootstrap` de reserva cubre el caso real de la flota: un equipo que se
// actualizó a esta versión con la sesión ya abierta. El plist recién aparece,
// launchd no lo ha visto y `kickstart` responde que no existe ese trabajo.
// Cargarlo a mano en el dominio `gui/<uid>` lo resuelve sin pedirle a nadie que
// cierre sesión.
func (a *Agent) despertarAyudanteCamara(uid string) error {
	destino := "gui/" + uid + "/" + webcamAquaLabel

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if salida, err := a.correrLaunchctl(ctx, "kickstart", "-k", destino); err == nil {
		return nil
	} else {
		a.Logger.Debugln("despertarAyudanteCamara: kickstart falló:", err, "salida:", salida)
	}

	if salida, err := a.correrLaunchctl(ctx, "bootstrap", "gui/"+uid, webcamAquaPlist); err != nil {
		a.Logger.Debugln("despertarAyudanteCamara: bootstrap falló:", err, "salida:", salida)
	}

	salida, err := a.correrLaunchctl(ctx, "kickstart", "-k", destino)
	if err != nil {
		return fmt.Errorf("kickstart: %w (%s)", err, salida)
	}
	return nil
}

func (a *Agent) correrLaunchctl(ctx context.Context, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	salida, err := exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
	return string(salida), err
}

// esperarRespuestaCamara sondea el buzón hasta que llega la respuesta del
// pedido correcto o hasta agotar el plazo.
func (a *Agent) esperarRespuestaCamara(id string) (string, bool) {
	limite := time.Now().Add(webcamAquaPlazo)
	ruta := webcamAquaRespuestaPath()

	for time.Now().Before(limite) {
		time.Sleep(webcamAquaSondeo)

		datos, err := os.ReadFile(ruta)
		if err != nil || len(datos) == 0 {
			continue
		}

		r, valida := parsearRespuestaAqua(datos, id)
		if !valida {
			// Todavía no hay respuesta de ESTE pedido: el ayudante puede estar
			// escribiéndola ahora mismo. Se sigue esperando.
			continue
		}
		if r.Detalle != "" {
			a.Logger.Debugln("ayudante Aqua:", r.Motivo, "-", r.Detalle)
		}
		if r.Motivo == "" {
			return "", true
		}
		return r.Motivo, false
	}

	a.Logger.Debugln("esperarRespuestaCamara: el ayudante Aqua no respondió en", webcamAquaPlazo)
	return webcamFailed, false
}

func nonceCamara() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Sin aleatoriedad se cae al reloj, que para distinguir un pedido del
		// anterior alcanza: lo que el nonce evita es confundir dos respuestas,
		// no resistir a un adversario.
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}

// descargarAyudanteCamara saca el trabajo de la sesión del usuario.
//
// Se llama al desinstalar y antes de borrar el plist: al revés queda un trabajo
// registrado apuntando a un archivo que ya no existe, y launchd lo arrastra
// hasta que la persona cierre sesión. El `bootout` puede fallar porque nunca se
// cargó —una instalación que jamás sacó una foto— y eso no es un error: por eso
// el resultado sólo va al log de depuración.
func (a *Agent) descargarAyudanteCamara() {
	uid, _, err := consoleUser()
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if salida, err := a.correrLaunchctl(ctx, "bootout", "gui/"+uid+"/"+webcamAquaLabel); err != nil {
		a.Logger.Debugln("descargarAyudanteCamara:", err, "salida:", salida)
	}
}

// RunWebcamAqua es el modo `-m webcamaqua`: el ayudante.
//
// 🚫 NO se invoca a mano. Lo corre launchd dentro de la sesión Aqua, que es el
// único contexto donde la cámara entrega cuadros. Lanzado desde una terminal o
// por SSH haría otra cosa —o nada— y confundiría la medición.
func (a *Agent) RunWebcamAqua() {
	respuesta := webcamAquaRespuesta{Motivo: webcamFailed}

	datos, err := os.ReadFile(webcamAquaPedidoPath())
	if err != nil {
		a.Logger.Debugln("RunWebcamAqua: no hay pedido que atender:", err)
		return
	}

	var pedido webcamAquaPedido
	if err := json.Unmarshal(datos, &pedido); err != nil || pedido.ID == "" || pedido.Destino == "" {
		a.Logger.Debugln("RunWebcamAqua: pedido ilegible:", err)
		return
	}

	respuesta.ID = pedido.ID
	respuesta.Motivo, respuesta.Detalle = capturarWebcamNativo(pedido.Destino, pedido.Descarte, webcamAquaPlazo)

	salida, err := json.Marshal(respuesta)
	if err != nil {
		return
	}
	if err := os.WriteFile(webcamAquaRespuestaPath(), salida, 0600); err != nil {
		a.Logger.Debugln("RunWebcamAqua: no se pudo dejar la respuesta:", err)
	}
}
