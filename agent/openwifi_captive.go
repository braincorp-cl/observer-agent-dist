package agent

import (
	"io"
	"net/http"
	"strings"
	"time"
)

// Feature 041 · RF-05 (T017) · sonda de conectividad y detección de portal cautivo.
//
// Es la pieza que distingue "estoy asociado y CON salida a Internet" de "estoy
// asociado pero un portal cautivo me está interceptando". El orquestador de
// openwifi.go la usa dos veces: como gate ("¿ya hay conectividad utilizable?",
// para no tocar la WiFi si el equipo ya tiene red) y tras asociarse a una red
// abierta ("¿esta red me deja salir o me tiró a un login?").
//
// NUNCA auto-autentica: ante un portal, la única acción es reportarlo para que el
// orquestador se desconecte. Seguir la redirección o mandar credenciales sería
// exactamente lo que la política prohíbe.
//
// La sonda va en HTTP EN CLARO a propósito: los portales cautivos interceptan
// HTTP (no pueden interceptar HTTPS sin romper el TLS), así que un 204 por HTTP es
// la señal limpia de "no hay portal". El tráfico real al backend sigue por TLS.

type estadoConectividad int

const (
	// conectividadSinRed: la sonda no llegó a ningún endpoint (sin salida).
	conectividadSinRed estadoConectividad = iota
	// conectividadUtilizable: hay salida real a Internet (sin portal).
	conectividadUtilizable
	// conectividadCautiva: asociado, pero un portal cautivo intercepta.
	conectividadCautiva
)

// Endpoints de sonda "sin contenido". generate_204 devuelve 204 y cuerpo vacío
// cuando hay salida real; un portal responde 200 con HTML o 302 al login. El
// fallback de Microsoft devuelve un cuerpo EXACTO conocido.
const (
	sondaGenerate204 = "http://connectivitycheck.gstatic.com/generate_204"
	sondaMsftURL     = "http://www.msftconnecttest.com/connecttest.txt"
	sondaMsftBody    = "Microsoft Connect Test"
)

// nuevoClienteSonda crea un cliente HTTP dedicado a la sonda: timeout corto y
// SIN seguir redirecciones (la redirección de un portal es la señal, no algo a
// seguir). No reusa a.rClient porque ése apunta al backend por TLS.
func nuevoClienteSonda() *http.Client {
	return &http.Client{
		Timeout: 6 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// sondearConectividad clasifica el estado de la red actual:
//
//	204 y cuerpo vacío en generate_204 ⇒ utilizable
//	2xx/3xx con cuerpo / redirección   ⇒ cautiva (portal interceptando)
//	fallo de transporte en ambos       ⇒ sin red
//
// El fallback de Microsoft cubre el caso en que gstatic sea inalcanzable por
// filtrado y no por falta de red.
func sondearConectividad(a *Agent) estadoConectividad {
	c := nuevoClienteSonda()

	if resp, err := c.Get(sondaGenerate204); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNoContent {
			return conectividadUtilizable
		}
		// 200 con HTML, 302 al login, etc.: un portal está respondiendo.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return conectividadCautiva
	} else {
		a.Logger.Debugln("openwifi: sonda generate_204 sin respuesta:", err.Error())
	}

	resp2, err2 := c.Get(sondaMsftURL)
	if err2 != nil {
		a.Logger.Debugln("openwifi: sonda msft sin respuesta:", err2.Error())
		return conectividadSinRed
	}
	defer resp2.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp2.Body, 1<<16))
	if resp2.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == sondaMsftBody {
		return conectividadUtilizable
	}
	return conectividadCautiva
}
