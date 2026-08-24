//go:build linux

package agent

import (
	"os/exec"
	"syscall"
)

// Feature 038 · no-suspensión reactiva en Linux (T011, Fase A2).
//
// Un equipo marcado como perdido no debe suspenderse ni hibernar —ni al cerrar la
// tapa ni por inactividad— para seguir reportando geo/capturas, y hay que poder
// REVERTIR exacto al recuperarlo. En Linux con systemd el mecanismo correcto es
// un inhibidor de logind: se mantiene vivo un proceso `systemd-inhibit
// --what=sleep:idle:handle-lid-switch --mode=block sleep infinity` mientras dure
// el caso. Mientras ese proceso viva, logind no suspende; al matarlo, el inhibidor
// se libera solo.
//
// El esqueleto común (estado persistido, idempotencia, revert, limpieza de
// huérfano) vive en `lostmode_power_unix.go`; acá sólo el lanzamiento del
// inhibidor real.

const (
	lostInhibitWho = "Observer RMM"
	lostInhibitWhy = "equipo en modo perdido"
)

// defaultStartLostInhibitor lanza el inhibidor real y devuelve su PID. El proceso
// sostiene el lock mientras viva; `sleep infinity` es el "held" que no hace nada
// salvo mantenerlo abierto hasta que lo matemos.
func defaultStartLostInhibitor(a *Agent) (int, error) {
	bin, err := exec.LookPath("systemd-inhibit")
	if err != nil {
		return 0, err
	}

	cmd := exec.Command(bin,
		"--what=sleep:idle:handle-lid-switch",
		"--who="+lostInhibitWho,
		"--why="+lostInhibitWhy,
		"--mode=block",
		"sleep", "infinity",
	)
	// Grupo de proceso propio: al matar el grupo (kill negativo) se lleva también
	// al `sleep` hijo, sin dejarlo huérfano cuando systemd-inhibit muera.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return 0, err
	}
	// Se cosecha en segundo plano: sin el Wait, el proceso quedaría zombi al
	// matarlo. No bloquea porque corre en su propia goroutine.
	go func() { _ = cmd.Wait() }()

	return cmd.Process.Pid, nil
}
