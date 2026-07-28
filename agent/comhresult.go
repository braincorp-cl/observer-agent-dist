package agent

// Feature 028 · Fase 2 — cómo se lee un HRESULT de COM.
//
// ⚠️ Este archivo NO lleva etiqueta de plataforma A PROPÓSITO, aunque hoy sólo
// lo use el código de Windows: la CI corre `go test` en Linux y macOS, así que
// una prueba `//go:build windows` se compilaría y **no se ejecutaría nunca**.
// La regla vive acá para que las pruebas la ejerzan de verdad — mismo criterio
// que la elección de sesión en `usersession.go`.
//
// 📌 El error que originó este archivo, medido el 2026-07-28 en
// `LAPTOP-DSHQMP1O` (Win 11 Enterprise 26200) con la sonda de T008 corriendo
// como SYSTEM en la sesión 0: `IAudioEndpointVolume::SetMute` devuelve
// **S_FALSE (0x00000001)** —no S_OK— cuando el estado de silencio YA era el
// pedido. Como `setVolume` trataba "distinto de cero" como fallo, el camino
// más común de todos —un equipo que no está silenciado, al que la alarma le
// manda `SetMute(false)`— se convertía en error DESPUÉS de que el nivel ya
// había subido al 100 %:
//
//	raiseVolumeForAlarm -> setVolume(max)  // el nivel sube; SetMute da S_FALSE
//	                    -> error           // devuelve volumeState{} (Known=false)
//	restoreSavedVolume  -> !Known          // no restaura NADA
//
// Equipo al volumen máximo de forma permanente, que es exactamente el peor
// caso que toda la Fase 2 existe para evitar. No es un "ok falso": es un
// "error falso" con daño real.
//
// La regla de COM es `SUCCEEDED(hr) = hr >= 0`, con el HRESULT leído como
// entero de 32 bits **con signo**: los códigos de éxito son `0x0xxxxxxx` y los
// de fallo `0x8xxxxxxx`. El truncado a 32 bits no es cosmético — `SyscallN`
// devuelve `uintptr`, que en amd64 tiene 64 bits, y la convención x64 de
// Windows sólo define los 32 bajos (el HRESULT viaja en EAX).
func comFailed(hr uintptr) bool {
	return int32(uint32(hr)) < 0
}
