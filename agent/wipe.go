package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
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
// ⚠️ El borrado seguro real por-SO y su verificación (T012/T013/T014) NO están en
// este incremento: `borrarRutasSeguro` es un stub por plataforma que reporta el
// fallo honestamente (nunca "ok falso"). En prod el flag destructivo sigue en False,
// así que una orden real ni siquiera llega hasta acá.

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
