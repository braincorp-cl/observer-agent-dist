package agent

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"math/rand"

	crand "crypto/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	goDebug "runtime/debug"
	"strconv"
	"strings"
	"time"

	ps "github.com/elastic/go-sysinfo"
	"github.com/go-ping/ping"
	"github.com/go-resty/resty/v2"
	"github.com/shirou/gopsutil/v3/process"
)

type PingResponse struct {
	Status string
	Output string
}

func DoPing(host string) (PingResponse, error) {
	var ret PingResponse
	pinger, err := ping.NewPinger(host)
	if err != nil {
		return ret, err
	}

	var buf bytes.Buffer
	pinger.OnRecv = func(pkt *ping.Packet) {
		fmt.Fprintf(&buf, "%d bytes from %s: icmp_seq=%d time=%v\n",
			pkt.Nbytes, pkt.IPAddr, pkt.Seq, pkt.Rtt)
	}

	pinger.OnFinish = func(stats *ping.Statistics) {
		fmt.Fprintf(&buf, "\n--- %s ping statistics ---\n", stats.Addr)
		fmt.Fprintf(&buf, "%d packets transmitted, %d packets received, %v%% packet loss\n",
			stats.PacketsSent, stats.PacketsRecv, stats.PacketLoss)
		fmt.Fprintf(&buf, "round-trip min/avg/max/stddev = %v/%v/%v/%v\n",
			stats.MinRtt, stats.AvgRtt, stats.MaxRtt, stats.StdDevRtt)
	}

	pinger.Count = 3
	pinger.Size = 548
	pinger.Interval = time.Second
	pinger.Timeout = 5 * time.Second
	pinger.SetPrivileged(true)

	err = pinger.Run()
	if err != nil {
		return ret, err
	}

	ret.Output = buf.String()

	stats := pinger.Statistics()

	if stats.PacketsRecv == stats.PacketsSent || stats.PacketLoss == 0 {
		ret.Status = "passing"
	} else {
		ret.Status = "failing"
	}

	return ret, nil
}

// PublicIP returns the agent's public ip
// Tries 3 times before giving up
func (a *Agent) PublicIP() string {
	a.Logger.Debugln("PublicIP start")
	client := resty.New()
	client.SetHeader("User-Agent", a.AgentHeader)
	client.SetTimeout(4 * time.Second)
	if len(a.Proxy) > 0 {
		client.SetProxy(a.Proxy)
	}
	urls := []string{"https://icanhazip.com", "https://ifconfig.co/ip"}
	ip := "error"

	for _, url := range urls {
		r, err := client.R().Get(url)
		if err != nil {
			a.Logger.Debugln("PublicIP err", err)
			continue
		}
		ip = StripAll(r.String())
		if !IsValidIP(ip) {
			a.Logger.Debugln("PublicIP not valid", ip)
			continue
		}
		v4 := net.ParseIP(ip)
		if v4.To4() == nil {
			r1, err := client.R().Get("https://ifconfig.me/ip")
			if err != nil {
				return ip
			}
			ipv4 := StripAll(r1.String())
			if !IsValidIP(ipv4) {
				continue
			}
			a.Logger.Debugln("Forcing ipv4:", ipv4)
			return ipv4
		}
		a.Logger.Debugln("PublicIP return: ", ip)
		break
	}

	// Fallback por DNS cuando el camino HTTPS no dejó nada usable. Ver
	// publicIPViaDNS: existe porque una red que inspecciona TLS rompe los tres
	// servicios de arriba sin romper el agente.
	if !IsValidIP(ip) {
		if dnsIP := a.publicIPViaDNS(); dnsIP != "" {
			return dnsIP
		}
	}
	return ip
}

// Presupuesto por consulta DNS. Corto a propósito: esto corre en el ciclo de
// check-in de la IP pública (CHECKIN_PUBIP), y ya viene detrás de los intentos
// HTTPS, así que el peor caso acumulado no debe volverse notorio.
const dnsPublicIPTimeout = 2 * time.Second

// Servicios DNS que responden con la IP de origen de la consulta. Dos, de
// operadores independientes, para poder cruzar el resultado.
//
// ⚠️ Se dialan por IP LITERAL a propósito: si hubiera que resolver primero el
// nombre del propio servidor, una red que rompe la resolución haría fallar el
// fallback justo en el escenario para el que existe.
var dnsPublicIPProviders = []struct {
	name   string
	lookup func(context.Context) (string, error)
}{
	{
		// myip.opendns.com devuelve un registro A con la IP de origen.
		name: "opendns",
		lookup: func(ctx context.Context) (string, error) {
			addrs, err := newFixedDNSResolver("208.67.222.222:53").LookupHost(ctx, "myip.opendns.com")
			if err != nil {
				return "", err
			}
			if len(addrs) == 0 {
				return "", errors.New("respuesta vacía de opendns")
			}
			return StripAll(addrs[0]), nil
		},
	},
	{
		// o-o.myaddr.l.google.com devuelve un TXT con la IP de origen. Va contra
		// ns1.google.com, que es autoritativo para ese nombre.
		name: "google",
		lookup: func(ctx context.Context) (string, error) {
			txts, err := newFixedDNSResolver("216.239.32.10:53").LookupTXT(ctx, "o-o.myaddr.l.google.com")
			if err != nil {
				return "", err
			}
			if len(txts) == 0 {
				return "", errors.New("respuesta vacía de google")
			}
			return StripAll(strings.Trim(txts[0], `"`)), nil
		},
	},
}

// newFixedDNSResolver arma un resolver que ignora /etc/resolv.conf y pregunta
// SIEMPRE al servidor indicado: la respuesta que buscamos ES la IP de origen que
// ve ese servidor, así que preguntarle al resolver del sistema —que suele ser el
// router de la LAN— devolvería una dirección privada o nada.
//
// PreferGo es obligatorio: sin él, en algunas plataformas se usa el resolver del
// sistema vía cgo y este Dial se ignora en silencio.
func newFixedDNSResolver(server string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: dnsPublicIPTimeout}
			return d.DialContext(ctx, network, server)
		},
	}
}

// publicIPViaDNS obtiene la IP pública por DNS en vez de HTTPS.
//
// POR QUÉ EXISTE (medido el 2026-08-02 en un Fedora 44 de la flota): un firewall
// Sophos con inspección SSL en el camino hace que los tres servicios HTTPS de
// PublicIP fallen con "self-signed certificate in certificate chain", porque su CA
// no está en el trust store del equipo. El agente entonces reportaba "error" y la
// consola lo mostraba como si el agente estuviera fallando. UDP/53 no lleva TLS,
// así que no hay cadena que validar y estas consultas pasan donde HTTPS no pasa.
//
// ⚠️ LO QUE ESTE VALOR SIGNIFICA, Y NO ES OBVIO: la respuesta es la IP de origen
// que ve el servidor DNS consultado, o sea la salida de la ruta que el sistema
// elija para llegar a ÉL. En un equipo con VPN de túnel parcial eso puede no ser
// la misma ruta que usa el tráfico general: en el Fedora medido, las rutas hacia
// los resolvers públicos las empuja la VPN, así que esto devuelve la salida de la
// VPN y no la del enlace local. Es correcto como "IP pública" y es mejor que
// "error", pero NO sirve para deducir en qué enlace físico está el equipo.
//
// Devuelve "" si ningún proveedor dejó una IP pública utilizable; el llamador
// conserva entonces el "error" de siempre.
func (a *Agent) publicIPViaDNS() string {
	var results []dnsPublicIPResult

	for _, p := range dnsPublicIPProviders {
		ctx, cancel := context.WithTimeout(context.Background(), dnsPublicIPTimeout)
		ip, err := p.lookup(ctx)
		cancel()

		if err != nil {
			a.Logger.Debugln("publicIPViaDNS", p.name, "err", err)
			continue
		}
		if !isUsablePublicIP(ip) {
			// Un resolver secuestrado, o el router de la LAN respondiendo por su
			// cuenta, devuelve algo que parece IP pero no lo es.
			a.Logger.Debugln("publicIPViaDNS", p.name, "descartada", ip)
			continue
		}
		results = append(results, dnsPublicIPResult{provider: p.name, ip: ip})
	}

	ip, agreed := pickDNSPublicIP(results)
	switch {
	case ip == "":
		a.Logger.Debugln("publicIPViaDNS: ningún proveedor respondió una IP utilizable")
	case !agreed && len(results) > 1:
		// No es necesariamente un ataque: con túnel parcial cada resolver puede
		// alcanzarse por una ruta distinta y las dos respuestas ser legítimas. Se
		// registra y se sigue con la primera en vez de descartar las dos.
		a.Logger.Debugln("publicIPViaDNS: proveedores discrepan", results, "se usa", ip)
	case !agreed:
		a.Logger.Debugln("publicIPViaDNS: sin verificación cruzada, un solo proveedor respondió:", ip)
	default:
		a.Logger.Debugln("publicIPViaDNS: verificación cruzada OK:", ip)
	}
	return ip
}

type dnsPublicIPResult struct {
	provider string
	ip       string
}

// pickDNSPublicIP decide con qué respuesta quedarse. Separada de la consulta para
// poder testear la decisión sin red. `agreed` es true sólo cuando dos proveedores
// independientes dijeron lo mismo.
func pickDNSPublicIP(results []dnsPublicIPResult) (ip string, agreed bool) {
	switch len(results) {
	case 0:
		return "", false
	case 1:
		return results[0].ip, false
	default:
		return results[0].ip, results[0].ip == results[1].ip
	}
}

// isUsablePublicIP exige IPv4 enrutable en Internet. Rechaza lo que no sirve para
// este campo aunque sea una IP válida: privadas (RFC1918), loopback, link-local,
// multicast y CGNAT (100.64/10, RFC6598 — es NAT del operador, no una dirección
// que el mundo vea). Se limita a IPv4 porque el resto del flujo de PublicIP ya
// prefiere IPv4, y porque estas consultas van a servidores IPv4.
func isUsablePublicIP(s string) bool {
	parsed := net.ParseIP(s)
	if parsed == nil {
		return false
	}
	v4 := parsed.To4()
	if v4 == nil {
		return false
	}
	if v4.IsLoopback() || v4.IsPrivate() || v4.IsLinkLocalUnicast() || v4.IsMulticast() || v4.IsUnspecified() {
		return false
	}
	// 100.64.0.0/10
	if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return false
	}
	return true
}

// GenerateAgentID creates and returns a unique agent id using crypto/rand for cryptographic entropy
func GenerateAgentID() string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, 40)
	for i := range b {
		n, err := crand.Int(crand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
		b[i] = letters[n.Int64()]
	}
	return string(b)
}

// ShowVersionInfo prints basic debugging info
func ShowVersionInfo(ver string) {
	fmt.Println("Observer RMM Agent:", ver)
	fmt.Println("Arch:", runtime.GOARCH)
	if runtime.GOOS == "windows" {
		fmt.Println("Program Directory:", filepath.Join(os.Getenv("ProgramFiles"), progFilesName))
	}
	bi, ok := goDebug.ReadBuildInfo()
	if ok {
		fmt.Println(bi.String())
	}
}

// TotalRAM returns total RAM in GB
func (a *Agent) TotalRAM() float64 {
	host, err := ps.Host()
	if err != nil {
		return 8.0
	}
	mem, err := host.Memory()
	if err != nil {
		return 8.0
	}
	return math.Ceil(float64(mem.Total) / 1073741824.0)
}

// BootTime returns system boot time as a unix timestamp
func (a *Agent) BootTime() int64 {
	host, err := ps.Host()
	if err != nil {
		return 1000
	}
	info := host.Info()
	return info.BootTime.Unix()
}

// IsValidIP checks for a valid ipv4 or ipv6
func IsValidIP(ip string) bool {
	return net.ParseIP(ip) != nil
}

// StripAll strips all whitespace and newline chars
func StripAll(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\n")
	s = strings.Trim(s, "\r")
	return s
}

// KillProc kills a process and its children
func KillProc(pid int32) error {
	p, err := process.NewProcess(pid)
	if err != nil {
		return err
	}

	children, err := p.Children()
	if err == nil {
		for _, child := range children {
			if err := child.Kill(); err != nil {
				continue
			}
		}
	}

	if err := p.Kill(); err != nil {
		return err
	}
	return nil
}

// DjangoStringResp removes double quotes from django rest api resp
func DjangoStringResp(resp string) string {
	return strings.Trim(resp, `"`)
}

func TestTCP(addr string) error {
	conn, err := net.Dial("tcp4", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	return nil
}

// CleanString removes invalid utf-8 byte sequences
func CleanString(s string) string {
	r := strings.NewReplacer("\x00", "")
	s = r.Replace(s)
	return strings.ToValidUTF8(s, "")
}

// https://golangcode.com/unzip-files-in-go/
func Unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {

		// Store filename/path for returning and using later on
		fpath := filepath.Join(dest, f.Name)

		// Check for ZipSlip. More Info: http://bit.ly/2MsjAWE
		if !strings.HasPrefix(fpath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("%s: illegal file path", fpath)
		}

		if f.FileInfo().IsDir() {
			// Make Folder
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		// Make File
		if err = os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		_, err = io.Copy(outFile, rc)

		// Close the file without defer to close before next iteration of loop
		outFile.Close()
		rc.Close()

		if err != nil {
			return err
		}
	}
	return nil
}

// ExtractTarGz extracts a tar.gz file to the specified directory.
// Returns the extracted directory name.
// https://stackoverflow.com/questions/57639648/how-to-decompress-tar-gz-file-in-go
func (a *Agent) ExtractTarGz(targz string, destDir string) (extractedDir string, err error) {
	gzipStream, err := os.Open(targz)
	if err != nil {
		a.Logger.Errorln("ExtractTarGz(): Open() failed:", err.Error())
		return "", err
	}
	defer gzipStream.Close()

	uncompressedStream, err := gzip.NewReader(gzipStream)
	if err != nil {
		a.Logger.Errorln("ExtractTarGz(): NewReader() failed:", err.Error())
		return "", err
	}
	defer uncompressedStream.Close()

	extractedDir = ""
	tarReader := tar.NewReader(uncompressedStream)
	for {
		header, err := tarReader.Next()

		if err == io.EOF {
			break
		}

		if err != nil {
			a.Logger.Errorln("ExtractTarGz(): Next() failed:", err.Error())
			return "", err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(filepath.Join(destDir, header.Name), 0755); err != nil {
				a.Logger.Errorln("ExtractTarGz(): Mkdir() failed:", err.Error())
				return "", err
			}
			if extractedDir == "" {
				extractedDir = header.Name
			}
		case tar.TypeReg:
			outFile, err := os.Create(filepath.Join(destDir, header.Name))
			if err != nil {
				a.Logger.Errorln("ExtractTarGz(): Create() failed:", err.Error())
				return "", err
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				a.Logger.Errorln("ExtractTarGz(): Copy() failed:", err.Error())
				return "", err
			}
			err = outFile.Close()
			if err != nil {
				a.Logger.Errorln("ExtractTarGz(): Close() failed:", err.Error())
				return "", err
			}

		default:
			errMsg := fmt.Sprintf("ExtractTarGz(): Unknown type: %v in %s", header.Typeflag, header.Name)
			a.Logger.Errorln(errMsg)
			return "", errors.New(errMsg)
		}

	}
	return extractedDir, nil
}

// https://yourbasic.org/golang/formatting-byte-size-to-human-readable-format/
func ByteCountSI(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB",
		float64(b)/float64(div), "kMGTPE"[exp])
}

func randRange(min, max int) int {
	return rand.Intn(max-min) + min
}

func randomCheckDelay() {
	time.Sleep(time.Duration(randRange(300, 950)) * time.Millisecond)
}

func removeWinNewLines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

func removeNewlines(s string) string {
	return strings.ReplaceAll(s, "\n", "")
}

func contains(s string, substrs []string) bool {
	for _, substr := range substrs {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func regRangeToInt(s string) int {
	split := strings.Split(s, ",")
	min, _ := strconv.Atoi(split[0])
	max, _ := strconv.Atoi(split[1])
	return randRange(min, max)
}

func getPowershellExe() string {
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil || powershell == "" {
		return filepath.Join(os.Getenv("WINDIR"), `System32\WindowsPowerShell\v1.0\powershell.exe`)
	}
	return powershell
}

func getCMDExe() string {
	cmdExe, err := exec.LookPath("cmd.exe")
	if err != nil || cmdExe == "" {
		return filepath.Join(os.Getenv("WINDIR"), `System32\cmd.exe`)
	}
	return cmdExe
}

// more accurate than os.Getwd()
func getCwd() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}

	return filepath.Dir(self), nil
}

func createNixTmpFile(path string, shell ...string) (*os.File, error) {
	var f *os.File

	if len(path) == 0 {
		var err error
		path, err = getCwd()
		if err != nil {
			return f, err
		}
	}

	// check if path exists
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			// create the path
			err := os.MkdirAll(path, 0755)
			if err != nil {
				return f, err
			}
		} else {
			return f, err
		}
	}

	ext := ""
	if len(shell) > 0 && shell[0] == "deno" {
		ext = ".ts"
	}

	f, err := os.CreateTemp(path, fmt.Sprintf("ormm*%s", ext))
	if err != nil {
		return f, err
	}

	return f, nil
}
