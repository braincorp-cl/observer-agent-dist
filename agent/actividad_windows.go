//go:build windows
// +build windows

package agent

// Primitiva de actividad en Windows, nativa (Win32), sin binarios externos:
//   - Ventana/proceso activo: GetForegroundWindow + GetWindowThreadProcessId (w32),
//     nombre del proceso por QueryFullProcessImageName.
//   - Título: GetWindowTextW (user32).
//   - Inactividad: GetLastInputInfo + GetTickCount (user32/kernel32).
// Corre en la sesión del usuario (self-exec como el usuario interactivo, igual que
// la captura de pantalla de la 030).

import (
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/gonutz/w32/v2"
	"golang.org/x/sys/windows"

	rmm "github.com/braincorp-cl/observer-agent-dist/shared"
)

var (
	modUser32            = windows.NewLazySystemDLL("user32.dll")
	modKernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procGetLastInputInfo = modUser32.NewProc("GetLastInputInfo")
	procGetWindowTextW   = modUser32.NewProc("GetWindowTextW")
	procGetTickCount     = modKernel32.NewProc("GetTickCount")
)

type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

func idleSegundosWindows() int {
	var info lastInputInfo
	info.cbSize = uint32(unsafe.Sizeof(info))
	r, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return -1
	}
	tick, _, _ := procGetTickCount.Call()
	ms := uint32(tick) - info.dwTime
	return int(ms / 1000)
}

func tituloVentana(h w32.HWND) string {
	buf := make([]uint16, 512)
	r, _, _ := procGetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r == 0 {
		return ""
	}
	return limpiarTituloActividad(syscall.UTF16ToString(buf[:r]))
}

func nombreProceso(pid uint32) string {
	if pid == 0 {
		return ""
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return filepath.Base(syscall.UTF16ToString(buf[:n]))
}

func muestraActividadImpl(conTitulo bool) rmm.ActividadMuestra {
	m := rmm.ActividadMuestra{SO: "windows", Sesion: "windows", IdleSeg: idleSegundosWindows()}

	win := w32.GetForegroundWindow()
	if win == 0 {
		// Sin ventana en primer plano: escritorio seguro / bloqueo / sesión sin consola.
		m.Proceso = "(escritorio)"
		m.Bloqueado = true
		return m
	}
	_, pid := w32.GetWindowThreadProcessId(win)
	if n := nombreProceso(uint32(pid)); n != "" {
		m.Proceso = toLowerASCII(n)
	} else {
		m.Proceso = "(desconocido)"
	}
	m.PID = int(pid)
	if conTitulo {
		m.Titulo = tituloVentana(win)
	}
	// La pantalla de bloqueo corre LockApp.exe en primer plano.
	if m.Proceso == "lockapp.exe" {
		m.Bloqueado = true
	}
	return m
}

// toLowerASCII baja a minúsculas sin depender de locale (los nombres de .exe son ASCII).
func toLowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
