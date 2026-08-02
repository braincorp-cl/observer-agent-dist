package agent

import "testing"

// Tabla CONGELADA: cada par salió de ejecutar
// `cases.Title(language.AmericanEnglish).String(entrada)` con
// golang.org/x/text v0.21.0 —la versión que este árbol tenía— ANTES de sacar la
// dependencia (2026-08-02). No es una expectativa escrita a mano: es el
// comportamiento medido del código que titleCasePlatform reemplaza, así que este
// test es lo que sostiene que la consola siga mostrando exactamente lo mismo.
//
// Los casos patológicos ('!', '_', '.', '/', dígitos iniciales, no-ASCII) están acá
// a propósito, aunque el dominio real sean IDs de distro: son los que distinguen la
// regla implementada de una "capitalizar la primera letra y ya".
var casosTitleCase = []struct {
	in, want string
}{
	// IDs de /etc/os-release y macOS — el dominio real.
	{"ubuntu", "Ubuntu"},
	{"debian", "Debian"},
	{"linuxmint", "Linuxmint"},
	{"fedora", "Fedora"},
	{"centos", "Centos"},
	{"rhel", "Rhel"},
	{"arch", "Arch"},
	{"manjaro", "Manjaro"},
	{"opensuse-leap", "Opensuse-Leap"},
	{"opensuse-tumbleweed", "Opensuse-Tumbleweed"},
	{"raspbian", "Raspbian"},
	{"amzn", "Amzn"},
	{"alpine", "Alpine"},
	{"kali", "Kali"},
	{"darwin", "Darwin"},
	{"rocky", "Rocky"},
	{"almalinux", "Almalinux"},
	{"sles", "Sles"},
	{"ol", "Ol"},
	{"gentoo", "Gentoo"},
	{"nixos", "Nixos"},
	{"endeavouros", "Endeavouros"},
	{"zorin", "Zorin"},
	{"mx", "Mx"},
	{"devuan", "Devuan"},

	// Varias palabras.
	{"ubuntu core", "Ubuntu Core"},
	{"red hat enterprise linux", "Red Hat Enterprise Linux"},
	{"elementary os", "Elementary Os"},
	{"linux mint 22.2", "Linux Mint 22.2"},
	{"arch linux", "Arch Linux"},
	{"a b", "A B"},

	// Entrada ya con mayúsculas: el resto se baja.
	{"UBUNTU", "Ubuntu"},
	{"Debian", "Debian"},
	{"MiXeD cAsE", "Mixed Case"},

	// Vacío y espacios en los bordes.
	{"", ""},
	{" leading", " Leading"},
	{"trailing ", "Trailing "},
	{"double  space", "Double  Space"},

	// Los que fijan la regla: '!' y '/' cortan palabra; '_' y '.' NO.
	{"pop!_os", "Pop!_Os"},
	{"os-2/warp_x", "Os-2/Warp_x"},
	{"a.b.c", "A.b.c"},
	{"foo/bar", "Foo/Bar"},
	{"x11-wayland", "X11-Wayland"},

	// La mayúscula va en la primera LETRA de la palabra, no en el primer rune.
	{"3cx", "3Cx"},
	{"v2 os", "V2 Os"},
	{"os2 warp", "Os2 Warp"},
	{"e2fs 1.2.3", "E2fs 1.2.3"},
	{"windows10pro", "Windows10pro"},

	// No-ASCII.
	{"über", "Über"},
	{"ñandú", "Ñandú"},
}

func TestTitleCasePlatform(t *testing.T) {
	for _, c := range casosTitleCase {
		if got := titleCasePlatform(c.in); got != c.want {
			t.Errorf("titleCasePlatform(%q) = %q, se esperaba %q (salida medida de x/text v0.21.0)", c.in, got, c.want)
		}
	}
}

// La función se llama por cada osString(), que corre en cada check-in de inventario:
// no debe asignar de más ni depender del largo de la entrada de forma rara.
func BenchmarkTitleCasePlatform(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = titleCasePlatform("opensuse-leap")
	}
}
