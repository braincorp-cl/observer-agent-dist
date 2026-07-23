//go:build darwin

package agent

// Feature 023 · provider macOS.
//
// CoreLocation (CLLocationManager) requiere: (a) entitlement de ubicación,
// (b) una sesión interactiva con run-loop para mostrar el prompt de permiso.
// El agente corre como LaunchDaemon headless, contexto en el que CoreLocation
// normalmente NO puede solicitar ni obtener ubicación. Por eso el proveedor
// macOS reporta "no disponible" y GetLocation degrada a geolocalización por IP
// (RF-07), que sí funciona en un servidor macOS sin sesión.
//
// La integración cgo/Objective-C con CoreLocation para equipos macOS con sesión
// interactiva queda DIFERIDA a propósito: escribir cgo que no se pueda compilar
// ni validar en este entorno introduciría un binario roto. Ver investigation.md
// §1 y el watch item de regresión correspondiente. Cuando se retome, este archivo
// pasará a `//go:build darwin && cgo` con un stub `darwin && !cgo` que conserve
// esta degradación.
func osLocation(a *Agent) (Location, error) {
	a.Logger.Debugln("osLocation(darwin): CoreLocation no disponible en daemon headless; se degradará a IP")
	return Location{Source: SourceUnavailable}, errNativeUnavailable
}
