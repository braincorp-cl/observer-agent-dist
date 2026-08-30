package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// Feature 043 · wipe (Bloque A · A2) — borrado seguro SELECTIVO por rutas.
//
// Segundo eslabón del orden invariante `fileretrieval → wipe → fullwipe →
// factoryreset`: el primero que DESTRUYE datos. Reusa la vía servidor→agente de
// fileretrieval (042). El `dry_run` (RF-G05) enumera lo que borraría y reporta el
// plan SIN tocar disco — es el simulacro en seco obligatorio antes de habilitar el
// despacho destructivo en el servidor (`ERASE_DESTRUCTIVE_DISPATCH_ENABLED`).
//
// La verificación por relectura (RN-08) es obligatoria: el agente solo reporta
// `verified=1` si confirma que las rutas quedaron irrecuperables; si queda residuo,
// `verified=0` y la orden queda `incomplete` en el servidor (sin certificado).
//
// El borrado seguro real por-SO (T012 Windows, T013 Linux) y su verificación por
// relectura (T014) viven abajo: `borrarRutasSeguro` (por plataforma) delega en el
// núcleo portable `ejecutarBorradoSeguro`. macOS queda fuera de alcance (APFS/CoW,
// T015). En prod el flag destructivo sigue en False hasta el simulacro en seco
// verde (T023), así que una orden real no llega hasta acá salvo en staging.

// EjecutarWipe procesa una orden de wipe (corre en su propia goroutine).
func (a *Agent) EjecutarWipe(data map[string]string) {
	orderID := data["order_id"]
	if orderID == "" {
		a.Logger.Debugln("wipe: orden sin order_id, se ignora")
		return
	}

	dryRun := data["dry_run"] == "1"

	var rutas []string
	if err := json.Unmarshal([]byte(data["paths"]), &rutas); err != nil {
		a.Logger.Debugln("wipe: rutas ilegibles:", err)
		a.reportarWipeError(orderID, "rutas ilegibles")
		return
	}

	// El tope de nº de rutas lo fija el servidor (RF-07); el agente lo respeta al
	// enumerar para no expandir de más un directorio enorme.
	pathLimit := 0
	fmt.Sscanf(data["path_limit"], "%d", &pathLimit)

	archivos := a.expandirRutasFileRetrieval(rutas, pathLimit)

	if dryRun {
		// Simulacro en seco (RF-G05): sólo el plan, cero borrado.
		plan := fmt.Sprintf("%d archivo(s) que se borrarían", len(archivos))
		a.Logger.Infoln("wipe (dry-run):", plan)
		a.reportarWipePlan(orderID, plan)
		return
	}

	// Borrado real + verificación por relectura, por-SO (T012/T013/T014). En este
	// incremento es un stub que reporta el fallo; nunca borra sin verificar.
	result, verified, metodo, err := borrarRutasSeguro(archivos)
	if err != nil {
		a.Logger.Errorln("wipe: borrado seguro no disponible:", err)
		a.reportarWipeError(orderID, err.Error())
		return
	}
	a.reportarWipeResultado(orderID, result, verified, metodo)
}

// --- Reporte al servidor (endpoint apiv3, token de agente) --------------------

func (a *Agent) wipeReportURL(orderID string) string {
	return fmt.Sprintf("/api/v3/%s/wipe/%s/report/", a.AgentID, orderID)
}

func (a *Agent) postWipeReport(orderID string, datos map[string]string) {
	r, err := a.rClient.R().SetFormData(datos).Post(a.wipeReportURL(orderID))
	if err != nil {
		a.Logger.Debugln("wipe: no se pudo reportar:", err)
		return
	}
	if r.StatusCode() == http.StatusConflict {
		a.Logger.Infoln("wipe: el servidor informa orden terminada, se descarta el reporte")
	}
}

// reportarWipePlan cierra una orden dry-run con el plan (sin borrado).
func (a *Agent) reportarWipePlan(orderID, plan string) {
	a.postWipeReport(orderID, map[string]string{"plan": plan})
}

// reportarWipeResultado informa el resultado real + el veredicto de la verificación
// por relectura (RN-08) + la técnica aplicada.
func (a *Agent) reportarWipeResultado(orderID string, result map[string]string, verified bool, metodo string) {
	datos := map[string]string{
		"method_applied": metodo,
		"verified":       "0",
	}
	if verified {
		datos["verified"] = "1"
	}
	if b, err := json.Marshal(result); err == nil {
		datos["result"] = string(b)
	}
	a.postWipeReport(orderID, datos)
}

// reportarWipeError avisa al servidor que la orden no se pudo ejecutar (→ failed).
func (a *Agent) reportarWipeError(orderID, motivo string) {
	a.postWipeReport(orderID, map[string]string{"error": motivo})
}

// --- Núcleo portable de borrado seguro (T014) --------------------------------
//
// La verificación por relectura (RN-08) es el corazón de la garantía: tras
// sobrescribir y eliminar cada ruta, se reintenta acceder a ella; si sigue
// presente, es residuo y la orden NO se verifica. `borrarRutasSeguro` (por-SO,
// T012/T013) delega en `ejecutarBorradoSeguro`, que arma el reporte por-ruta y
// sólo declara `verified=true` si TODAS las rutas quedaron borradas y verificadas.
// Cero GPL: sólo la stdlib de Go (os/crypto), sin `shred(1)`/`cipher` ni externos.

const wipeChunk = 64 * 1024

// escribirAleatorio sobrescribe `size` bytes del archivo abierto con datos
// aleatorios (crypto/rand), en bloques para no cargar el archivo entero en RAM.
func escribirAleatorio(f *os.File, size int64) error {
	buf := make([]byte, wipeChunk)
	for restante := size; restante > 0; {
		n := int64(len(buf))
		if restante < n {
			n = restante
		}
		if _, err := rand.Read(buf[:n]); err != nil {
			return err
		}
		if _, err := f.Write(buf[:n]); err != nil {
			return err
		}
		restante -= n
	}
	return nil
}

// sobrescribirArchivo pisa el contenido con aleatorio y lo fuerza a disco (fsync)
// antes de que el llamador rompa metadatos y elimine.
func sobrescribirArchivo(path string, size int64) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if size > 0 {
		if err := escribirAleatorio(f, size); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// nombreAleatorio genera un nombre oculto e impredecible para romper metadatos.
func nombreAleatorio() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return ".wipe.tmp"
	}
	return "." + hex.EncodeToString(b) + ".wipe"
}

// romperMetadatos renombra el archivo a un nombre aleatorio en el mismo
// directorio y lo trunca a 0, para que la entrada de directorio no delate el
// nombre ni el tamaño originales. Devuelve la ruta final a eliminar (o la
// original si no se pudo renombrar, para intentar borrarla igual).
func romperMetadatos(path string) string {
	nuevo := filepath.Join(filepath.Dir(path), nombreAleatorio())
	if err := os.Rename(path, nuevo); err != nil {
		return path
	}
	_ = os.Truncate(nuevo, 0)
	return nuevo
}

// borrarUnaRuta aplica el borrado seguro a una sola ruta y verifica por relectura
// (RN-08) que dejó de existir. No sigue symlinks (elimina el enlace, no el destino).
func borrarUnaRuta(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		// Symlink: se elimina el enlace sin tocar el destino.
		if err := os.Remove(path); err != nil {
			return err
		}
	case info.IsDir():
		return fmt.Errorf("es un directorio, no un archivo regular")
	default:
		if info.Mode().IsRegular() {
			if err := sobrescribirArchivo(path, info.Size()); err != nil {
				return err
			}
		}
		final := romperMetadatos(path)
		if err := os.Remove(final); err != nil {
			return err
		}
	}
	// Verificación por relectura: el nombre original no debe seguir presente.
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("residuo: la ruta sigue presente tras el borrado")
	}
	return nil
}

// ejecutarBorradoSeguro borra y verifica cada ruta, arma el reporte por-ruta y
// devuelve verified=true SÓLO si todas quedaron borradas y verificadas. Nunca
// reporta un "ok falso": cualquier residuo o error deja verified=false. El error
// de retorno se reserva para "borrado no disponible" (los stubs por-SO), no para
// fallos por-ruta, que van en el mapa de resultado.
func ejecutarBorradoSeguro(archivos []string, metodo string) (map[string]string, bool, string, error) {
	result := make(map[string]string, len(archivos)+1)
	if len(archivos) == 0 {
		// Sin archivos resueltos no se emite certificado (evita "ok falso").
		result["_"] = "sin archivos que borrar"
		return result, false, metodo, nil
	}
	verified := true
	for _, path := range archivos {
		if err := borrarUnaRuta(path); err != nil {
			result[path] = "error: " + err.Error()
			verified = false
			continue
		}
		result[path] = "borrado+verificado"
	}
	return result, verified, metodo, nil
}
