package agent

import (
	"testing"
	"time"
)

func TestAcumuladorResumenBasico(t *testing.T) {
	ac := nuevoAcumulador("PC-1", "linux", "-03:00")
	base := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	// 10 muestras de 60s activas en excel, 5 inactivas.
	for i := 0; i < 10; i++ {
		ac.Agregar(base.Add(time.Duration(i)*time.Minute), "ana", "excel.exe", "Informe", true, 60)
	}
	for i := 10; i < 15; i++ {
		ac.Agregar(base.Add(time.Duration(i)*time.Minute), "ana", "excel.exe", "", false, 60)
	}
	res, ok := ac.ResumenDe("2026-10-10", true)
	if !ok {
		t.Fatal("sin resumen")
	}
	if len(res.Dias) != 1 || len(res.Dias[0].Usuarios) != 1 {
		t.Fatalf("estructura: %+v", res)
	}
	u := res.Dias[0].Usuarios[0]
	if u.Usuario != "ana" || u.ActivoMin != 10 || u.InactivoMin != 5 {
		t.Fatalf("activo/inactivo: %+v", u)
	}
	if u.Procs["excel.exe"] != 10 {
		t.Fatalf("procs: %+v", u.Procs)
	}
	if len(u.TitulosTop) != 1 || u.TitulosTop[0][0] != "Informe" {
		t.Fatalf("titulos: %+v", u.TitulosTop)
	}
	if res.Dias[0].Parcial != true {
		t.Fatal("parcial esperado")
	}
}

func TestAcumuladorDosUsuariosYPurga(t *testing.T) {
	ac := nuevoAcumulador("PC-1", "linux", "-03:00")
	d1 := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	ac.Agregar(d1, "ana", "firefox", "", true, 60)
	ac.Agregar(d2, "ana", "excel.exe", "", true, 60)
	ac.Agregar(d2, "bruno", "chrome", "", true, 60)
	if _, ok := ac.ResumenDe("2026-10-10", false); !ok {
		t.Fatal("falta dia 10")
	}
	if n := len(ac.Dias["2026-10-10"]); n != 2 {
		t.Fatalf("usuarios dia 10: %d", n)
	}
	ac.PurgarAnteriores("2026-10-10")
	if _, ok := ac.Dias["2026-10-09"]; ok {
		t.Fatal("el día 9 debió purgarse")
	}
}

func TestProcesoReal(t *testing.T) {
	for _, p := range []string{"", "(escritorio)", "(desconocido)"} {
		if procesoReal(p) {
			t.Fatalf("%q no debería ser proceso real", p)
		}
	}
	if !procesoReal("firefox") {
		t.Fatal("firefox sí es real")
	}
}
