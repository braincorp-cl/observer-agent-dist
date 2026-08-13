//go:build linux
// +build linux

package agent

import (
	"strconv"
	"strings"
	"testing"
)

// Feature 030 · Fase 2 · lo que se puede probar de Linux sin cámara.
//
// El caso de los DOS NODOS no es teórico: se midió el 2026-08-12 en un equipo
// real de la flota (FAZOCAR), que publica `/dev/video0` y `/dev/video1` para una
// sola cámara — uno es la captura y el otro los metadatos, y cuál queda primero
// depende del orden de enumeración del kernel. Un agente que asumiera
// `/dev/video0` fallaría en la mitad de los equipos y reportaría "sin cámara",
// que es falso.

func TestNumeroDeNodoOrdenaNumericamente(t *testing.T) {
	// Con orden alfabético, video10 iría antes que video2 y la cámara real
	// quedaría al final de la cola de intentos.
	if !(numeroDeNodo("/dev/video2") < numeroDeNodo("/dev/video10")) {
		t.Error("video2 tiene que ordenar antes que video10")
	}
}

func TestNumeroDeNodoNoRevientaConBasura(t *testing.T) {
	// Un nodo con nombre inesperado va al final, no al principio, y sobre todo
	// no hace fallar el ordenamiento.
	if numeroDeNodo("/dev/videoX") <= numeroDeNodo("/dev/video7") {
		t.Error("un nodo ilegible tiene que quedar al final de la cola")
	}
}

func TestHerramientasWebcamDescartanCuadrosDeCalentamiento(t *testing.T) {
	// La prueba que impide el ok falso: si alguien reescribe los argumentos y se
	// le pierde el descarte de cuadros, la foto vuelve a salir negra y nada más
	// lo detectaría hasta el terreno.
	n := strconv.Itoa(cuadrosDeCalentamiento)

	for _, h := range herramientasWebcam {
		unidos := strings.Join(h.args, " ")
		if !strings.Contains(unidos, n) {
			t.Errorf("%s no descarta los %s cuadros de calentamiento: %q", h.bin, n, unidos)
		}
	}
}

func TestHerramientasWebcamSustituyenNodoYDestino(t *testing.T) {
	// Los dos marcadores tienen que estar en cada herramienta: sin `%d` se
	// capturaría siempre del dispositivo por omisión (rompe el recorrido de
	// nodos) y sin `%s` la herramienta escribiría donde ella quiera.
	for _, h := range herramientasWebcam {
		unidos := strings.Join(h.args, " ")
		if !strings.Contains(unidos, "%d") {
			t.Errorf("%s no recibe el nodo de video: %q", h.bin, unidos)
		}
		if !strings.Contains(unidos, "%s") {
			t.Errorf("%s no recibe la ruta destino: %q", h.bin, unidos)
		}
	}
}

func TestHerramientasWebcamDistinguenCamaraOcupada(t *testing.T) {
	// "La cámara está ocupada" es información del caso —alguien está en una
	// videollamada— y no un fallo del producto. Sin estos fragmentos, ese caso
	// degradaría a `fallo_captura` y se leería como una avería.
	for _, h := range herramientasWebcam {
		if len(h.ocupada) == 0 {
			t.Errorf("%s no sabe reconocer el dispositivo ocupado", h.bin)
		}
	}
}

func TestContieneAlguno(t *testing.T) {
	if !contieneAlguno("ioctl: VIDIOC_STREAMON: Device or resource busy", []string{"Device or resource busy"}) {
		t.Error("tenía que reconocer el dispositivo ocupado")
	}
	if contieneAlguno("no such file or directory", []string{"Device or resource busy"}) {
		t.Error("no tenía que confundir un error distinto con ocupado")
	}
}
