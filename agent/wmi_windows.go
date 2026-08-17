package agent

import (
	"encoding/json"

	"github.com/StackExchange/wmi"
	rmm "github.com/braincorp-cl/observer-agent-dist/shared"
)

func GetWin32_USBController() ([]interface{}, error) {
	var dst []rmm.Win32_USBController
	ret := make([]interface{}, 0)

	q := wmi.CreateQuery(&dst, "")
	err := wmi.Query(q, &dst)
	if err != nil {
		return ret, err
	}

	for _, val := range dst {
		b, err := json.Marshal(val)
		if err != nil {
			return ret, err
		}
		// this creates an extra unneeded array but keeping for now
		// for backwards compatibility with the python agent
		tmp := make([]interface{}, 0)
		var un map[string]interface{}
		if err := json.Unmarshal(b, &un); err != nil {
			return ret, err
		}
		tmp = append(tmp, un)
		ret = append(ret, tmp)
	}
	return ret, nil
}

func GetWin32_Processor() ([]interface{}, error) {
	var (
		dstEX    []rmm.Win32_ProcessorEX
		dst      []rmm.Win32_Processor
		errEX    error
		errORIG  error
		fallback bool = false
	)
	ret := make([]interface{}, 0)
	q := "SELECT * FROM Win32_Processor"

	errEX = wmi.Query(q, &dstEX)
	if errEX != nil {
		errORIG = wmi.Query(q, &dst)
		if errORIG != nil {
			return ret, errORIG
		}
	}

	if errEX == nil {
		for _, val := range dstEX {
			b, err := json.Marshal(val)
			if err != nil {
				fallback = true
				break
			}
			// this creates an extra unneeded array but keeping for now
			// for backwards compatibility with the python agent
			tmp := make([]interface{}, 0)
			var un map[string]interface{}
			if err := json.Unmarshal(b, &un); err != nil {
				return ret, err
			}
			tmp = append(tmp, un)
			ret = append(ret, tmp)
		}
		if !fallback {
			return ret, nil
		}
	}

	if errORIG == nil {
		for _, val := range dst {
			b, err := json.Marshal(val)
			if err != nil {
				return ret, err
			}
			tmp := make([]interface{}, 0)
			var un map[string]interface{}
			if err := json.Unmarshal(b, &un); err != nil {
				return ret, err
			}
			tmp = append(tmp, un)
			ret = append(ret, tmp)
		}
		return ret, nil
	}
	return ret, nil
}

func GetWin32_DesktopMonitor() ([]interface{}, error) {
	var dst []rmm.Win32_DesktopMonitor
	ret := make([]interface{}, 0)

	q := wmi.CreateQuery(&dst, "")
	err := wmi.Query(q, &dst)
	if err != nil {
		return ret, err
	}

	for _, val := range dst {
		b, err := json.Marshal(val)
		if err != nil {
			return ret, err
		}
		// this creates an extra unneeded array but keeping for now
		// for backwards compatibility with the python agent
		tmp := make([]interface{}, 0)
		var un map[string]interface{}
		if err := json.Unmarshal(b, &un); err != nil {
			return ret, err
		}
		tmp = append(tmp, un)
		ret = append(ret, tmp)

	}
	return ret, nil
}

func GetWin32_NetworkAdapter() ([]interface{}, error) {
	var dst []rmm.Win32_NetworkAdapter
	ret := make([]interface{}, 0)

	q := wmi.CreateQuery(&dst, "")
	err := wmi.Query(q, &dst)
	if err != nil {
		return ret, err
	}

	for _, val := range dst {
		b, err := json.Marshal(val)
		if err != nil {
			return ret, err
		}
		// this creates an extra unneeded array but keeping for now
		// for backwards compatibility with the python agent
		tmp := make([]interface{}, 0)
		var un map[string]interface{}
		if err := json.Unmarshal(b, &un); err != nil {
			return ret, err
		}
		tmp = append(tmp, un)
		ret = append(ret, tmp)

	}
	return ret, nil
}

func GetWin32_DiskDrive() ([]interface{}, error) {
	var dst []rmm.Win32_DiskDrive
	ret := make([]interface{}, 0)

	q := wmi.CreateQuery(&dst, "")
	err := wmi.Query(q, &dst)
	if err != nil {
		return ret, err
	}

	for _, val := range dst {
		b, err := json.Marshal(val)
		if err != nil {
			return ret, err
		}
		// this creates an extra unneeded array but keeping for now
		// for backwards compatibility with the python agent
		tmp := make([]interface{}, 0)
		var un map[string]interface{}
		if err := json.Unmarshal(b, &un); err != nil {
			return ret, err
		}
		tmp = append(tmp, un)
		ret = append(ret, tmp)

	}
	return ret, nil
}

func GetWin32_ComputerSystemProduct() ([]interface{}, error) {
	var dst []rmm.Win32_ComputerSystemProduct
	ret := make([]interface{}, 0)

	q := wmi.CreateQuery(&dst, "")
	err := wmi.Query(q, &dst)
	if err != nil {
		return ret, err
	}

	for _, val := range dst {
		b, err := json.Marshal(val)
		if err != nil {
			return ret, err
		}
		// this creates an extra unneeded array but keeping for now
		// for backwards compatibility with the python agent
		tmp := make([]interface{}, 0)
		var un map[string]interface{}
		if err := json.Unmarshal(b, &un); err != nil {
			return ret, err
		}
		tmp = append(tmp, un)
		ret = append(ret, tmp)

	}
	return ret, nil
}

func GetWin32_BIOS() ([]interface{}, error) {
	var (
		dstEX    []rmm.Win32_BIOSEX
		dst      []rmm.Win32_BIOS
		errEX    error
		errORIG  error
		fallback bool = false
	)
	ret := make([]interface{}, 0)
	q := "SELECT * FROM Win32_BIOS"

	errEX = wmi.Query(q, &dstEX)
	if errEX != nil {
		errORIG = wmi.Query(q, &dst)
		if errORIG != nil {
			return ret, errORIG
		}
	}

	if errEX == nil {
		for _, val := range dstEX {
			b, err := json.Marshal(val)
			if err != nil {
				fallback = true
				break
			}
			// this creates an extra unneeded array but keeping for now
			// for backwards compatibility with the python agent
			tmp := make([]interface{}, 0)
			var un map[string]interface{}
			if err := json.Unmarshal(b, &un); err != nil {
				return ret, err
			}
			tmp = append(tmp, un)
			ret = append(ret, tmp)
		}
		if !fallback {
			return ret, nil
		}
	}

	if errORIG == nil {
		for _, val := range dst {
			b, err := json.Marshal(val)
			if err != nil {
				return ret, err
			}
			tmp := make([]interface{}, 0)
			var un map[string]interface{}
			if err := json.Unmarshal(b, &un); err != nil {
				return ret, err
			}
			tmp = append(tmp, un)
			ret = append(ret, tmp)
		}
		return ret, nil
	}
	return ret, nil
}

func GetWin32_ComputerSystem() ([]interface{}, error) {
	var (
		dstEX    []rmm.Win32_ComputerSystemEX
		dst      []rmm.Win32_ComputerSystem
		errEX    error
		errORIG  error
		fallback bool = false
	)
	ret := make([]interface{}, 0)
	q := "SELECT * FROM Win32_ComputerSystem"

	errEX = wmi.Query(q, &dstEX)
	if errEX != nil {
		errORIG = wmi.Query(q, &dst)
		if errORIG != nil {
			return ret, errORIG
		}
	}

	if errEX == nil {
		for _, val := range dstEX {
			b, err := json.Marshal(val)
			if err != nil {
				fallback = true
				break
			}
			// this creates an extra unneeded array but keeping for now
			// for backwards compatibility with the python agent
			tmp := make([]interface{}, 0)
			var un map[string]interface{}
			if err := json.Unmarshal(b, &un); err != nil {
				return ret, err
			}
			tmp = append(tmp, un)
			ret = append(ret, tmp)
		}
		if !fallback {
			return ret, nil
		}
	}

	if errORIG == nil {
		for _, val := range dst {
			b, err := json.Marshal(val)
			if err != nil {
				return ret, err
			}
			tmp := make([]interface{}, 0)
			var un map[string]interface{}
			if err := json.Unmarshal(b, &un); err != nil {
				return ret, err
			}
			tmp = append(tmp, un)
			ret = append(ret, tmp)
		}
		return ret, nil
	}
	return ret, nil
}

func GetWin32_NetworkAdapterConfiguration() ([]interface{}, error) {
	var dst []rmm.Win32_NetworkAdapterConfiguration
	ret := make([]interface{}, 0)

	q := wmi.CreateQuery(&dst, "")
	err := wmi.Query(q, &dst)
	if err != nil {
		return ret, err
	}

	for _, val := range dst {
		b, err := json.Marshal(val)
		if err != nil {
			return ret, err
		}
		// this creates an extra unneeded array but keeping for now
		// for backwards compatibility with the python agent
		tmp := make([]interface{}, 0)
		var un map[string]interface{}
		if err := json.Unmarshal(b, &un); err != nil {
			return ret, err
		}
		tmp = append(tmp, un)
		ret = append(ret, tmp)

	}
	return ret, nil
}

func GetWin32_PhysicalMemory() ([]interface{}, error) {
	var (
		dstEX    []rmm.Win32_PhysicalMemoryEX
		dst      []rmm.Win32_PhysicalMemory
		errEX    error
		errORIG  error
		fallback bool = false
	)
	ret := make([]interface{}, 0)
	q := "SELECT * FROM Win32_PhysicalMemory"

	errEX = wmi.Query(q, &dstEX)
	if errEX != nil {
		errORIG = wmi.Query(q, &dst)
		if errORIG != nil {
			return ret, errORIG
		}
	}

	if errEX == nil {
		for _, val := range dstEX {
			b, err := json.Marshal(val)
			if err != nil {
				fallback = true
				break
			}
			// this creates an extra unneeded array but keeping for now
			// for backwards compatibility with the python agent
			tmp := make([]interface{}, 0)
			var un map[string]interface{}
			if err := json.Unmarshal(b, &un); err != nil {
				return ret, err
			}
			tmp = append(tmp, un)
			ret = append(ret, tmp)
		}
		if !fallback {
			return ret, nil
		}
	}

	if errORIG == nil {
		for _, val := range dst {
			b, err := json.Marshal(val)
			if err != nil {
				return ret, err
			}
			tmp := make([]interface{}, 0)
			var un map[string]interface{}
			if err := json.Unmarshal(b, &un); err != nil {
				return ret, err
			}
			tmp = append(tmp, un)
			ret = append(ret, tmp)
		}
		return ret, nil
	}
	return ret, nil
}

func GetWin32_OperatingSystem() ([]interface{}, error) {
	var dst []rmm.Win32_OperatingSystem
	ret := make([]interface{}, 0)

	q := wmi.CreateQuery(&dst, "")
	err := wmi.Query(q, &dst)
	if err != nil {
		return ret, err
	}

	for _, val := range dst {
		b, err := json.Marshal(val)
		if err != nil {
			return ret, err
		}
		// this creates an extra unneeded array but keeping for now
		// for backwards compatibility with the python agent
		tmp := make([]interface{}, 0)
		var un map[string]interface{}
		if err := json.Unmarshal(b, &un); err != nil {
			return ret, err
		}
		tmp = append(tmp, un)
		ret = append(ret, tmp)
	}
	return ret, nil
}

func GetWin32_BaseBoard() ([]interface{}, error) {
	var dst []rmm.Win32_BaseBoard
	ret := make([]interface{}, 0)

	q := wmi.CreateQuery(&dst, "")
	err := wmi.Query(q, &dst)
	if err != nil {
		return ret, err
	}

	for _, val := range dst {
		b, err := json.Marshal(val)
		if err != nil {
			return ret, err
		}
		// this creates an extra unneeded array but keeping for now
		// for backwards compatibility with the python agent
		tmp := make([]interface{}, 0)
		var un map[string]interface{}
		if err := json.Unmarshal(b, &un); err != nil {
			return ret, err
		}
		tmp = append(tmp, un)
		ret = append(ret, tmp)
	}
	return ret, nil
}

func GetWin32_VideoController() ([]interface{}, error) {
	var dst []rmm.Win32_VideoController
	ret := make([]interface{}, 0)

	q := wmi.CreateQuery(&dst, "")
	err := wmi.Query(q, &dst)
	if err != nil {
		return ret, err
	}

	for _, val := range dst {
		b, err := json.Marshal(val)
		if err != nil {
			return ret, err
		}
		// this creates an extra unneeded array but keeping for now
		// for backwards compatibility with the python agent
		tmp := make([]interface{}, 0)
		var un map[string]interface{}
		if err := json.Unmarshal(b, &un); err != nil {
			return ret, err
		}
		tmp = append(tmp, un)
		ret = append(ret, tmp)
	}
	return ret, nil
}

func (a *Agent) GetWMIInfo() map[string]interface{} {
	wmiInfo := make(map[string]interface{})

	compSysProd, err := GetWin32_ComputerSystemProduct()
	if err != nil {
		a.Logger.Debugln(err)
	}

	compSys, err := GetWin32_ComputerSystem()
	if err != nil {
		a.Logger.Debugln(err)
	}

	netAdaptConfig, err := GetWin32_NetworkAdapterConfiguration()
	if err != nil {
		a.Logger.Debugln(err)
	}

	physMem, err := GetWin32_PhysicalMemory()
	if err != nil {
		a.Logger.Debugln(err)
	}

	winOS, err := GetWin32_OperatingSystem()
	if err != nil {
		a.Logger.Debugln(err)
	}

	baseBoard, err := GetWin32_BaseBoard()
	if err != nil {
		a.Logger.Debugln(err)
	}

	bios, err := GetWin32_BIOS()
	if err != nil {
		a.Logger.Debugln(err)
	}

	disk, err := GetWin32_DiskDrive()
	if err != nil {
		a.Logger.Debugln(err)
	}

	netAdapt, err := GetWin32_NetworkAdapter()
	if err != nil {
		a.Logger.Debugln(err)
	}

	desktopMon, err := GetWin32_DesktopMonitor()
	if err != nil {
		a.Logger.Debugln(err)
	}

	cpu, err := GetWin32_Processor()
	if err != nil {
		a.Logger.Debugln(err)
	}

	usb, err := GetWin32_USBController()
	if err != nil {
		a.Logger.Debugln(err)
	}

	graphics, err := GetWin32_VideoController()
	if err != nil {
		a.Logger.Debugln(err)
	}

	// Feature 037 · el estado de cifrado NO sigue el patron de arriba, y la
	// diferencia es deliberada.
	//
	// Las GetWin32_* devuelven `([]interface{}, error)` y aca el error se
	// registra en el log y se DESCARTA: el mapa se queda con el valor cero y
	// el servidor recibe una lista vacia indistinguible de "este equipo no
	// tiene tarjetas de video". Para el inventario eso es tolerable.
	//
	// Para una feature de cumplimiento no lo es: una consulta que fallo y un
	// equipo sin cifrar se veen igual en el panel, y el que decide sobre un
	// equipo robado lee "sin cifrar" cuando la verdad era "no sabemos"
	// (RF-07). Por eso GetDiskEncryption no devuelve error: mete el fallo
	// DENTRO del sobre, junto con el "soportado", y los tres estados llegan
	// distinguibles al otro lado.
	diskEncryption := GetDiskEncryption()

	wmiInfo["comp_sys_prod"] = compSysProd
	wmiInfo["comp_sys"] = compSys
	wmiInfo["network_config"] = netAdaptConfig
	wmiInfo["mem"] = physMem
	wmiInfo["os"] = winOS
	wmiInfo["base_board"] = baseBoard
	wmiInfo["bios"] = bios
	wmiInfo["disk"] = disk
	wmiInfo["network_adapter"] = netAdapt
	wmiInfo["desktop_monitor"] = desktopMon
	wmiInfo["cpu"] = cpu
	wmiInfo["usb"] = usb
	wmiInfo["graphics"] = graphics
	wmiInfo["disk_encryption"] = diskEncryption

	return wmiInfo
}
