package agent

import "testing"

// TestComFailed congela la regla `SUCCEEDED(hr) = hr >= 0` con los HRESULT
// reales que aparecen en el camino del volumen de Windows.
//
// El caso que da sentido a todo el archivo es **S_FALSE**: medido el
// 2026-07-28 en `LAPTOP-DSHQMP1O` como valor de retorno de
// `IAudioEndpointVolume::SetMute` cuando el equipo ya estaba desilenciado.
// Con la regla vieja ("distinto de cero es fallo") ese éxito se leía como
// error y el equipo se quedaba al volumen máximo de forma permanente.
func TestComFailed(t *testing.T) {
	// Los 32 bits altos de un `uintptr` de 64 no están definidos por la
	// convención x64 de Windows: el HRESULT viaja en EAX.
	//
	// ⚠️ El desplazamiento tiene que ser de EJECUCIÓN, con la máscara en una
	// variable: escrito como constante (`uintptr(0xFFFFFFFF) << 32`) Go lo
	// pliega en compilación y **windows/386 no compila**, porque ahí `uintptr`
	// tiene 32 bits y el literal se desborda. Con la variable, en 386 el
	// desplazamiento da 0 —comportamiento definido por la especificación— y
	// los dos casos degradan a su HRESULT limpio, que sigue siendo válido.
	// El desplazamiento va en DOS pasos de 16 y no en uno de 32 porque el
	// chequeo `shift` de `go vet` rechaza desplazar un valor de 32 bits por 32
	// —que es lo que ve al analizar windows/386— aunque el resultado esté
	// definido.
	var mascaraAlta uintptr = 0xFFFFFFFF
	basuraAlta := mascaraAlta << 16 << 16

	casos := []struct {
		nombre string
		hr     uintptr
		falla  bool
	}{
		{"S_OK", 0x00000000, false},
		{"S_FALSE — SetMute con el silencio ya en el valor pedido", 0x00000001, false},
		{"E_FAIL", 0x80004005, true},
		{"E_ACCESSDENIED", 0x80070005, true},
		{"E_INVALIDARG", 0x80070057, true},
		{"E_POINTER", 0x80004003, true},
		{"E_NOINTERFACE", 0x80004002, true},
		{"RPC_E_CHANGED_MODE", 0x80010106, true},
		{"AUDCLNT_E_DEVICE_INVALIDATED", 0x88890004, true},
		{"CO_E_NOTINITIALIZED", 0x800401F0, true},

		// El borde exacto de la regla: 0x7FFFFFFF es el último éxito y
		// 0x80000000 el primer fallo.
		{"último código de éxito", 0x7FFFFFFF, false},
		{"primer código de fallo", 0x80000000, true},

		{"S_FALSE con basura en los 32 bits altos", basuraAlta | 0x00000001, false},
		{"E_FAIL con basura en los 32 bits altos", basuraAlta | 0x80004005, true},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := comFailed(c.hr); got != c.falla {
				t.Errorf("comFailed(0x%X) = %v, se esperaba %v", c.hr, got, c.falla)
			}
		})
	}
}
