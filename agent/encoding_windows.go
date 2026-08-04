package agent

import (
	"golang.org/x/sys/windows"
)

// GetOEMCP no esta expuesta por golang.org/x/sys/windows, asi que se resuelve a mano
// sobre el modkernel32 que ya declara syscall_windows.go.
var procGetOEMCP = modkernel32.NewProc("GetOEMCP")

const (
	codePageUTF8  = uint32(65001)
	codePageLatin = uint32(1252)
)

func getOEMCP() uint32 {
	r, _, _ := procGetOEMCP.Call()
	return uint32(r)
}

// codePageDeSalida decide desde que pagina de codigos hay que decodificar la salida
// de un proceso hijo.
//
// El orden no es arbitrario. El hijo hereda la consola del agente (o su ausencia),
// asi que la pagina de salida de esa consola es el mejor dato disponible. Pero el
// agente corre como servicio y normalmente NO tiene consola: en ese caso .NET
// inicializa Console.OutputEncoding desde la pagina OEM del sistema, que es la que
// PowerShell 5.1 termina usando al escribir en una tuberia redirigida. La pagina
// ANSI queda de ultimo recurso porque describe la entrada, no la salida de consola.
func codePageDeSalida() uint32 {
	if cp, err := windows.GetConsoleOutputCP(); err == nil && cp != 0 {
		return cp
	}
	if cp := getOEMCP(); cp != 0 {
		return cp
	}
	if cp := windows.GetACP(); cp != 0 {
		return cp
	}
	return codePageLatin
}

func decodeSystemCodePage(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", true
	}

	cp := codePageDeSalida()
	if cp == codePageUTF8 {
		// El sistema ya dice UTF-8 y aun asi los bytes no lo eran: estan rotos de
		// origen y ninguna decodificacion los arregla.
		return "", false
	}

	// dwFlags queda en 0 a proposito: con MB_ERR_INVALID_CHARS un solo byte suelto
	// invalido haria fallar la conversion y se perderia toda la salida del script.
	n, err := windows.MultiByteToWideChar(cp, 0, &b[0], int32(len(b)), nil, 0)
	if err != nil || n <= 0 {
		return "", false
	}

	buf := make([]uint16, n)
	n, err = windows.MultiByteToWideChar(cp, 0, &b[0], int32(len(b)), &buf[0], n)
	if err != nil || n <= 0 {
		return "", false
	}

	return windows.UTF16ToString(buf[:n]), true
}
