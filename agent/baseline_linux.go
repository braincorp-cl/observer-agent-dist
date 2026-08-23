//go:build linux

package agent

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Feature 041 · baseline keep-awake en Linux (T028).
//
// Se instala un drop-in PERSISTENTE de logind que ignora el cierre de tapa (con y
// sin corriente, y en dock), y se enmascaran los targets de sleep para inhibir la
// suspensión por systemd. A diferencia de la no-suspensión del modo perdido
// (lostmode_power_linux.go), que sostiene un proceso systemd-inhibit efímero, el
// baseline es persistente por diseño: sobrevive reinicios y sólo se revierte al
// apagar el toggle o desinstalar.
//
// El revert NO restaura valores capturados: borra el drop-in y desenmascara los
// targets, y el sistema vuelve a su configuración base. Por eso capturar() no
// guarda nada.
//
// ⚠️ 🟡 El enmascarado de sleep.target es agresivo (bloquea también un `systemctl
// suspend` manual mientras el baseline esté puesto). Es la vía persistente de
// inhibir la suspensión por inactividad; se confirma en terreno. Ver la lacuna en
// actions.md.

func init() { baselineBackendImpl = linuxBaseline{} }

type linuxBaseline struct{}

// baselineLogindDropin y su override para pruebas (el path real no es escribible
// en CI y tocarlo sería peligroso).
var baselineLogindDropin = "/etc/systemd/logind.conf.d/99-observer-keepawake.conf"

const baselineLogindContent = `# Observer RMM · baseline keep-awake (feature 041)
# Generado automáticamente por el agente. Se elimina al apagar el toggle
# keep_awake_baseline o al desinstalar.
[Login]
HandleLidSwitch=ignore
HandleLidSwitchExternalPower=ignore
HandleLidSwitchDocked=ignore
`

var baselineSleepTargets = []string{
	"sleep.target", "suspend.target", "hibernate.target", "hybrid-sleep.target",
}

func (linuxBaseline) capturar() ([]byte, error) {
	// El revert es "borrar el drop-in + desenmascarar": no hay valores previos que
	// guardar.
	return nil, nil
}

func (linuxBaseline) aplicar() error {
	if err := os.MkdirAll(filepath.Dir(baselineLogindDropin), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(baselineLogindDropin, []byte(baselineLogindContent), 0644); err != nil {
		return err
	}
	// Recargar logind para que tome el drop-in, e inhibir la suspensión por
	// inactividad enmascarando los targets. Best-effort: en un host sin estos
	// targets (contenedor) systemctl falla y se ignora.
	_ = exec.Command("systemctl", "restart", "systemd-logind").Run()
	_ = exec.Command("systemctl", append([]string{"mask"}, baselineSleepTargets...)...).Run()
	return nil
}

func (linuxBaseline) restaurar(_ []byte) error {
	_ = exec.Command("systemctl", append([]string{"unmask"}, baselineSleepTargets...)...).Run()
	if err := os.Remove(baselineLogindDropin); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = exec.Command("systemctl", "restart", "systemd-logind").Run()
	return nil
}
