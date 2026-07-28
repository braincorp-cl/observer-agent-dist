//go:build windows
// +build windows

package agent

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"unicode/utf16"
)

// Feature 028 · implementación de lock / alert / alarm en Windows.

// showAlert delega en WTSSendMessageW (ver usersession_windows.go). El texto del
// operador viaja como parámetro de una API, nunca como parte de un script: no
// hay ninguna superficie de inyección que escapar.
func (a *Agent) showAlert(title, message string) error {
	sessionID, user, err := activeUserSession()
	if err != nil {
		return err
	}

	a.Logger.Debugln("showAlert: enviando mensaje a la sesión", sessionID, "de", user)
	return wtsSendMessage(sessionID, title, message)
}

// lockScreen bloquea la estación de trabajo.
//
// `LockWorkStation` sólo surte efecto si la llama un proceso de la sesión
// interactiva; desde la sesión 0 del servicio falla silenciosamente. Por eso se
// lanza `rundll32` con el token del usuario. rundll32 no dibuja nada, así que no
// necesita escritorio asignado.
func (a *Agent) lockScreen() error {
	// Con timeout y no con WithCancel a secas: esto corre dentro de la goroutine
	// que responde el mensaje NATS. Un rundll32 colgado sin plazo dejaría al
	// operador esperando una respuesta que no llega.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rundll32 := filepath.Join(os.Getenv("WINDIR"), "System32", "rundll32.exe")

	cmd, err := a.newUserSessionCmd(ctx, rundll32, "user32.dll,LockWorkStation")
	if err != nil {
		return err
	}

	return cmd.Run()
}

// alarmSoundScript es el bucle de sonido que corre en la sesión del usuario.
//
// Estrategia en tres niveles, decidida por lo que Windows garantiza:
//
//   - Primero la sirena que genera el propio agente (`alarmsound.go`), que es la
//     única que suena como una alarma y no como una notificación.
//   - Si no está, un .wav de la carpeta Media del sistema.
//   - Si tampoco (Windows Server sin Desktop Experience no trae Media), se cae a
//     `SystemSounds`, que es parte de .NET y está siempre.
//
// La ruta de la sirena llega por VARIABLE DE ENTORNO y no interpolada en el texto del
// script. No es paranoia gratuita: es la misma regla que el resto de la feature —
// ningún dato entra a un script como parte de su código— y acá además evita tener que
// pensar en comillas y espacios de una ruta de Windows.
//
// El bucle es infinito a propósito: la duración la impone el lado Go matando el
// proceso (ver PlayAlarm en response.go). Un `for` con reloj acá duplicaría la
// lógica de tiempo y no sería interrumpible por `stopalarm`.
const alarmSoundScript = `
$ErrorActionPreference = 'SilentlyContinue'
Add-Type -AssemblyName System.Windows.Forms

$candidates = @()
if ($env:OBSERVER_ALARM_WAV) { $candidates += $env:OBSERVER_ALARM_WAV }
$candidates += @(
    "$env:WINDIR\Media\Alarm01.wav",
    "$env:WINDIR\Media\Ring01.wav",
    "$env:WINDIR\Media\Windows Foreground.wav",
    "$env:WINDIR\Media\Windows Notify System Generic.wav"
)

$sound = $null
foreach ($c in $candidates) {
    if (Test-Path -LiteralPath $c) {
        $sound = New-Object System.Media.SoundPlayer $c
        break
    }
}

while ($true) {
    if ($sound -ne $null) {
        $sound.PlaySync()
    } else {
        [System.Media.SystemSounds]::Exclamation.Play()
        Start-Sleep -Milliseconds 900
    }
    Start-Sleep -Milliseconds 250
}
`

// alarmLoopCmd construye el proceso que hace sonar la alarma.
//
// El audio de Windows es por sesión: un proceso de la sesión 0 no tiene endpoint
// de audio y no se oiría nada. De ahí el token de usuario.
//
// El script va por `-EncodedCommand` (UTF-16LE en base64) en vez de un archivo
// temporal: no deja rastro en disco, no depende de la política de ejecución y
// evita cualquier problema de comillas.
func (a *Agent) alarmLoopCmd(ctx context.Context) (*exec.Cmd, error) {
	encoded := encodePowershellCommand(alarmSoundScript)

	cmd, err := a.newUserSessionCmd(ctx, getPowershellExe(),
		"-NonInteractive", "-NoProfile", "-EncodedCommand", encoded)
	if err != nil {
		return nil, err
	}

	// La sirena se agrega al entorno del usuario que ya armó newUserSessionCmd.
	//
	// El `len(cmd.Env) > 0` no es una formalidad: si CreateEnvironmentBlock hubiera
	// fallado, `cmd.Env` viene nil y el hijo hereda el entorno del servicio. Añadirle
	// una sola variable ahí lo dejaría con un entorno de UN elemento —sin PATH, sin
	// SystemRoot— y PowerShell no arrancaría. Preferimos la alarma sin sirena antes
	// que la alarma sin alarma.
	if siren := a.ensureAlarmSiren(); siren != "" && len(cmd.Env) > 0 {
		cmd.Env = append(cmd.Env, "OBSERVER_ALARM_WAV="+siren)
	}

	return cmd, nil
}

// encodePowershellCommand deja un script listo para `-EncodedCommand`, que
// espera UTF-16 little endian en base64.
func encodePowershellCommand(script string) string {
	encoded := utf16.Encode([]rune(script))

	buf := make([]byte, 0, len(encoded)*2)
	for _, r := range encoded {
		buf = append(buf, byte(r), byte(r>>8))
	}

	return base64.StdEncoding.EncodeToString(buf)
}
