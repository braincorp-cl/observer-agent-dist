//go:build windows

package agent

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Feature 023 · provider Windows (F4).
//
// La API nativa WinRT Windows.Devices.Geolocation.Geolocator NO funciona desde el
// servicio del agente: corriendo como SYSTEM / sesión 0 la operación async nunca
// es atendida (confirmado en Win10 22H2, Win11 23H2/24H2 y hardware físico), y la
// doc de Microsoft exige app en primer plano con hilo de UI. Descartada.
//
// Se adopta el enfoque de Prey: escanear las antenas WiFi visibles con
// `netsh wlan show networks mode=bssid` (sin UI, sin WinRT) y resolverlas vía
// resolveWifiLocation() (código común) contra el backend, que consulta a Google
// con la key server-side. En Windows 11 24H2 / Server 2025 el escaneo de BSSIDs
// exige que la ubicación del sistema esté activada (se pre-configura por GPO); en
// Windows 10 y Server 2019/2022 no hay esa restricción. Sin antenas → GetLocation
// degrada a IP.

// netsh imprime, por cada red, un bloque por BSSID: "BSSID n : xx:xx:.." seguido de
// "Signal : NN%", "Channel : N", etc. El literal BSSID es estable entre locales; la
// señal la detectamos por el símbolo "%" (independiente del idioma). El canal por su
// etiqueta en los locales relevantes (EN "Channel" / ES "Canal"); si no se detecta,
// queda 0 y se omite del request.
var winBSSIDRe = regexp.MustCompile(`(?i)BSSID[ \t]*\d*[ \t]*:[ \t]*([0-9A-Fa-f]{2}(?::[0-9A-Fa-f]{2}){5})`)
var winPctRe = regexp.MustCompile(`(\d{1,3})\s*%`)
var winChannelRe = regexp.MustCompile(`(?i)(?:channel|canal)\D+(\d+)`)

func osLocation(a *Agent) (Location, error) {
	// Feature 041: fuera de geocerca se fuerza la radio WiFi encendida aunque el
	// force-on GLOBAL (geo_force_on) esté apagado —para que la localización por WiFi
	// no degrade a IP mientras el equipo está fuera del sitio—. forceLocationOn es
	// transitorio (netsh admin=enabled): NO toca el power-management persistente de
	// la NIC, así que no deja secuela tras revertir la geocerca.
	if a.GeoForceOn || a.getOutsideGeofence() {
		forceLocationOn(a)
	}
	aps := scanWifiBSSIDs(a)
	// Gap 3 (permiso activo): en Windows 11 24H2 / Server 2025 el escaneo de BSSIDs
	// exige la ubicación del sistema ACTIVADA. Si el escaneo vino vacío y el
	// ConsentStore dice Deny, es ese gate (no "sin WiFi"): lo reportamos como
	// denied —visible en la consola y accionable por GPO— en vez de degradar mudo a
	// IP. Si hubo antenas (Win10 / Server 2019-2022 no gatean el escaneo), o si no
	// se puede afirmar la negativa, resolvemos/degradamos normal.
	if len(aps) == 0 && locationConsentDenied() {
		a.Logger.Infoln("geo: ubicación del sistema desactivada (ConsentStore=Deny); " +
			"el escaneo WiFi no es posible. Actívala por GPO para geolocalizar por WiFi.")
		return Location{Source: SourceDenied}, nil
	}
	return a.resolveWifiLocation(aps)
}

// forceLocationOn habilita el sensor de ubicación del dispositivo y la radio WiFi
// (Feature 023 · gap 3). Política: el endpoint es un ACTIVO CORPORATIVO y el agente
// corre como SYSTEM; no hay opt-in del usuario final. Best-effort: cualquier fallo
// se loguea y NO interrumpe la captura (que degradará a IP si el escaneo sigue
// vacío). Es idempotente: si ya está permitido, reescribe el mismo valor.
func forceLocationOn(a *Agent) {
	// 1. Consentimiento de ubicación a nivel-dispositivo = Allow. ESTE es el gate
	//    que Windows 11 24H2 / Server 2025 exige para que netsh/WlanScan lean
	//    BSSIDs; sin él, un servicio SYSTEM no puede escanear (ni disparar prompt).
	setLocationConsentAllow(a)
	// 2. Servicio de geolocalización (lfsvc): arranque automático + iniciado.
	_ = exec.Command("sc", "config", "lfsvc", "start=", "auto").Run()
	_ = exec.Command("sc", "start", "lfsvc").Run()
	// 3. Radio WiFi encendida (best-effort). netsh reactiva un adaptador
	//    administrativamente deshabilitado; el soft-block de radio (airplane) por
	//    WinRT Windows.Devices.Radios NO es fiable desde SYSTEM/sesión 0, así que
	//    no dependemos de él (los endpoints corporativos rara vez lo tienen off).
	for _, name := range []string{"Wi-Fi", "Wireless Network Connection", "WiFi"} {
		_ = exec.Command("netsh", "interface", "set", "interface",
			"name="+name, "admin=enabled").Run()
	}
}

// setLocationConsentAllow pone el consentimiento de ubicación en "Allow" a nivel
// dispositivo (HKLM) y de la cuenta de servicio (HKCU) y marca el estado del
// servicio de ubicación. Best-effort por clave; loguea el fallo pero sigue.
func setLocationConsentAllow(a *Agent) {
	consent := `SOFTWARE\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\location`
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		k, _, err := registry.CreateKey(root, consent, registry.SET_VALUE)
		if err != nil {
			a.Logger.Debugln("forceLocationOn: no se pudo abrir ConsentStore:", err)
			continue
		}
		if err := k.SetStringValue("Value", "Allow"); err != nil {
			a.Logger.Debugln("forceLocationOn: no se pudo setear Location=Allow:", err)
		}
		k.Close()
	}
	if k, _, err := registry.CreateKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Services\lfsvc\Service\Configuration`,
		registry.SET_VALUE); err == nil {
		_ = k.SetDWordValue("Status", 1)
		k.Close()
	}
}

// locationConsentDenied lee el interruptor de ubicación del dispositivo
// (HKLM\...\CapabilityAccessManager\ConsentStore\location, valor "Value"). Devuelve
// true SOLO si está explícitamente en "Deny". Si no se puede leer (clave ausente,
// build antiguo, sin permiso), devuelve false: ante la duda NO bloqueamos —dejamos
// que GetLocation degrade a IP— para no perder el fix por una lectura fallida.
func locationConsentDenied() bool {
	k, err := registry.OpenKey(
		registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\location`,
		registry.QUERY_VALUE,
	)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringValue("Value")
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(v), "Deny")
}

// wlanapi expone WlanScan para forzar un barrido activo de antenas antes de leer la
// caché con netsh. Estando ASOCIADO a una red, Windows suele reportar solo el AP
// conectado y hace el barrido completo (vecinas incluidas) solo periódicamente; sin
// forzarlo, el escaneo del ticker ve <2 antenas casi siempre y la geo degrada a IP
// aunque haya cobertura WiFi. Con el barrido forzado el fix WiFi es consistente.
var (
	wlanapi                = windows.NewLazySystemDLL("wlanapi.dll")
	procWlanOpenHandle     = wlanapi.NewProc("WlanOpenHandle")
	procWlanEnumInterfaces = wlanapi.NewProc("WlanEnumInterfaces")
	procWlanScan           = wlanapi.NewProc("WlanScan")
	procWlanFreeMemory     = wlanapi.NewProc("WlanFreeMemory")
	procWlanCloseHandle    = wlanapi.NewProc("WlanCloseHandle")
)

// El barrido de WlanScan es asíncrono; Windows tarda ~4 s en completarlo y refrescar
// la caché que lee netsh. No hay señal síncrona simple desde un servicio, así que se
// espera un margen fijo antes de leer.
const wlanScanSettleTime = 4 * time.Second

// https://learn.microsoft.com/windows/win32/api/wlanapi/ns-wlanapi-wlan_interface_info
type wlanInterfaceInfo struct {
	InterfaceGUID  windows.GUID
	InterfaceDescr [256]uint16
	State          uint32
}

// https://learn.microsoft.com/windows/win32/api/wlanapi/ns-wlanapi-wlan_interface_info_list
type wlanInterfaceInfoList struct {
	NumberOfItems uint32
	Index         uint32
	InterfaceInfo [1]wlanInterfaceInfo
}

// forceWifiScan dispara un barrido activo (WlanScan) en cada interfaz WLAN y espera a
// que Windows actualice la caché. Best-effort: si wlanapi no está disponible (p.ej.
// Server Core sin WLAN AutoConfig) o cualquier paso falla, se loguea y se sigue —
// scanWifiBSSIDs leerá lo que haya en caché. No desasocia la red (el barrido en
// segundo plano no corta la conexión).
func forceWifiScan(a *Agent) {
	if err := wlanapi.Load(); err != nil {
		a.Logger.Debugln("forceWifiScan: wlanapi.dll no disponible:", err)
		return
	}
	for _, p := range []*windows.LazyProc{
		procWlanOpenHandle, procWlanEnumInterfaces, procWlanScan,
		procWlanFreeMemory, procWlanCloseHandle,
	} {
		if err := p.Find(); err != nil {
			a.Logger.Debugln("forceWifiScan: función wlanapi no disponible:", err)
			return
		}
	}

	var handle windows.Handle
	var negotiated uint32
	if r, _, _ := procWlanOpenHandle.Call(2, 0,
		uintptr(unsafe.Pointer(&negotiated)),
		uintptr(unsafe.Pointer(&handle))); r != 0 {
		a.Logger.Debugln("forceWifiScan: WlanOpenHandle falló:", r)
		return
	}
	defer procWlanCloseHandle.Call(uintptr(handle), 0)

	var list *wlanInterfaceInfoList
	if r, _, _ := procWlanEnumInterfaces.Call(uintptr(handle), 0,
		uintptr(unsafe.Pointer(&list))); r != 0 || list == nil {
		a.Logger.Debugln("forceWifiScan: WlanEnumInterfaces falló:", r)
		return
	}
	defer procWlanFreeMemory.Call(uintptr(unsafe.Pointer(list)))

	step := unsafe.Sizeof(list.InterfaceInfo[0])
	scanned := false
	for i := uintptr(0); i < uintptr(list.NumberOfItems); i++ {
		info := (*wlanInterfaceInfo)(unsafe.Add(unsafe.Pointer(&list.InterfaceInfo[0]), i*step))
		guid := info.InterfaceGUID
		if r, _, _ := procWlanScan.Call(uintptr(handle),
			uintptr(unsafe.Pointer(&guid)), 0, 0, 0); r == 0 {
			scanned = true
		}
	}
	if scanned {
		time.Sleep(wlanScanSettleTime)
	}
}

// scanWifiBSSIDs fuerza un barrido activo (WlanScan) y luego lista las antenas
// visibles con netsh, acumulando por cada BSSID su señal (%→dBm aprox.) y su canal.
// Lista vacía si netsh falla o no hay WiFi (→ degradación a IP). No expone las MAC en
// logs de nivel normal.
func scanWifiBSSIDs(a *Agent) []wifiAP {
	forceWifiScan(a)
	out, err := exec.Command("netsh", "wlan", "show", "networks", "mode=bssid").Output()
	if err != nil {
		a.Logger.Debugln("scanWifiBSSIDs: netsh no disponible:", err)
		return nil
	}

	var aps []wifiAP
	var cur *wifiAP
	haveSignal := false
	flush := func() {
		if cur != nil {
			aps = append(aps, *cur)
		}
		cur = nil
		haveSignal = false
	}

	for _, line := range strings.Split(string(out), "\n") {
		if m := winBSSIDRe.FindStringSubmatch(line); m != nil {
			flush()
			cur = &wifiAP{MacAddress: strings.ToLower(m[1]), SignalStrength: -65}
			continue
		}
		if cur == nil {
			continue
		}
		if !haveSignal {
			if s := winPctRe.FindStringSubmatch(line); s != nil {
				if pct, perr := strconv.Atoi(s[1]); perr == nil && pct > 0 {
					cur.SignalStrength = pct/2 - 100 // 100%→-50dBm, 0%→-100dBm (aprox.)
				}
				haveSignal = true
				continue
			}
		}
		if cur.Channel == 0 {
			if c := winChannelRe.FindStringSubmatch(line); c != nil {
				if ch, cerr := strconv.Atoi(c[1]); cerr == nil {
					cur.Channel = ch
				}
			}
		}
	}
	flush()
	return aps
}
