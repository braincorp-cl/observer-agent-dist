//go:build windows

package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Feature 041 · baseline keep-awake en Windows (T027).
//
// Aplica la MISMA política de energía que la instalación (DisableSleepHibernate,
// agent_windows.go), pero de forma REVERSIBLE: se capturan los valores previos de
// los índices que esa función pisa (lid/standby/hibernate/disk/monitor en AC y DC,
// + HiberbootEnabled) para poder restaurarlos al apagar el toggle o desinstalar.
//
// A diferencia de la no-hibernación del modo perdido (lostmode_power_windows.go),
// que DUPLICA el esquema para no tocar el del usuario, el baseline reusa
// DisableSleepHibernate —que muta el esquema activo en sitio— porque es la misma
// política que ya se aplica en la instalación y conviene un solo camino. El precio
// es tener que capturar/restaurar valor por valor, que es lo que hace este backend.
//
// ⚠️ 🟡 El parseo de índices de powercfg /query se confirma en terreno: se apoya
// en el patrón "0x........" (invariante al idioma) y NO en las etiquetas, que se
// traducen. Ver la lacuna de implementación anotada en actions.md.

func init() { baselineBackendImpl = windowsBaseline{} }

type windowsBaseline struct{}

// winBaselineSettings son los (subgrupo, setting) que DisableSleepHibernate pisa y
// que por tanto hay que capturar para revertir.
var winBaselineSettings = [][2]string{
	{"SUB_BUTTONS", "LIDACTION"},
	{"SUB_SLEEP", "STANDBYIDLE"},
	{"SUB_SLEEP", "HIBERNATEIDLE"},
	{"SUB_DISK", "DISKIDLE"},
	{"SUB_VIDEO", "VIDEOIDLE"},
}

// winPowerIdxRe extrae los índices AC/DC del volcado de powercfg /query. El hex
// "0x........" es estable entre locales; las etiquetas alrededor se traducen.
var winPowerIdxRe = regexp.MustCompile(`0x[0-9a-fA-F]{8}`)

const winPowerRegPath = `SYSTEM\CurrentControlSet\Control\Session Manager\Power`

// windowsBaselineBackup son los valores de energía previos. La clave del mapa es
// "SUBGRUPO SETTING"; el valor es [AC, DC].
type windowsBaselineBackup struct {
	Settings       map[string][2]uint32 `json:"settings"`
	Hiberboot      uint32               `json:"hiberboot"`
	HiberbootKnown bool                 `json:"hiberboot_known"`
}

func runPowercfg(args string) string {
	out, err := CMDShell("cmd", []string{}, "powercfg "+args, 10, false, false, false, nil, nil, nil)
	if err != nil {
		return out[0]
	}
	return out[0]
}

func (windowsBaseline) capturar() ([]byte, error) {
	b := windowsBaselineBackup{Settings: map[string][2]uint32{}}
	for _, s := range winBaselineSettings {
		out := runPowercfg(fmt.Sprintf("/query SCHEME_CURRENT %s %s", s[0], s[1]))
		idx := winPowerIdxRe.FindAllString(out, -1)
		// Se esperan al menos 2 índices (AC y DC). Si el query no los devolvió
		// (build raro, subgrupo ausente), se guarda 0 = "dormir": revertir a 0 no
		// re-duerme más de lo que ya es la política, así que ante duda no empeora.
		var ac, dc uint32
		if len(idx) >= 2 {
			ac = parseHexIdx(idx[0])
			dc = parseHexIdx(idx[1])
		}
		b.Settings[s[0]+" "+s[1]] = [2]uint32{ac, dc}
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, winPowerRegPath, registry.QUERY_VALUE); err == nil {
		if v, _, err := k.GetIntegerValue("HiberbootEnabled"); err == nil {
			b.Hiberboot = uint32(v)
			b.HiberbootKnown = true
		}
		k.Close()
	}
	return json.Marshal(b)
}

func (windowsBaseline) aplicar() error {
	DisableSleepHibernate()
	return nil
}

func (windowsBaseline) restaurar(backup []byte) error {
	if len(backup) == 0 {
		return nil
	}
	var b windowsBaselineBackup
	if err := json.Unmarshal(backup, &b); err != nil {
		return err
	}
	for key, v := range b.Settings {
		parts := strings.SplitN(key, " ", 2)
		if len(parts) != 2 {
			continue
		}
		runPowercfg(fmt.Sprintf("/setacvalueindex SCHEME_CURRENT %s %s %d", parts[0], parts[1], v[0]))
		runPowercfg(fmt.Sprintf("/setdcvalueindex SCHEME_CURRENT %s %s %d", parts[0], parts[1], v[1]))
	}
	if b.HiberbootKnown {
		if k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, winPowerRegPath, registry.SET_VALUE); err == nil {
			_ = k.SetDWordValue("HiberbootEnabled", b.Hiberboot)
			k.Close()
		}
	}
	// Re-activar el esquema para que los índices restaurados tomen efecto.
	runPowercfg("-S SCHEME_CURRENT")
	return nil
}

func parseHexIdx(s string) uint32 {
	v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(s), "0x"), 16, 32)
	if err != nil {
		return 0
	}
	return uint32(v)
}
