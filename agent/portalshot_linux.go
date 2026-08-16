//go:build linux
// +build linux

package agent

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/godbus/dbus/v5"
)

// Feature 030 · Fase 3 · T021 — el lado que SÍ habla con el portal de Wayland.
//
// ── POR QUÉ ESTO ES UN RE-EJECUTABLE Y NO UNA LLAMADA EN SITIO ─────────────────
//
// El agente corre como root, y el bus de sesión NO acepta a root: se autentica
// por el uid del par del socket (SO_PEERCRED) y sólo deja entrar al dueño. Por
// eso este modo se re-ejecuta con las credenciales del usuario del escritorio
// (newSessionCmdFor, el mismo puente de la 028) y recién ahí conecta al bus.
//
// ── EL DEFECTO QUE ESTO CORRIGE (terreno, GNOME Shell 50.4, 2026-08-16) ────────
//
// 🪤 El portal NO devuelve la captura: el método devuelve el camino de un objeto
// `Request` y la respuesta llega DESPUÉS, como señal `Request::Response`. Esa
// señal viaja DIRIGIDA al nombre único del cliente que pidió — no se difunde. La
// versión anterior pedía con `gdbus call` en un proceso y escuchaba con
// `gdbus monitor` en OTRO: el `call` salía apenas recibía el handle y nunca
// escuchaba, y el `monitor` —otro nombre único en el bus— jamás veía la señal
// dirigida. Resultado medido: el portal CONCEDÍA y sacaba la foto, pero el
// agente reportaba "el portal no respondió en 45s" y marcaba el enrolamiento
// como fallido. La corrección es una sola cosa: pedir y escuchar en la MISMA
// conexión D-Bus, que es justo lo que `gdbus` CLI no puede hacer y `godbus` sí.
//
// Correr como el usuario resuelve además, de paso, el segundo problema viejo: el
// portal suele dejar la imagen en un lugar que root no puede leer (el montaje
// FUSE del document portal, o el `~/Pictures` del usuario). Acá la copia la hace
// el propio usuario, en proceso, sin depender de `cp` ni de permisos de root.

// RunPortalScreenshot corre en el contexto del usuario y saca UNA captura por el
// portal `org.freedesktop.portal.Screenshot`. Escribe el PNG en `destino` (que
// el padre eligió en un directorio escribible por el usuario) y contesta con el
// contrato de una línea de main.go: `OK <destino>` o `FAIL <motivo>`.
//
// `interactive=false`: no se pide el selector de área. El diálogo de permiso, si
// el portal decide mostrarlo, aparece igual la primera vez — y ese es justamente
// el momento del enrolamiento, con alguien delante. En capturas de modo perdido
// posteriores el permiso ya está y no hay diálogo.
func (a *Agent) RunPortalScreenshot(destino string) {
	if destino == "" {
		fmt.Println(prefijoCapturaFallo + captureFailed)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), waylandPlazoPortal)
	defer cancel()

	if err := a.capturaPortalEnSitio(ctx, destino); err != nil {
		a.Logger.Debugln("RunPortalScreenshot:", err)
		fmt.Println(prefijoCapturaFallo + motivoDeError(err))
		return
	}

	fmt.Println(prefijoCapturaOK + destino)
}

// errPortalCancelado marca la única falla que el padre trata distinto: alguien
// apretó "No compartir".
var errPortalCancelado = fmt.Errorf(captureWaylandCancelado)

func motivoDeError(err error) string {
	if err == errPortalCancelado {
		return captureWaylandCancelado
	}
	return captureFailed
}

// capturaPortalEnSitio es el baile del portal en una sola conexión.
func (a *Agent) capturaPortalEnSitio(ctx context.Context, destino string) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("no se pudo conectar al bus de sesión: %w", err)
	}
	defer conn.Close()

	if len(conn.Names()) == 0 {
		return fmt.Errorf("la conexión no tiene nombre único en el bus")
	}
	token := tokenPortal(os.Getpid(), time.Now())
	esperado := rutaPeticionPortal(conn.Names()[0], token)

	// 🔑 Suscribirse ANTES de pedir. Si se hiciera después, la respuesta podría
	// llegar en la ventana entre el `call` y el `AddMatch` y perderse: sería un
	// fallo intermitente dependiente de la carga, el peor de todos.
	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath(esperado),
		dbus.WithMatchInterface(portalRequestIface),
		dbus.WithMatchMember("Response"),
	); err != nil {
		return fmt.Errorf("no se pudo suscribir a la respuesta del portal: %w", err)
	}

	señales := make(chan *dbus.Signal, 8)
	conn.Signal(señales)

	opciones := map[string]dbus.Variant{
		"interactive":  dbus.MakeVariant(false),
		"modal":        dbus.MakeVariant(false),
		"handle_token": dbus.MakeVariant(token),
	}

	obj := conn.Object(portalDest, dbus.ObjectPath(portalPath))
	var handle dbus.ObjectPath
	if err := obj.CallWithContext(ctx, portalMetodo, 0, "", opciones).Store(&handle); err != nil {
		return fmt.Errorf("el portal rechazó la petición: %w", err)
	}

	// Los portales modernos honran el `handle_token` y el handle devuelto es el
	// que predijimos. Por si una versión vieja lo ignora, nos suscribimos también
	// al que efectivamente devolvió.
	if handle != esperado && handle != "" {
		conn.AddMatchSignal(
			dbus.WithMatchObjectPath(handle),
			dbus.WithMatchInterface(portalRequestIface),
			dbus.WithMatchMember("Response"),
		)
	}

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("el portal no respondió en %s", waylandPlazoPortal)
		case sig := <-señales:
			if sig.Path != esperado && sig.Path != handle {
				continue
			}
			codigo, uri, err := leerRespuestaPortal(sig)
			if err != nil {
				return err
			}
			switch codigo {
			case 0:
				return copiarCapturaDelPortal(uri, destino)
			case 1:
				return errPortalCancelado
			default:
				return fmt.Errorf("el portal respondió con el código %d", codigo)
			}
		}
	}
}

// leerRespuestaPortal saca el código y el URI del cuerpo de la señal Response.
//
// El cuerpo es `(uint32 código, a{sv} resultados)`. El URI sólo viene cuando el
// código es 0; en cancelación y error el mapa está vacío, y eso no es un fallo
// de parseo sino la forma normal de esas respuestas.
func leerRespuestaPortal(sig *dbus.Signal) (uint32, string, error) {
	if len(sig.Body) < 2 {
		return 0, "", fmt.Errorf("respuesta del portal con cuerpo incompleto: %v", sig.Body)
	}

	codigo, ok := sig.Body[0].(uint32)
	if !ok {
		return 0, "", fmt.Errorf("el código del portal no es un uint32: %T", sig.Body[0])
	}

	resultados, ok := sig.Body[1].(map[string]dbus.Variant)
	if !ok {
		return codigo, "", nil
	}

	if v, hay := resultados["uri"]; hay {
		if s, ok := v.Value().(string); ok {
			return codigo, s, nil
		}
	}
	return codigo, "", nil
}

// copiarCapturaDelPortal mueve la imagen que dejó el portal a `destino`.
//
// El original se borra en el acto: GNOME 50 con `interactive=false` guarda la
// captura como un archivo de verdad en `~/Pictures`, y una foto de un modo
// perdido no debe quedar en la galería del usuario. Si el original está en un
// montaje de sólo lectura (el document portal), el borrado falla y se ignora.
func copiarCapturaDelPortal(uri, destino string) error {
	origen, ok := rutaDesdeURI(uri)
	if !ok {
		return fmt.Errorf("el portal concedió pero devolvió un URI inservible: %q", uri)
	}

	datos, err := os.ReadFile(origen)
	if err != nil {
		return fmt.Errorf("no se pudo leer la captura del portal: %w", err)
	}
	if len(datos) == 0 {
		return fmt.Errorf("la captura del portal vino vacía")
	}

	if err := os.WriteFile(destino, datos, 0600); err != nil {
		return fmt.Errorf("no se pudo escribir la captura en %s: %w", destino, err)
	}

	os.Remove(origen)
	return nil
}
