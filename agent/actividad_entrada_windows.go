//go:build windows

package agent

// 049-E · Windows: un ayudante en la sesión del usuario (`-m actividad-entrada`)
// instala hooks de bajo nivel de teclado y mouse que SÓLO incrementan dos
// contadores; nunca miran qué tecla fue. Descarta la entrada inyectada por
// software (control remoto, "mouse jigglers"): cuenta sólo hardware. Imprime los
// acumulados cada 5 s por stdout; el servicio los lee del pipe y, si la sesión
// cambia o el ayudante muere, lo relanza. Al romperse el pipe el ayudante sale.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const (
	whKeyboardLL = 13
	whMouseLL    = 14

	wmKeyDown    = 0x0100
	wmSysKeyDown = 0x0104
	wmMouseMove  = 0x0200
	wmLButton    = 0x0201
	wmRButton    = 0x0204
	wmMButton    = 0x0207
	wmMouseWheel = 0x020A
	wmXButton    = 0x020B

	llkhfInjected = 0x10
	llmhfInjected = 0x01
)

var (
	user32Entrada       = syscall.NewLazyDLL("user32.dll")
	procSetHookEx       = user32Entrada.NewProc("SetWindowsHookExW")
	procCallNextHookEx  = user32Entrada.NewProc("CallNextHookEx")
	procUnhookEx        = user32Entrada.NewProc("UnhookWindowsHookEx")
	procGetMessageW     = user32Entrada.NewProc("GetMessageW")
	kernel32Entrada     = syscall.NewLazyDLL("kernel32.dll")
	procGetModuleHandle = kernel32Entrada.NewProc("GetModuleHandleW")
)

type kbdLLHook struct {
	VkCode, ScanCode, Flags, Time uint32
	Extra                         uintptr
}

type msLLHook struct {
	X, Y                   int32
	MouseData, Flags, Time uint32
	Extra                  uintptr
}

// eventoTeclado/eventoMouse deciden si el mensaje cuenta. Puras, para probarlas.
func eventoTeclado(wParam uintptr, flags uint32) bool {
	return (wParam == wmKeyDown || wParam == wmSysKeyDown) && flags&llkhfInjected == 0
}

func eventoMouse(wParam uintptr, flags uint32) bool {
	switch wParam {
	case wmMouseMove, wmLButton, wmRButton, wmMButton, wmMouseWheel, wmXButton:
		return flags&llmhfInjected == 0
	}
	return false
}

// ModoActividadEntrada es el cuerpo del ayudante `-m actividad-entrada`.
func ModoActividadEntrada() {
	var teclas, mouse atomic.Uint64

	go func() {
		w := bufio.NewWriter(os.Stdout)
		for {
			time.Sleep(5 * time.Second)
			if _, err := fmt.Fprintf(w, "%d %d\n", teclas.Load(), mouse.Load()); err != nil {
				os.Exit(0)
			}
			if w.Flush() != nil {
				os.Exit(0) // el servicio cerró el pipe
			}
		}
	}()

	runtime.LockOSThread()
	mod, _, _ := procGetModuleHandle.Call(0)
	cbTeclado := syscall.NewCallback(func(nCode int, wParam uintptr, lParam *kbdLLHook) uintptr {
		if nCode >= 0 && lParam != nil && eventoTeclado(wParam, lParam.Flags) {
			teclas.Add(1)
		}
		r, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, uintptr(unsafe.Pointer(lParam)))
		return r
	})
	cbMouse := syscall.NewCallback(func(nCode int, wParam uintptr, lParam *msLLHook) uintptr {
		if nCode >= 0 && lParam != nil && eventoMouse(wParam, lParam.Flags) {
			mouse.Add(1)
		}
		r, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, uintptr(unsafe.Pointer(lParam)))
		return r
	})
	hk, _, _ := procSetHookEx.Call(whKeyboardLL, cbTeclado, mod, 0)
	hm, _, _ := procSetHookEx.Call(whMouseLL, cbMouse, mod, 0)
	if hk == 0 && hm == 0 {
		fmt.Println("error sin-hooks")
		return
	}
	defer procUnhookEx.Call(hk)
	defer procUnhookEx.Call(hm)

	// Los hooks de bajo nivel exigen un bucle de mensajes en este hilo.
	var msg [48]byte
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
	}
}

var entradaWin struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

// entradaIniciar lanza y supervisa el ayudante (idempotente).
func (a *Agent) entradaIniciar() {
	entradaWin.mu.Lock()
	defer entradaWin.mu.Unlock()
	if entradaWin.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	entradaWin.cancel = cancel
	go a.supervisarAyudanteEntrada(ctx)
}

func (a *Agent) supervisarAyudanteEntrada(ctx context.Context) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	for ctx.Err() == nil {
		cmd, err := a.newUserSessionCmd(ctx, exe, "-m", "actividad-entrada")
		if err == nil {
			if out, perr := cmd.StdoutPipe(); perr == nil && cmd.Start() == nil {
				sc := bufio.NewScanner(out)
				for sc.Scan() {
					if t, m, ok := parsearLineaEntrada(sc.Text()); ok {
						// El ayudante relanzado parte de cero: el ciclo lo ve
						// como reinicio del contador (deltaContador).
						entradaTeclas.Store(t)
						entradaMouse.Store(m)
						entradaListo.Store(true)
					}
				}
				_ = cmd.Wait()
			}
		}
		entradaListo.Store(false)
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
	}
}

// entradaDetener mata el ayudante (monitoreo apagado).
func (a *Agent) entradaDetener() {
	entradaWin.mu.Lock()
	defer entradaWin.mu.Unlock()
	if entradaWin.cancel != nil {
		entradaWin.cancel()
		entradaWin.cancel = nil
	}
	entradaListo.Store(false)
}
