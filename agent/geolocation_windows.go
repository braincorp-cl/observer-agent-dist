//go:build windows

package agent

import (
	"encoding/base64"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Feature 023 · provider Windows. Usa la API WinRT
// Windows.Devices.Geolocation.Geolocator a través de PowerShell (el agente ya
// depende de PowerShell para otras tareas), evitando añadir un binding WinRT en
// Go. La salida es "lat,long,accuracy" (cultura invariante) o "DENIED" cuando el
// servicio de ubicación del SO está apagado o el permiso fue denegado por
// directiva. Ejecutar como SYSTEM puede devolver DENIED según GPO → se reporta
// como source=denied sin abortar el check-in.
const geoPowershellScript = `
$ErrorActionPreference = 'Stop'
try {
  [void][Windows.Devices.Geolocation.Geolocator,Windows.Devices.Geolocation,ContentType=WindowsRuntime]
  $locator = New-Object Windows.Devices.Geolocation.Geolocator
  $locator.DesiredAccuracy = [Windows.Devices.Geolocation.PositionAccuracy]::High
  function Await($t, $rt) {
    $m = ([System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name.StartsWith('IAsyncOperation') })[0]
    $g = $m.MakeGenericMethod($rt)
    $task = $g.Invoke($null, @($t))
    [void]$task.Wait(15000)
    $task.Result
  }
  $pos = Await ($locator.GetGeopositionAsync()) ([Windows.Devices.Geolocation.Geoposition])
  $p = $pos.Coordinate.Point.Position
  $acc = [int]$pos.Coordinate.Accuracy
  $ci = [System.Globalization.CultureInfo]::InvariantCulture
  Write-Output ('{0},{1},{2}' -f $p.Latitude.ToString($ci), $p.Longitude.ToString($ci), $acc)
} catch [System.UnauthorizedAccessException] {
  Write-Output 'DENIED'
} catch {
  if ($_.Exception.Message -match 'denied|Unauthorized|disabled|location') { Write-Output 'DENIED' } else { Write-Output ('ERROR: ' + $_.Exception.Message) }
}
`

func osLocation(a *Agent) (Location, error) {
	psExe := getPowershellExe()
	encoded := encodeUTF16LEBase64(geoPowershellScript)
	cmd := exec.Command(psExe, "-NonInteractive", "-NoProfile", "-EncodedCommand", encoded)
	out, err := cmd.Output()
	if err != nil {
		return Location{Source: SourceUnavailable}, err
	}

	line := strings.TrimSpace(string(out))
	switch {
	case line == "DENIED":
		return Location{Source: SourceDenied}, fmt.Errorf("geolocation permission denied")
	case strings.HasPrefix(line, "ERROR"):
		return Location{Source: SourceUnavailable}, fmt.Errorf("geolocation: %s", line)
	}

	parts := strings.Split(line, ",")
	if len(parts) < 3 {
		return Location{Source: SourceUnavailable}, fmt.Errorf("unexpected geolocation output")
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	long, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	acc, _ := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	if err1 != nil || err2 != nil {
		return Location{Source: SourceUnavailable}, fmt.Errorf("parse coords: %v %v", err1, err2)
	}
	return Location{Lat: lat, Long: long, AccuracyM: int(acc), Source: SourceNative}, nil
}

// encodeUTF16LEBase64 codifica el script para -EncodedCommand (PowerShell espera
// UTF-16LE en base64), evitando problemas de escaping al pasar el script inline.
func encodeUTF16LEBase64(s string) string {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2)
	for i, r := range u {
		b[i*2] = byte(r)
		b[i*2+1] = byte(r >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}
