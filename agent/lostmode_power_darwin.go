//go:build darwin

package agent

import (
	"os/exec"
	"syscall"
)

// Feature 038 · no-suspensión reactiva en macOS (T010, Fase A2).
//
// Análogo directo del inhibidor de logind de Linux (T011): en macOS el mecanismo
// correcto para una no-suspensión EFÍMERA y reversible es sostener vivo un proceso
// `caffeinate` mientras dure el caso. Mientras viva, el sistema no se suspende por
// inactividad; al matarlo, la aserción se libera sola. NO se toca ninguna
// configuración persistente —a diferencia del BASELINE keep-awake (T029,
// `baseline_darwin.go`), que sí muta `pmset`—, así que las dos capas componen sin
// pisarse: el reactivo es un proceso sostenido, el baseline es config del sistema.
// El esqueleto común (estado, idempotencia, revert) vive en `lostmode_power_unix.go`.
//
// Flags de caffeinate:
//   - -i  impedir la suspensión del sistema por inactividad (aplica también con batería)
//   - -s  impedir la suspensión del sistema (sólo con corriente / AC)
//   - -d  impedir que la pantalla duerma
//   - -m  impedir que el disco quede inactivo
//   - -u  declarar actividad del usuario
//
// Sin `-t` (timeout) ni comando hijo, caffeinate corre hasta que lo matemos: es el
// "held" equivalente al `sleep infinity` de Linux.
//
// ⚠️ 🔴 LACUNA (confirmar en terreno, T012-macOS): a diferencia del
// `handle-lid-switch` de systemd en Linux, `caffeinate` NO impide la suspensión
// por CIERRE DE TAPA (clamshell) con batería —eso en macOS sólo lo frena
// `pmset disablesleep 1`, que es competencia del baseline persistente, no de esta
// capa reactiva efímera—. En un caso perdido con baseline activo la tapa queda
// cubierta; sin baseline, el clamshell-en-batería es el borde irreducible. Además
// falta el E2E en hardware real (T012-macOS, sin Mac hoy).

// defaultStartLostInhibitor lanza `caffeinate` y devuelve su PID. El proceso
// sostiene la aserción mientras viva.
func defaultStartLostInhibitor(a *Agent) (int, error) {
	bin, err := exec.LookPath("caffeinate")
	if err != nil {
		return 0, err
	}

	cmd := exec.Command(bin, "-dimsu")
	// Grupo de proceso propio: killProcessGroup mata el grupo (kill negativo) sin
	// dejar nada huérfano si caffeinate muriera de forma inesperada.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return 0, err
	}
	// Se cosecha en segundo plano: sin el Wait, el proceso quedaría zombi al
	// matarlo. No bloquea porque corre en su propia goroutine.
	go func() { _ = cmd.Wait() }()

	return cmd.Process.Pid, nil
}
