//go:build windows
// +build windows

package agent

// Feature 030 · Fase 2 · foto de webcam en Windows (T014) — TODAVÍA NO IMPLEMENTADA.
//
// Este archivo existe para que el agente de Windows COMPILE y para que el
// motivo que llega a la línea de tiempo sea cierto. Devolver `so_no_soportado`
// es exacto hoy: este agente, en este sistema operativo, no toma fotos.
//
// POR QUÉ NO ESTÁ, que es la parte que importa dejar escrita: el camino técnico
// está claro —Media Foundation por COM, con el mismo `go-ole` que ya usa el
// control de volumen de la 028— pero **no hay ninguna máquina donde medirlo**.
// El 2026-08-12 se enumeraron las tres Windows en línea de la flota de pruebas
// con `Get-PnpDevice -Class Camera,Image` y las tres contestaron NINGUNA: son
// VMs de datacenter y no tienen cámara. Escribir varios cientos de líneas de
// COM que nadie puede ejecutar produciría exactamente el modo de fallo que esta
// feature combate: código que parece funcionar, devuelve `exit 0` y entrega
// cuadros negros.
//
// Lo que hace falta para desbloquearlo: una máquina Windows con cámara —una VM
// con webcam USB por passthrough alcanza— o un equipo autorizado. Con eso, la
// implementación tiene dos partes ya identificadas:
//
//  1. `IMFSourceReader` sobre el dispositivo de captura, descartando los
//     `cuadrosDeCalentamiento` como en las otras dos plataformas.
//  2. El estado del permiso en
//     `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\webcam`.
//     El agente corre como SYSTEM y PODRÍA escribirlo, pero encender solo el
//     acceso a la cámara de la máquina de alguien es una decisión de gobernanza
//     y no un detalle de implementación: acá se leerá para REPORTAR
//     `permiso_denegado`, no para cambiarlo.
func (a *Agent) capturarWebcam(nombre string) WebcamShot {
	return WebcamShot{Reason: webcamUnsupported}
}
