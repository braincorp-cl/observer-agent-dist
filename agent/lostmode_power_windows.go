package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
)

// Feature 038 · no-hibernación reversible en Windows (T007).
//
// El objetivo es que un portátil marcado como perdido NO se duerma al cerrar la
// tapa y siga reportando geo/capturas. El requisito duro es poder REVERTIR al
// recuperarlo, sin dejar pegada la configuración de energía.
//
// Por eso NO se muta el esquema del usuario (como sí hace DisableSleepHibernate
// en la instalación, que es de una sola vía). En su lugar se DUPLICA el esquema
// activo, se endurece la copia y se activa; al revertir se vuelve al original y
// se borra la copia. Así el revert es exacto y no hay que leer/parsear los
// valores previos de cada perilla.
//
// El par {original, copia} se guarda en disco para sobrevivir a un reinicio: un
// equipo robado que se reinicia tiene que poder revertirse igual al recuperarlo.

type lostPowerState struct {
	OriginalGUID string `json:"original_guid"`
	LostGUID     string `json:"lost_guid"`
}

// guidRe extrae un GUID de la salida de powercfg. Es invariante al idioma: el
// GUID tiene el mismo formato en cualquier locale, así que no dependemos del
// texto que powercfg imprime alrededor (que sí se traduce).
var guidRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

func (a *Agent) lostPowerStatePath() string {
	return filepath.Join(a.lostModeDataDir(), "lost_power.json")
}

// powercfg corre un subcomando de powercfg y devuelve su stdout.
func (a *Agent) powercfg(args string) string {
	out, err := CMDShell("cmd", []string{}, "powercfg "+args, 10, false, false, false, nil, nil, nil)
	if err != nil {
		a.Logger.Debugln("powercfg", args, "->", err.Error())
	}
	return out[0]
}

// aplicarNoHibernacion duplica el esquema activo, lo endurece y lo activa. Es
// idempotente: si ya hay un estado guardado (p. ej. tras un reinicio con el caso
// aún abierto), no hace nada para no encadenar copias.
func (a *Agent) aplicarNoHibernacion() {
	if _, err := os.Stat(a.lostPowerStatePath()); err == nil {
		a.Logger.Debugln("no-hibernación: ya aplicada, no se duplica de nuevo")
		return
	}

	original := guidRe.FindString(a.powercfg("/getactivescheme"))
	if original == "" {
		a.Logger.Errorln("no-hibernación: no se pudo leer el esquema de energía activo")
		return
	}

	lost := guidRe.FindString(a.powercfg("-duplicatescheme " + original))
	if lost == "" {
		a.Logger.Errorln("no-hibernación: no se pudo duplicar el esquema de energía")
		return
	}

	// Endurecer la COPIA: nada de dormir/hibernar/apagar disco o monitor por
	// inactividad, y cerrar la tapa no hace nada (LIDACTION 0). En ac y dc.
	ajustes := [][2]string{
		{"SUB_SLEEP", "STANDBYIDLE"},
		{"SUB_SLEEP", "HIBERNATEIDLE"},
		{"SUB_DISK", "DISKIDLE"},
		{"SUB_VIDEO", "VIDEOIDLE"},
		{"SUB_BUTTONS", "LIDACTION"},
	}
	for _, s := range ajustes {
		a.powercfg("/setacvalueindex " + lost + " " + s[0] + " " + s[1] + " 0")
		a.powercfg("/setdcvalueindex " + lost + " " + s[0] + " " + s[1] + " 0")
	}

	a.powercfg("/setactive " + lost)

	data, _ := json.Marshal(lostPowerState{OriginalGUID: original, LostGUID: lost})
	if err := os.WriteFile(a.lostPowerStatePath(), data, 0600); err != nil {
		a.Logger.Errorln("no-hibernación: no se pudo guardar el estado de energía:", err.Error())
	}
	a.Logger.Infoln("no-hibernación aplicada (esquema", lost, "sobre", original, ")")
}

// revertirNoHibernacion vuelve al esquema original y borra la copia endurecida.
// Ausencia de estado guardado = nada que revertir (nunca se aplicó, o ya se
// revirtió): es idempotente y silencioso.
func (a *Agent) revertirNoHibernacion() {
	path := a.lostPowerStatePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var st lostPowerState
	if err := json.Unmarshal(data, &st); err != nil {
		a.Logger.Debugln("no-hibernación: estado de energía ilegible, se descarta:", err.Error())
		_ = os.Remove(path)
		return
	}

	if st.OriginalGUID != "" {
		a.powercfg("/setactive " + st.OriginalGUID)
	}
	if st.LostGUID != "" {
		a.powercfg("-delete " + st.LostGUID)
	}
	_ = os.Remove(path)
	a.Logger.Infoln("no-hibernación revertida (esquema original", st.OriginalGUID, "restaurado)")
}
