package agent

import "testing"

// isUsablePublicIP es la guarda que evita publicar basura en el campo public_ip de
// la consola. Los casos que importan no son los obvios: son los que un resolver
// secuestrado, o el router de la LAN respondiendo por su cuenta, devolvería.
func TestIsUsablePublicIP(t *testing.T) {
	casos := []struct {
		in   string
		want bool
		por  string
	}{
		{"186.10.233.30", true, "IPv4 pública real (la medida en el Fedora)"},
		{"8.8.8.8", true, "IPv4 pública"},
		{"1.2.3.4", true, "IPv4 pública"},

		// Lo que devolvería un resolver de LAN contestando por su cuenta.
		{"192.168.1.15", false, "RFC1918 /16"},
		{"10.20.0.104", false, "RFC1918 /8 — justo la IP del backend por VPN"},
		{"172.16.128.198", false, "RFC1918 /12 — la IP del túnel del Fedora"},
		{"127.0.0.1", false, "loopback"},
		{"169.254.10.1", false, "link-local"},
		{"224.0.0.1", false, "multicast"},
		{"0.0.0.0", false, "unspecified"},

		// CGNAT: es NAT del operador, no una dirección que el mundo vea.
		{"100.64.0.1", false, "CGNAT límite inferior"},
		{"100.127.255.254", false, "CGNAT límite superior"},
		{"100.63.255.255", true, "justo ANTES del rango CGNAT: es pública"},
		{"100.128.0.1", true, "justo DESPUÉS del rango CGNAT: es pública"},

		// IPv6 y basura.
		{"2606:4700::6810:b9f1", false, "IPv6: fuera de alcance de este camino"},
		{"::1", false, "loopback IPv6"},
		{"", false, "vacío"},
		{"error", false, "el propio centinela de PublicIP"},
		{"no soy una ip", false, "texto"},
		{"1.2.3", false, "IPv4 incompleta"},
		{"999.1.1.1", false, "octeto fuera de rango"},
		{"<html>bloqueado</html>", false, "página de bloqueo de un filtro web"},
	}

	for _, c := range casos {
		if got := isUsablePublicIP(c.in); got != c.want {
			t.Errorf("isUsablePublicIP(%q) = %v, se esperaba %v — %s", c.in, got, c.want, c.por)
		}
	}
}

// pickDNSPublicIP decide con qué respuesta quedarse. Lo que este test fija es la
// política, no la mecánica: dos proveedores que discrepan NO invalidan el dato,
// porque con VPN de túnel parcial cada resolver puede alcanzarse por una ruta
// distinta y las dos respuestas ser legítimas.
func TestPickDNSPublicIP(t *testing.T) {
	casos := []struct {
		nombre     string
		in         []dnsPublicIPResult
		wantIP     string
		wantAgreed bool
	}{
		{
			nombre: "sin respuestas: no se inventa nada",
			in:     nil,
			wantIP: "", wantAgreed: false,
		},
		{
			nombre: "un solo proveedor: se usa, pero sin verificación cruzada",
			in:     []dnsPublicIPResult{{"opendns", "186.10.233.30"}},
			wantIP: "186.10.233.30", wantAgreed: false,
		},
		{
			nombre: "los dos coinciden: verificación cruzada OK",
			in: []dnsPublicIPResult{
				{"opendns", "186.10.233.30"},
				{"google", "186.10.233.30"},
			},
			wantIP: "186.10.233.30", wantAgreed: true,
		},
		{
			nombre: "discrepan: se usa el primero y agreed=false (el llamador lo registra)",
			in: []dnsPublicIPResult{
				{"opendns", "186.10.233.30"},
				{"google", "200.27.1.1"},
			},
			wantIP: "186.10.233.30", wantAgreed: false,
		},
	}

	for _, c := range casos {
		gotIP, gotAgreed := pickDNSPublicIP(c.in)
		if gotIP != c.wantIP || gotAgreed != c.wantAgreed {
			t.Errorf("%s: pickDNSPublicIP = (%q, %v), se esperaba (%q, %v)",
				c.nombre, gotIP, gotAgreed, c.wantIP, c.wantAgreed)
		}
	}
}

// El resolver del fallback NO debe usar /etc/resolv.conf: la respuesta que se busca
// es la IP de origen que ve el servidor consultado, así que preguntarle al resolver
// del sistema (habitualmente el router) devolvería una privada o nada. Este test fija
// las dos condiciones que lo garantizan: PreferGo y un Dial propio.
func TestNewFixedDNSResolverIgnoraElSistema(t *testing.T) {
	r := newFixedDNSResolver("208.67.222.222:53")
	if !r.PreferGo {
		t.Error("PreferGo debe ser true: sin él el resolver del sistema puede tomar el control vía cgo y el Dial se ignora en silencio")
	}
	if r.Dial == nil {
		t.Error("Dial debe estar seteado: es lo que fuerza la consulta al servidor elegido")
	}
}
