//go:build darwin
// +build darwin

package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Feature 030 · Fase 2 · T016 — el lado macOS del ayudante de captura.
//
// El porqué está entero en capturaaqua.go, que es el archivo que la CI sí mira.
// Acá está el cómo: armar y firmar el bundle, registrarlo, dejar el plist,
// despertar al ayudante y esperar su respuesta.

const (
	// capturaAquaPlazo es lo que el servicio espera al ayudante en una captura.
	//
	// La cuenta: abrir la cámara en macOS tarda entre uno y tres segundos
	// (`AVCaptureSession` negocia el formato con el controlador), más los 20
	// cuadros de calentamiento a 30 por segundo, más codificar y escribir. Con
	// 30 s sobra tres veces y, sobre todo, el ciclo de modo perdido no se queda
	// colgado: pasado el plazo reporta motivo y sigue con la ubicación, que vale
	// igual sin la foto.
	capturaAquaPlazo = 30 * time.Second

	// capturaAquaSondeo es cada cuánto se mira si ya hay respuesta. `kickstart`
	// vuelve en cuanto launchd acepta el encargo, no cuando el trabajo termina,
	// así que no hay nada que esperar salvo el archivo.
	capturaAquaSondeo = 200 * time.Millisecond

	// lsregisterBin registra el bundle en LaunchServices.
	//
	// 🪤 SIN ESTO EL AYUDANTE NO SE PUEDE REPARAR. Medido el 2026-08-15: con el
	// `.app` recién armado y bien firmado, `tccutil reset Camera <id>` responde
	// `No such bundle identifier` (rc=64); después de `lsregister -f`, responde
	// `Successfully reset` (rc=0). El bundle existe para que `tccutil` pueda
	// nombrarlo, y `tccutil` lo nombra a través de LaunchServices.
	lsregisterBin = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
)

// capturarPorAyudante es el camino único de la evidencia visual en macOS.
//
// La foto y la captura de pantalla comparten TODO salvo la línea que toca el
// dispositivo, así que comparten esta función: el buzón, el nonce, los permisos
// del archivo y la espera se escriben una vez. Cuando eran dos caminos, uno de
// los dos siempre estaba desactualizado.
func (a *Agent) capturarPorAyudante(tipo, nombre string) (string, string) {
	uid, _, err := consoleUser()
	if err != nil {
		a.Logger.Debugln("capturarPorAyudante: sin sesión de consola:", err)
		return "", captureNoSession
	}

	if err := a.prepararAyudanteCaptura(uid); err != nil {
		a.Logger.Debugln("capturarPorAyudante: no se pudo preparar el ayudante:", err)
		return "", captureFailed
	}

	destino := filepath.Join(capturaAquaDir, filepath.Base(nombre))
	pedido := capturaAquaPedido{
		ID:       nonceCaptura(),
		Tipo:     tipo,
		Destino:  destino,
		Descarte: cuadrosDeCalentamiento,
	}

	resp, err := a.encargarAlAyudante(pedido, uid, capturaAquaPlazo)
	if err != nil {
		a.Logger.Debugln("capturarPorAyudante:", tipo, "falló:", err)
		_ = os.Remove(destino)
		return "", captureFailed
	}
	if resp.Motivo != "" {
		_ = os.Remove(destino)
		return "", resp.Motivo
	}

	// El `exit 0` no prueba el efecto, y acá ni siquiera hay un `exit` que mirar:
	// hay un archivo que otro proceso dijo haber escrito. Se comprueba que exista
	// y que pese algo antes de darlo por bueno; lo de mirar los píxeles lo hacen
	// `CaptureWebcam` y `CaptureScreen`, igual que en las otras plataformas.
	info, err := os.Stat(destino)
	if err != nil || info.Size() == 0 {
		a.Logger.Debugln("capturarPorAyudante:", tipo, "dijo OK y no dejó archivo utilizable")
		_ = os.Remove(destino)
		return "", captureFailed
	}

	return destino, ""
}

// encargarAlAyudante deja el pedido, despierta al ayudante y espera su respuesta.
func (a *Agent) encargarAlAyudante(pedido capturaAquaPedido, uid string, plazo time.Duration) (capturaAquaRespuesta, error) {
	if err := a.dejarPedidoCaptura(pedido, uid); err != nil {
		return capturaAquaRespuesta{}, fmt.Errorf("dejar pedido: %w", err)
	}
	if err := a.despertarAyudanteCaptura(uid); err != nil {
		return capturaAquaRespuesta{}, fmt.Errorf("despertar: %w", err)
	}

	limite := time.Now().Add(plazo)
	ruta := capturaAquaRespuestaPath()

	for time.Now().Before(limite) {
		time.Sleep(capturaAquaSondeo)

		datos, err := os.ReadFile(ruta)
		if err != nil || len(datos) == 0 {
			continue
		}

		resp, valida := parsearRespuestaAqua(datos, pedido.ID)
		if !valida {
			// Todavía no hay respuesta de ESTE pedido: el ayudante puede estar
			// escribiéndola ahora mismo. Se sigue esperando.
			continue
		}
		if resp.Detalle != "" {
			a.Logger.Debugln("ayudante de captura:", pedido.Tipo, resp.Motivo, "-", resp.Detalle)
		}
		return resp, nil
	}

	return capturaAquaRespuesta{}, fmt.Errorf("el ayudante no respondió en %s", plazo)
}

// prepararAyudanteCaptura deja el bundle, el plist y el buzón en su sitio.
//
// Se hace en cada captura y no sólo en la instalación, a propósito: el bundle
// vive bajo `/opt/observeragent` y un `-m install` sobre una instalación previa
// lo borra entero; el binario cambia en cada auto-update y el ayudante tiene que
// seguirlo; y el plist puede faltar en cualquier equipo que venga de una versión
// anterior a T016. Reponerlos cuando hace falta cuesta dos `stat` y evita la
// clase de fallo que sólo aparece en los equipos actualizados, que son justo los
// que nadie prueba.
func (a *Agent) prepararAyudanteCaptura(uid string) error {
	if err := os.MkdirAll(capturaAquaDir, 0755); err != nil {
		return err
	}

	// Se barren las capturas que quedaron de ciclos anteriores. En el camino
	// normal las borra el ciclo después de subirlas, pero si el agente muere
	// entre la captura y la subida la foto de una cara se queda en disco sin que
	// nadie la reclame. ADR-025 fija plazos de retención para lo que llega al
	// servidor; en el equipo no debería quedar nada.
	if entradas, err := os.ReadDir(capturaAquaDir); err == nil {
		for _, e := range entradas {
			n := e.Name()
			if strings.HasPrefix(n, "webcam-") || strings.HasPrefix(n, "screen") {
				_ = os.Remove(filepath.Join(capturaAquaDir, n))
			}
		}
	}

	rearmado, err := a.asegurarBundleCaptura()
	if err != nil {
		return err
	}

	deseado := capturaAquaPlistContenido()
	actual, err := os.ReadFile(capturaAquaPlist)
	plistCambio := err != nil || string(actual) != deseado
	if plistCambio {
		if err := os.WriteFile(capturaAquaPlist, []byte(deseado), 0644); err != nil {
			return err
		}
	}

	if rearmado || plistCambio {
		// El trabajo cargado apunta al bundle anterior: hay que descargarlo o
		// launchd sigue ejecutando el ejecutable viejo. El `bootout` puede fallar
		// porque no estaba cargado, y eso no es error.
		//
		// Va acotado por llamada como el resto: `bootout` también espera a que el
		// trabajo muera, así que es de la misma familia que el `kickstart -k` y
		// tenía el mismo plazo corto.
		a.launchctlAcotado("bootout", "gui/"+uid+"/"+capturaAquaID)
	}

	return nil
}

// asegurarBundleCaptura arma el `.app` si falta o si el binario del servicio
// cambió. Devuelve si tuvo que rearmarlo.
//
// 🔑 EL EJECUTABLE DEL BUNDLE ES UNA COPIA, NO UN ENLACE, Y SE VUELVE A FIRMAR
// CON IDENTIFICADOR PROPIO. Un enlace simbólico dejaría la identidad de código
// del servicio (`cl.observer.agent`) dentro de un bundle que dice llamarse
// `cl.observer.agent.captura`, y esa contradicción es justo la clase de cosa que
// TCC resuelve de una forma que nadie predijo. La copia con `codesign -f -s -
// --identifier` deja las dos identidades de acuerdo.
//
// `codesign` y `lsregister` son parte del sistema base de macOS: no hace falta
// Xcode ni las herramientas de línea de comandos.
func (a *Agent) asegurarBundleCaptura() (bool, error) {
	origen, err := os.Stat(nixAgentBin)
	if err != nil {
		return false, err
	}

	// 🪤 EL TESTIGO NO PUEDE SER EL EJECUTABLE DEL BUNDLE, y creer que sí dejó
	// esta función rearmando el bundle EN CADA CONSULTA, para siempre.
	//
	// La versión anterior comparaba tamaño y fecha de la copia contra el binario
	// del servicio, y copiaba la fecha para que sirviera de testigo — pero
	// `codesign`, tres líneas más abajo, REESCRIBE esa copia: le agrega la firma
	// (+32 bytes medidos) y le pone la fecha del firmado. La comparación no
	// volvía a dar verdadera nunca.
	//
	// Medido el 2026-08-15 con la v2.15.24 en el Mac: una línea "armando el
	// bundle" cada 15 segundos, y el `tccutil reset` fallando con `rc=70` un
	// segundo después de un rearmado, contra un bundle que se estaba
	// reescribiendo debajo. El churn no era cosmético: rompía la reparación.
	//
	// El testigo es entonces una huella APARTE, con lo que se sabe del binario de
	// origen — que es lo único que decide si hay que rearmar.
	huella := fmt.Sprintf("%d|%d", origen.Size(), origen.ModTime().UnixNano())
	if previa, err := os.ReadFile(capturaAquaHuella); err == nil && string(previa) == huella {
		if _, err := os.Stat(capturaAquaAppExe); err == nil {
			return false, nil
		}
	}

	a.Logger.Infoln("asegurarBundleCaptura: armando el bundle del ayudante de captura")

	if err := os.MkdirAll(filepath.Dir(capturaAquaAppExe), 0755); err != nil {
		return false, err
	}
	if err := copiarArchivo(nixAgentBin, capturaAquaAppExe, 0755); err != nil {
		return false, err
	}
	// La fecha se copia para que la comparación de arriba sirva de testigo.
	if err := os.Chtimes(capturaAquaAppExe, origen.ModTime(), origen.ModTime()); err != nil {
		a.Logger.Debugln("asegurarBundleCaptura: no se pudo igualar la fecha:", err)
	}
	if err := os.WriteFile(capturaAquaAppPl, []byte(capturaAquaInfoPlist()), 0644); err != nil {
		return false, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if salida, err := exec.CommandContext(ctx, "/usr/bin/codesign",
		"-f", "-s", "-", "--identifier", capturaAquaID, capturaAquaAppDir).CombinedOutput(); err != nil {
		return false, fmt.Errorf("codesign: %w (%s)", err, strings.TrimSpace(string(salida)))
	}

	// Sin registrar, `tccutil` no puede nombrarlo y el permiso deja de ser
	// reparable — que es la razón entera de que el ayudante sea un bundle.
	if salida, err := exec.CommandContext(ctx, lsregisterBin, "-f", capturaAquaAppDir).CombinedOutput(); err != nil {
		a.Logger.Debugln("asegurarBundleCaptura: lsregister falló:", err, strings.TrimSpace(string(salida)))
	}

	// La huella se escribe AL FINAL y sólo si todo salió bien: si el armado se
	// cae a la mitad, la próxima vuelta tiene que volver a intentarlo.
	if err := os.WriteFile(capturaAquaHuella, []byte(huella), 0600); err != nil {
		a.Logger.Debugln("asegurarBundleCaptura: no se pudo dejar la huella:", err)
	}

	return true, nil
}

func copiarArchivo(origen, destino string, modo os.FileMode) error {
	src, err := os.Open(origen)
	if err != nil {
		return err
	}
	defer src.Close()

	// Se borra antes de crear: escribir sobre el ejecutable de un bundle firmado
	// invalida la firma de forma silenciosa en vez de reemplazarla.
	_ = os.Remove(destino)

	dst, err := os.OpenFile(destino, os.O_WRONLY|os.O_CREATE|os.O_EXCL, modo)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

// dejarPedidoCaptura escribe el encargo y prepara los archivos que el ayudante
// va a escribir.
//
// LOS CREA EL SERVICIO, no el ayudante, y con 0600 desde el primer instante. Es
// la diferencia entre que la foto de una cara nazca cerrada o que exista un
// intervalo —corto, pero real— en el que cualquiera con una sesión en el equipo
// puede leerla. El `chown` al usuario de consola es lo que permite que el
// ayudante, que corre sin privilegios, pueda escribir dentro.
func (a *Agent) dejarPedidoCaptura(p capturaAquaPedido, uid string) error {
	nuid, err := strconv.Atoi(uid)
	if err != nil {
		return err
	}

	datos, err := json.Marshal(p)
	if err != nil {
		return err
	}

	// El pedido lo lee el ayudante y lo escribe root: 0644 y sin chown.
	if err := os.WriteFile(capturaAquaPedidoPath(), datos, 0644); err != nil {
		return err
	}

	rutas := []string{capturaAquaRespuestaPath()}
	if p.Destino != "" {
		rutas = append(rutas, p.Destino)
	}

	for _, ruta := range rutas {
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

// despertarAyudanteCaptura le pide a launchd que corra el trabajo ahora.
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
//
// 🔑 EL PLAZO ES POR LLAMADA Y NO POR RONDA. Los tres `launchctl` compartían un
// único `context` de 15 s, y esa sola línea dejaba a la pantalla sin intentarse:
//
//  1. El ayudante de la cámara sigue vivo cuando llega el pedido siguiente — la
//     respuesta se escribe ANTES de que el proceso termine de bajarse, así que
//     el servicio la lee y encarga lo próximo contra un trabajo que todavía se
//     está muriendo.
//  2. El primer `kickstart -k` se queda esperando esa muerte y se come el
//     presupuesto entero.
//  3. El `bootstrap` y el `kickstart` de reserva reciben un contexto YA VENCIDO.
//     No es que fallen: es que no pueden salir bien. El reintento existía en el
//     código y no existía en la práctica.
//
// El síntoma en terreno el 2026-08-15 fue `kickstart: context deadline
// exceeded` en el paso de la pantalla cada vez que el de la cámara se demoraba,
// o sea justo cuando había una persona contestando un diálogo. La pantalla no
// se denegaba: no se pedía.
func (a *Agent) despertarAyudanteCaptura(uid string) error {
	destino := "gui/" + uid + "/" + capturaAquaID

	if _, err := a.launchctlAcotado("kickstart", "-k", destino); err == nil {
		return nil
	}

	if salida, err := a.launchctlAcotado("bootstrap", "gui/"+uid, capturaAquaPlist); err != nil {
		a.Logger.Debugln("despertarAyudanteCaptura: bootstrap:", err, salida)
	}

	salida, err := a.launchctlAcotado("kickstart", "-k", destino)
	if err != nil {
		return fmt.Errorf("kickstart: %w (%s)", err, salida)
	}
	return nil
}

// launchctlAcotado corre un `launchctl` con su PROPIO plazo.
//
// Existe para que el defecto de arriba no se pueda reintroducir: mientras el
// contexto no se pueda pasar desde afuera, dos llamadas no pueden compartir un
// presupuesto.
func (a *Agent) launchctlAcotado(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), capturaAquaDespertarPlazo)
	defer cancel()
	return a.correrLaunchctl(ctx, args...)
}

func (a *Agent) correrLaunchctl(ctx context.Context, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	salida, err := exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
	return string(salida), err
}

// descargarAyudanteCaptura saca el trabajo de la sesión del usuario y lo
// desregistra de LaunchServices.
//
// Se llama al desinstalar y antes de borrar el plist: al revés queda un trabajo
// registrado apuntando a un archivo que ya no existe, y launchd lo arrastra
// hasta que la persona cierre sesión. Los dos comandos pueden fallar porque
// nunca se cargó o nunca se registró, y eso no es un error.
func (a *Agent) descargarAyudanteCaptura() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if uid, _, err := consoleUser(); err == nil {
		if salida, err := a.correrLaunchctl(ctx, "bootout", "gui/"+uid+"/"+capturaAquaID); err != nil {
			a.Logger.Debugln("descargarAyudanteCaptura: bootout:", err, salida)
		}
	}

	if salida, err := exec.CommandContext(ctx, lsregisterBin, "-u", capturaAquaAppDir).CombinedOutput(); err != nil {
		a.Logger.Debugln("descargarAyudanteCaptura: lsregister -u:", err, strings.TrimSpace(string(salida)))
	}
}

func nonceCaptura() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Sin aleatoriedad se cae al reloj, que para distinguir un pedido del
		// anterior alcanza: lo que el nonce evita es confundir dos respuestas,
		// no resistir a un adversario.
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}

// RunCapturaAqua es el modo `-m capturaaqua`: el ayudante.
//
// 🚫 NO se invoca a mano. Lo corre launchd desde dentro del bundle y en la
// sesión Aqua, que son las dos condiciones que le dan su identidad de TCC y el
// contexto gráfico. Lanzado desde una terminal o por SSH haría otra cosa —o
// nada— y confundiría la medición.
func (a *Agent) RunCapturaAqua() {
	datos, err := os.ReadFile(capturaAquaPedidoPath())
	if err != nil {
		a.Logger.Debugln("RunCapturaAqua: no hay pedido que atender:", err)
		return
	}

	var pedido capturaAquaPedido
	if err := json.Unmarshal(datos, &pedido); err != nil || pedido.ID == "" {
		a.Logger.Debugln("RunCapturaAqua: pedido ilegible:", err)
		return
	}

	respuesta := capturaAquaRespuesta{ID: pedido.ID}

	switch pedido.Tipo {
	case capturaTipoWebcam:
		if pedido.Destino == "" {
			respuesta.Motivo = webcamFailed
			break
		}
		respuesta.Motivo, respuesta.Detalle = capturarWebcamNativo(pedido.Destino, pedido.Descarte, capturaAquaPlazo)

	case capturaTipoPantalla:
		if pedido.Destino == "" {
			respuesta.Motivo = captureFailed
			break
		}
		respuesta.Motivo, respuesta.Detalle = a.capturarPantallaAqua(pedido.Destino)

	case capturaTipoEstado:
		respuesta.Estado = estadoTCCNativo(pedido.Servicio)

	case capturaTipoPermiso:
		respuesta.Estado = pedirPermisoTCCNativo(pedido.Servicio, tccPlazoDialogo)

	default:
		respuesta.Motivo = captureFailed
		respuesta.Detalle = "tipo de pedido desconocido: " + pedido.Tipo
	}

	salida, err := json.Marshal(respuesta)
	if err != nil {
		return
	}
	if err := os.WriteFile(capturaAquaRespuestaPath(), salida, 0600); err != nil {
		a.Logger.Debugln("RunCapturaAqua: no se pudo dejar la respuesta:", err)
	}
}

// capturarPantallaAqua saca la captura desde DENTRO del bundle.
//
// Sigue siendo `/usr/sbin/screencapture` —es parte del sistema y no hay nada que
// instalar—, pero ahora corre como hijo del ayudante, así que el sujeto de TCC
// es el bundle y no el servicio. Ese cambio de sujeto es todo el arreglo: la
// fila del servicio quedó en «permitido» con un csreq viejo y no se puede
// reparar; la del bundle es nueva y sí.
//
// Ya no hace falta `launchctl asuser` ni `sudo -u`: el ayudante YA corre como el
// usuario de consola dentro de su sesión gráfica.
//
// Las banderas:
//
//	-x  sin sonido de obturador. En un equipo robado, el "clic" avisa al ladrón
//	    que alguien lo está mirando — y esta feature depende de no delatarse.
//	-C  sin el cursor del mouse dibujado: la evidencia es lo que hay en pantalla.
//	-t png  formato explícito, para no depender de la preferencia del usuario.
func (a *Agent) capturarPantallaAqua(destino string) (string, string) {
	if !pantallaPreflightNativo() {
		// Se CONSULTA y no se pide: `CGRequestScreenCaptureAccess` abre un
		// diálogo, y un diálogo en un equipo robado es exactamente lo que no
		// puede pasar. El permiso se pide en la ronda de T016b/T016c.
		return captureDenied, ""
	}

	// 🪤 NO SE CAPTURA DIRECTO SOBRE `destino`, aunque `destino` sea escribible.
	//
	// `screencapture` no escribe el archivo que se le pide: escribe un temporal
	// HERMANO en el mismo directorio (`..<nombre>-XXXX`) y recién al final lo
	// renombra. El buzón es de root con 0755 a propósito —para que nadie más
	// pueda dejar ahí un pedido ni una respuesta falsa—, así que el ayudante,
	// que corre como el usuario de consola, no puede crear ese temporal y la
	// captura muere con `error = 13 (Permission denied)`.
	//
	// Medido en terreno el 2026-08-15, y con la peor cara posible: `screencapture`
	// igual sale con 0. Sin permiso de TCC el síntoma es EXACTAMENTE el mismo
	// —sale con 0 y no deja archivo—, así que durante un rato esto se leyó como
	// una denegación de permiso que no existía.
	//
	// El temporal va al directorio temporal del propio usuario (0700, suyo), y de
	// ahí se copia DENTRO del archivo ya precreado 0600: copiar y no mover es lo
	// que conserva el dueño y el modo con que la evidencia nació cerrada.
	tmpDir, err := os.MkdirTemp("", "observercaptura")
	if err != nil {
		return captureFailed, "no se pudo crear el directorio temporal: " + err.Error()
	}
	defer os.RemoveAll(tmpDir)

	tmp := filepath.Join(tmpDir, "pantalla.png")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	salida, err := exec.CommandContext(ctx, "/usr/sbin/screencapture",
		"-x", "-C", "-t", "png", tmp).CombinedOutput()
	if err != nil {
		return captureFailed, strings.TrimSpace(string(salida))
	}

	// El `exit 0` no prueba el efecto: sin permiso, `screencapture` sale con 0 y
	// puede no dejar nada. Lo que prueba la captura es el archivo.
	info, err := os.Stat(tmp)
	if err != nil || info.Size() == 0 {
		return captureFailed, "screencapture salio con 0 y no dejo archivo utilizable"
	}

	if err := copiarSobreArchivo(tmp, destino); err != nil {
		return captureFailed, "no se pudo dejar la captura en el buzon: " + err.Error()
	}

	return "", ""
}

// copiarSobreArchivo vuelca `origen` DENTRO de `destino`, que ya existe con el
// dueño y el modo que le puso el servicio. Se abre sin O_CREATE a propósito: si
// el archivo precreado no está, algo se saltó el protocolo del buzón y es mejor
// fallar que crear uno nuevo con permisos de nadie.
func copiarSobreArchivo(origen, destino string) error {
	src, err := os.Open(origen)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(destino, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	return dst.Sync()
}
