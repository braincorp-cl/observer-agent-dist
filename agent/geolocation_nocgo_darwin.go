//go:build darwin && !cgo

package agent

// Feature 025 · stub macOS sin cgo.
//
// El proveedor real (CoreLocation) necesita cgo para hablar con Objective-C. Si
// el binario se compila con CGO_ENABLED=0 —por ejemplo un cross-compile desde
// Linux— se conserva exactamente la degradación previa a la feature 025:
// "no disponible" ⇒ GetLocation cae a geolocalización por IP (RF-07). Nunca se
// rompe el check-in por esto.
func osLocation(a *Agent) (Location, error) {
	a.Logger.Debugln("osLocation(darwin): binario sin cgo, CoreLocation no disponible; se degradará a IP")
	return Location{Source: SourceUnavailable}, errNativeUnavailable
}
