//go:build linux

package agent

// 049-E · Linux: el servicio corre como root y lee los eventos de teclado y
// mouse de /dev/input. Funciona igual en X11 y en Wayland. Del evento sólo se
// mira el TIPO (tecla, botón, movimiento): el código de la tecla se descarta.

import (
	"bufio"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var tamEventoInput = int(unsafe.Sizeof(syscall.Timeval{})) + 8

var entradaLinux struct {
	mu       sync.Mutex
	activo   bool
	abiertos map[string]*os.File
	parar    chan struct{}
}

// dispositivosEntrada lee /proc/bus/input/devices y devuelve los eventN de
// teclados y mouse/touchpad.
func dispositivosEntrada() []string {
	f, err := os.Open("/proc/bus/input/devices")
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := sc.Text()
		if !strings.HasPrefix(l, "H: Handlers=") {
			continue
		}
		campos := strings.Fields(strings.TrimPrefix(l, "H: Handlers="))
		util := false
		ev := ""
		for _, c := range campos {
			if c == "kbd" || strings.HasPrefix(c, "mouse") {
				util = true
			}
			if strings.HasPrefix(c, "event") {
				ev = c
			}
		}
		if util && ev != "" {
			out = append(out, filepath.Join("/dev/input", ev))
		}
	}
	return out
}

func leerDispositivo(path string, f *os.File) {
	buf := make([]byte, tamEventoInput*64)
	off := tamEventoInput - 8
	for {
		n, err := f.Read(buf)
		if err != nil {
			entradaLinux.mu.Lock()
			if entradaLinux.abiertos[path] == f {
				delete(entradaLinux.abiertos, path)
			}
			entradaLinux.mu.Unlock()
			_ = f.Close()
			return
		}
		for i := 0; i+tamEventoInput <= n; i += tamEventoInput {
			e := buf[i+off : i+tamEventoInput]
			switch clasificarEvento(binary.LittleEndian.Uint16(e[0:2]), binary.LittleEndian.Uint16(e[2:4]), int32(binary.LittleEndian.Uint32(e[4:8]))) {
			case 1:
				entradaTeclas.Add(1)
			case 2:
				entradaMouse.Add(1)
			}
		}
	}
}

func abrirDispositivosNuevos() {
	for _, p := range dispositivosEntrada() {
		entradaLinux.mu.Lock()
		_, ya := entradaLinux.abiertos[p]
		entradaLinux.mu.Unlock()
		if ya {
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		entradaLinux.mu.Lock()
		entradaLinux.abiertos[p] = f
		entradaLinux.mu.Unlock()
		go leerDispositivo(p, f)
	}
}

// entradaIniciar empieza a contar (idempotente). Re-escanea cada minuto por
// teclados o mouse conectados después.
func (a *Agent) entradaIniciar() {
	entradaLinux.mu.Lock()
	if entradaLinux.activo {
		entradaLinux.mu.Unlock()
		return
	}
	entradaLinux.activo = true
	entradaLinux.abiertos = map[string]*os.File{}
	parar := make(chan struct{})
	entradaLinux.parar = parar
	entradaLinux.mu.Unlock()

	abrirDispositivosNuevos()
	entradaListo.Store(true)
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-parar:
				return
			case <-t.C:
				abrirDispositivosNuevos()
			}
		}
	}()
}

// entradaDetener deja de leer /dev/input (monitoreo apagado).
func (a *Agent) entradaDetener() {
	entradaLinux.mu.Lock()
	defer entradaLinux.mu.Unlock()
	if !entradaLinux.activo {
		return
	}
	entradaLinux.activo = false
	close(entradaLinux.parar)
	for p, f := range entradaLinux.abiertos {
		_ = f.Close()
		delete(entradaLinux.abiertos, p)
	}
	entradaListo.Store(false)
}
