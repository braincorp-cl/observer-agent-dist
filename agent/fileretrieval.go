package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

// Feature 042 · fileretrieval (Bloque A · B1) — recuperar archivos ANTES de borrar.
//
// Es la primera acción del orden invariante de irreversibilidad
// (`fileretrieval → wipe → fullwipe → factoryreset`): "recuperar antes de borrar".
// NO es destructiva: sólo LEE rutas del equipo y las sube, cifradas en reposo,
// por el mismo canal de evidencia del modo perdido. El `dry_run` (RF-G05) es el
// simulacro en seco: enumera lo que recuperaría y reporta el plan SIN subir nada,
// para validar la vía servidor→agente antes de habilitar las acciones destructivas.
//
// La idempotencia la garantiza el `order_id`: el servidor no re-ejecuta una orden
// ya terminada, y la unicidad `(orden, ruta)` del lado servidor descarta duplicados
// si el agente reintenta al reconectar.

// EjecutarFileRetrieval procesa una orden de recuperación (corre en su goroutine).
func (a *Agent) EjecutarFileRetrieval(data map[string]string) {
	orderID := data["order_id"]
	if orderID == "" {
		a.Logger.Debugln("fileretrieval: orden sin order_id, se ignora")
		return
	}

	dryRun := data["dry_run"] == "1"

	var rutas []string
	if err := json.Unmarshal([]byte(data["paths"]), &rutas); err != nil {
		a.Logger.Debugln("fileretrieval: rutas ilegibles:", err)
		a.reportarFalloFileRetrieval(orderID, "rutas ilegibles")
		return
	}

	// El tope de nº de archivos lo fija el servidor (RF-08); el agente lo respeta
	// para no empaquetar de más aunque una ruta sea un directorio enorme.
	fileLimit, _ := strconv.Atoi(data["file_limit"])

	archivos := a.expandirRutasFileRetrieval(rutas, fileLimit)

	if dryRun {
		// Simulacro en seco (RF-G05): sólo el plan, cero subida.
		plan := fmt.Sprintf("%d archivo(s) recuperable(s)", len(archivos))
		a.Logger.Infoln("fileretrieval (dry-run):", plan)
		a.cerrarFileRetrieval(orderID, true, plan)
		return
	}

	for _, ruta := range archivos {
		if !a.subirArchivoRecuperado(orderID, ruta) {
			// 409 del servidor (orden cancelada/expirada) o tope excedido: se corta.
			return
		}
	}
	a.cerrarFileRetrieval(orderID, false, "")
}

// expandirRutasFileRetrieval convierte la lista de rutas (archivos y directorios)
// en una lista plana de archivos existentes, recorriendo los directorios de forma
// recursiva y respetando el tope de nº de archivos.
func (a *Agent) expandirRutasFileRetrieval(rutas []string, fileLimit int) []string {
	var archivos []string

	agregar := func(p string) bool {
		if fileLimit > 0 && len(archivos) >= fileLimit {
			return false
		}
		archivos = append(archivos, p)
		return true
	}

	for _, ruta := range rutas {
		info, err := os.Stat(ruta)
		if err != nil {
			a.Logger.Debugln("fileretrieval: no se puede acceder a la ruta:", ruta, err)
			continue
		}
		if !info.IsDir() {
			if !agregar(ruta) {
				break
			}
			continue
		}
		// Directorio: recorrido recursivo.
		lleno := false
		_ = filepath.Walk(ruta, func(p string, fi os.FileInfo, werr error) error {
			if werr != nil || fi == nil || fi.IsDir() {
				return nil
			}
			if !agregar(p) {
				lleno = true
				return filepath.SkipDir
			}
			return nil
		})
		if lleno {
			break
		}
	}
	return archivos
}

// subirArchivoRecuperado sube un archivo al servidor por el canal cifrado.
// Devuelve false si el servidor pide detenerse (orden terminal) o hay un error
// que no vale la pena seguir intentando en esta corrida.
func (a *Agent) subirArchivoRecuperado(orderID, ruta string) bool {
	url := fmt.Sprintf("/api/v3/%s/fileretrieval/%s/upload/", a.AgentID, orderID)

	r, err := a.rClient.R().
		SetFormData(map[string]string{"source_path": ruta}).
		SetFile("file", ruta).
		Post(url)
	if err != nil {
		a.Logger.Debugln("fileretrieval: no se pudo subir:", ruta, err)
		return false
	}

	// 409 = la orden ya no está viva (cancelada o expirada mientras el equipo
	// estaba sin red). Como en el modo perdido, es la señal más rápida para dejar
	// de trabajar en una orden que el servidor ya cerró.
	if r.StatusCode() == http.StatusConflict {
		a.Logger.Infoln("fileretrieval: el servidor informa orden terminada, se detiene")
		return false
	}
	// 413 = tope de tamaño por orden excedido; el servidor ya marcó la orden
	// fallida, no tiene sentido seguir subiendo.
	if r.StatusCode() == http.StatusRequestEntityTooLarge {
		a.Logger.Infoln("fileretrieval: tope de tamaño por orden excedido, se detiene")
		return false
	}
	if r.StatusCode() >= 300 {
		a.Logger.Debugln("fileretrieval: el servidor rechazó el archivo:", r.StatusCode(), string(r.Body()))
		// Un archivo puede fallar sin condenar la orden entera (ej. bloqueado):
		// se sigue con los demás.
		return true
	}
	return true
}

// cerrarFileRetrieval marca la orden como completada en el servidor. En dry-run
// adjunta el plan; en la corrida real informa que terminó la subida.
func (a *Agent) cerrarFileRetrieval(orderID string, dryRun bool, plan string) {
	url := fmt.Sprintf("/api/v3/%s/fileretrieval/%s/upload/", a.AgentID, orderID)
	datos := map[string]string{"done": "1"}
	if dryRun {
		datos["plan"] = plan
	}
	if _, err := a.rClient.R().SetFormData(datos).Post(url); err != nil {
		a.Logger.Debugln("fileretrieval: no se pudo cerrar la orden:", err)
	}
}

// reportarFalloFileRetrieval avisa al servidor que la orden no se pudo ejecutar.
func (a *Agent) reportarFalloFileRetrieval(orderID, motivo string) {
	url := fmt.Sprintf("/api/v3/%s/fileretrieval/%s/upload/", a.AgentID, orderID)
	if _, err := a.rClient.R().SetFormData(map[string]string{"error": motivo}).Post(url); err != nil {
		a.Logger.Debugln("fileretrieval: no se pudo reportar el fallo:", err)
	}
}
