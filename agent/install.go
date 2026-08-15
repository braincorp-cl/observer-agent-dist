package agent

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	ormm "github.com/braincorp-cl/observer-agent-dist/shared"
	"github.com/go-resty/resty/v2"
)

type Installer struct {
	Headers          map[string]string
	RMM              string
	ClientID         int
	SiteID           int
	Description      string
	AgentType        string
	Power            bool
	RDP              bool
	Ping             bool
	Token            string
	LocalMesh        string
	Cert             string
	Proxy            string
	Timeout          time.Duration
	SaltMaster       string
	Silent           bool
	NoMesh           bool
	MeshDir          string
	MeshNodeID       string
	Insecure         bool
	NatsStandardPort string
	TmpDir           string
}

// logTLSChain registra la cadena de certificados TLS que presenta el server
// (subject / issuer / vigencia) para diagnosticar fallos de handshake, como el
// incidente de macOS (2026-07-06) donde la interceptación TLS del EDR rompía la
// cadena. Solo escribe logs; no altera la verificación.
func (a *Agent) logTLSChain(cs *tls.ConnectionState, ctx string) {
	if cs == nil {
		return
	}
	a.Logger.Infoln(fmt.Sprintf("%s: %d cert(s) en la cadena, verifiedChains=%d",
		ctx, len(cs.PeerCertificates), len(cs.VerifiedChains)))
	for idx, cert := range cs.PeerCertificates {
		a.Logger.Infoln(fmt.Sprintf("  [%d] subject=%q issuer=%q notAfter=%s",
			idx, cert.Subject.String(), cert.Issuer.String(),
			cert.NotAfter.Format(time.RFC3339)))
	}
}

// meshUninstallArgs son los argumentos del desinstalador local del Mesh.
//
// Vive en una función con test propio porque el ORDEN no es decorativo: el
// `--no-embedded=1` va DESPUÉS del comando. El binario que MeshCentral entrega
// trae un instalador JS anexado que se dispara ante CUALQUIER argumento, también
// ante `-fulluninstall` (medido en Linux el 2026-07-29: abre un diálogo en el
// escritorio y se cuelga esperando un clic que nadie va a dar). Invertirlos, o
// perder la bandera en una edición, deja el desinstalador colgado sin que nada
// falle a la vista.
func meshUninstallArgs() []string {
	return []string{"-fulluninstall", "--no-embedded=1"}
}

// meshUninstallShellCmd arma la misma invocación como línea de shell, que es lo
// que CmdV2 necesita en unix. Sin comillas, igual que checkExistingAndRemove: en
// unix la ruta del Mesh es fija y sin espacios (/usr/local/mesh_services/... en
// macOS, /opt/observermesh en Linux). En Windows NO se usa esta forma — ahí va
// por CMD con argv, que no pasa por ningún shell.
func meshUninstallShellCmd(exe string) string {
	return exe + " " + strings.Join(meshUninstallArgs(), " ")
}

// rollbackMeshInstall desinstala el Mesh que ESTA corrida acaba de instalar,
// cuando el enrolamiento en el RMM falla después.
//
// El orden del instalador es: instalar el Mesh —que al conectarse ya registra el
// nodo en MeshCentral— y recién después `POST /api/v3/newagent/`. Si ese POST
// falla, el proceso muere y el equipo queda con un agente Mesh VIVO, conectado a
// nuestro MeshCentral, que ninguna fila del RMM apunta: acceso remoto que nadie
// administra ni ve. Eso es lo que esto corta.
//
// ⚠️ LO QUE NO HACE, y hay que saberlo antes de creerle de más: NO borra el nodo
// del servidor. El `-fulluninstall` es LOCAL POR DISEÑO — verificado en la fuente
// de MeshAgent el 2026-08-09: `-fulluninstall` (agentcore.c:4858) desemboca en
// `modules/agent-installer.js`, que no importa una sola primitiva de red (sólo
// fs, child_process y los service managers) y cuyo único trato con el `.msh` —el
// archivo donde vive la URL del servidor— es borrarlo. El nodo queda en
// MeshCentral, ahora muerto, y lo levanta el censo de huérfanos del servidor.
// Acá se cierra el ACCESO, no el registro.
//
// Nunca puede tapar el error del enrolamiento: si el rollback falla se registra y
// se sigue de largo, para que el usuario vea el error de verdad.
func (a *Agent) rollbackMeshInstall(installedEXE, meshNodeID string) {
	if installedEXE == "" {
		return
	}

	a.Logger.Errorln(
		"El enrolamiento falló después de instalar el Mesh; se desinstala para no",
		"dejar un agente Mesh sin dueño. Nodo:", meshNodeID,
	)

	switch runtime.GOOS {
	case "windows":
		if _, err := CMD(installedEXE, meshUninstallArgs(), 60, false); err != nil {
			a.Logger.Errorln("rollbackMeshInstall():", err)
		}
	default:
		// En unix `CMD` es un stub que no ejecuta nada (agent_unix.go:1012), así
		// que acá va por CmdV2, igual que checkExistingAndRemove.
		opts := a.NewCMDOpts()
		opts.Command = meshUninstallShellCmd(installedEXE)
		out := a.CmdV2(opts)
		if out.Status.Exit != 0 {
			a.Logger.Errorln("rollbackMeshInstall():", out.Stderr)
		}
	}
}

func (a *Agent) Install(i *Installer) {
	i.Headers = map[string]string{
		"content-type":  "application/json",
		"Authorization": fmt.Sprintf("Token %s", i.Token),
	}
	a.AgentID = GenerateAgentID()
	a.Logger.Debugln("Agent ID:", a.AgentID)

	u, err := url.Parse(i.RMM)
	if err != nil {
		a.installerMsg(err.Error(), "error", i.Silent)
	}

	if u.Scheme != "https" && u.Scheme != "http" {
		a.installerMsg("Invalid URL (must contain https or http)", "error", i.Silent)
	}

	// will match either ipv4 , or ipv4:port
	var ipPort = regexp.MustCompile(`[0-9]+(?:\.[0-9]+){3}(:[0-9]+)?`)

	// if ipv4:port, strip the port to get ip for salt master
	if ipPort.MatchString(u.Host) && strings.Contains(u.Host, ":") {
		i.SaltMaster = strings.Split(u.Host, ":")[0]
	} else if strings.Contains(u.Host, ":") {
		i.SaltMaster = strings.Split(u.Host, ":")[0]
	} else {
		i.SaltMaster = u.Host
	}

	a.Logger.Debugln("API:", i.SaltMaster)

	baseURL := u.Scheme + "://" + u.Host
	a.Logger.Debugln("Base URL:", baseURL)

	iClient := resty.New()
	iClient.SetCloseConnection(true)
	iClient.SetTimeout(15 * time.Second)
	iClient.SetDebug(a.Debug)
	iClient.SetHeaders(i.Headers)

	// set proxy if applicable
	if len(i.Proxy) > 0 {
		a.Logger.Infoln("Using proxy:", i.Proxy)
		iClient.SetProxy(i.Proxy)
	}

	insecureConf := &tls.Config{
		InsecureSkipVerify: true,
		// Aun en modo -insecure, logueamos la cadena que presenta el server para
		// diagnóstico. Devolver nil NO debilita nada: en -insecure la verificación
		// ya se omite; este callback solo observa.
		VerifyConnection: func(cs tls.ConnectionState) error {
			a.logTLSChain(&cs, "handshake TLS (installer)")
			return nil
		},
	}

	if i.Insecure {
		iClient.SetTLSClientConfig(insecureConf)
	}

	creds, cerr := iClient.R().Get(fmt.Sprintf("%s/api/v3/installer/", baseURL))
	if cerr != nil {
		a.installerMsg(cerr.Error(), "error", i.Silent)
	}
	if creds.StatusCode() == 401 {
		a.installerMsg("Installer token has expired. Please generate a new one.", "error", i.Silent)
	}
	// Registra la cadena presentada en la conexión exitosa (todos los modos).
	if creds.RawResponse != nil && creds.RawResponse.TLS != nil {
		a.logTLSChain(creds.RawResponse.TLS, "respuesta TLS (installer)")
	}

	verPayload := map[string]string{"version": a.Version}
	iVersion, ierr := iClient.R().SetBody(verPayload).Post(fmt.Sprintf("%s/api/v3/installer/", baseURL))
	if ierr != nil {
		a.installerMsg(ierr.Error(), "error", i.Silent)
	}
	if iVersion.StatusCode() != 200 {
		a.installerMsg(DjangoStringResp(iVersion.String()), "error", i.Silent)
	}

	// checkExistingAndRemove borra la instalación existente (en macOS hace
	// -fulluninstall del mesh + borra dirs; en Windows aborta pidiendo desinstalar).
	// Se ejecuta AQUÍ, sólo DESPUÉS de confirmar que el server responde y que el
	// endpoint /installer/ devolvió 200, para no dejar la máquina sin agente si el
	// server es inalcanzable (p.ej. TLS roto, incidente macOS 2026-07-06). Antes
	// corría al inicio de Install(), antes de cualquier chequeo del server.
	a.checkExistingAndRemove(i.Silent)

	rClient := resty.New()
	rClient.SetCloseConnection(true)
	rClient.SetTimeout(i.Timeout * time.Second)
	rClient.SetDebug(a.Debug)
	// set rest knox headers
	rClient.SetHeaders(i.Headers)

	// set local cert if applicable
	if len(i.Cert) > 0 {
		if !ormm.FileExists(i.Cert) {
			a.installerMsg(fmt.Sprintf("%s does not exist", i.Cert), "error", i.Silent)
		}
		rClient.SetRootCertificate(i.Cert)
	}

	if len(i.Proxy) > 0 {
		rClient.SetProxy(i.Proxy)
	}

	if i.Insecure {
		rClient.SetTLSClientConfig(insecureConf)
	}

	var installerMeshSystemEXE string
	if len(i.MeshDir) > 0 {
		installerMeshSystemEXE = filepath.Join(i.MeshDir, "MeshAgent.exe")
	} else {
		installerMeshSystemEXE = a.MeshSystemEXE
	}

	var meshNodeID, meshOutput string

	// Binario del Mesh que instaló ESTA corrida, para poder deshacerlo si el
	// enrolamiento falla más abajo. Vacío = no instalamos nada acá (-nomesh,
	// Linux, o el Mesh ya venía puesto) y entonces no hay nada que deshacer:
	// sólo se revierte lo que uno mismo hizo.
	var installedMeshEXE string

	if !i.NoMesh && runtime.GOOS != "linux" {
		switch runtime.GOOS {
		case "windows":
			meshOutput = filepath.Join(a.ProgramDir, a.MeshInstaller)
		case "darwin":
			tmp, err := createNixTmpFile(a.UnixTmpDir)
			if err != nil {
				a.Logger.Fatalln("Failed to create mesh temp file", err)
			}
			meshOutput = tmp.Name()
			os.Chmod(meshOutput, 0755)
			defer os.Remove(meshOutput)
			defer os.Remove(meshOutput + ".msh")
		}

		if runtime.GOOS == "windows" && i.LocalMesh != "" {
			err := copyFile(i.LocalMesh, meshOutput)
			if err != nil {
				a.installerMsg(err.Error(), "error", i.Silent)
			}
		} else {
			a.Logger.Infoln("Downloading mesh agent...")
			payload := map[string]string{"goarch": a.GoArch, "plat": a.Platform}
			r, err := rClient.R().SetBody(payload).SetOutput(meshOutput).Post(fmt.Sprintf("%s/api/v3/meshexe/", baseURL))
			if err != nil {
				a.installerMsg(fmt.Sprintf("Failed to download mesh agent: %s", err.Error()), "error", i.Silent)
			}
			if r.StatusCode() != 200 {
				a.installerMsg(fmt.Sprintf("Unable to download the mesh agent from the RMM. %s", r.String()), "error", i.Silent)
			}
		}

		a.Logger.Infoln("Installing mesh agent...")
		a.Logger.Debugln("Mesh agent:", meshOutput)
		time.Sleep(1 * time.Second)

		if runtime.GOOS == "windows" {
			meshNodeID, err = a.installMesh(meshOutput, installerMeshSystemEXE, i.Proxy)
			if err != nil {
				a.installerMsg(fmt.Sprintf("Failed to install mesh agent: %s", err.Error()), "error", i.Silent)
			}
			installedMeshEXE = installerMeshSystemEXE
		} else {
			opts := a.NewCMDOpts()
			opts.Command = fmt.Sprintf("%s -install --installPath=%s", meshOutput, nixMeshDir)
			opts.Timeout = i.Timeout
			out := a.CmdV2(opts)
			if out.Status.Exit != 0 {
				a.Logger.Fatalln("Error installing mesh agent:", out.Stderr)
			}
			fmt.Println(out.Stdout)
			installedMeshEXE = a.MeshSystemEXE
		}
	}

	if len(i.MeshNodeID) > 0 {
		meshNodeID = i.MeshNodeID
	}

	a.Logger.Infoln("Adding agent to dashboard")
	// add agent
	type NewAgentResp struct {
		AgentPK int    `json:"pk"`
		Token   string `json:"token"`
	}
	agentPayload := map[string]interface{}{
		"agent_id":        a.AgentID,
		"hostname":        a.Hostname,
		"site":            i.SiteID,
		"monitoring_type": i.AgentType,
		"mesh_node_id":    meshNodeID,
		"description":     i.Description,
		"goarch":          a.GoArch,
		"plat":            a.Platform,
	}

	// Los dos rollbacks van ANTES del installerMsg de error: ese mensaje es
	// FATAL (Logger.Fatalln en las dos plataformas, install_unix.go:19 e
	// install_windows.go:138), así que cualquier limpieza puesta después no
	// llega a correr nunca.
	r, err := rClient.R().SetBody(agentPayload).SetResult(&NewAgentResp{}).Post(fmt.Sprintf("%s/api/v3/newagent/", baseURL))
	if err != nil {
		a.rollbackMeshInstall(installedMeshEXE, meshNodeID)
		a.installerMsg(err.Error(), "error", i.Silent)
	}
	if r.StatusCode() != 200 {
		a.rollbackMeshInstall(installedMeshEXE, meshNodeID)
		a.installerMsg(r.String(), "error", i.Silent)
	}

	agentPK := r.Result().(*NewAgentResp).AgentPK
	agentToken := r.Result().(*NewAgentResp).Token

	a.Logger.Debugln("Agent token:", agentToken)
	a.Logger.Debugln("Agent PK:", agentPK)

	createAgentConfig(baseURL, a.AgentID, i.SaltMaster, agentToken, strconv.Itoa(agentPK), i.Cert, i.Proxy, i.MeshDir, i.NatsStandardPort, i.Insecure, i.TmpDir)
	time.Sleep(1 * time.Second)
	// refresh our agent with new values
	a = New(a.Logger, a.Version)
	a.Logger.Debugf("%+v\n", a)

	// set new headers, no longer knox auth...use agent auth
	rClient.SetHeaders(a.Headers)

	time.Sleep(3 * time.Second)
	// check in once
	a.DoNatsCheckIn()

	if runtime.GOOS == "linux" {
		// Used for Nushell and Deno binaries
		os.MkdirAll(nixAgentBinDir, 0755)
	}

	if runtime.GOOS == "darwin" {
		os.MkdirAll(nixAgentBinDir, 0755)
		self, _ := os.Executable()
		copyFile(self, nixAgentBin)
		os.Chmod(nixAgentBin, 0755)
		svc := fmt.Sprintf(`
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple Computer//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
	<dict>
		<key>Label</key>
		<string>%s</string>

		<key>ServiceDescription</key>
        <string>Observer RMM Agent Service</string>

		<key>ProgramArguments</key>
		<array>
			<string>%s</string>
			<string>-m</string>
			<string>svc</string>
		</array>

		<key>WorkingDirectory</key>
		<string>%s/</string>

		<key>RunAtLoad</key>
		<true/>

		<key>KeepAlive</key>
		<true/>
	</dict>
</plist>
`, macPlistName, nixAgentBin, nixAgentDir)

		os.WriteFile(macPlistPath, []byte(svc), 0644)

		// Feature 030 · T016: el ayudante que saca la foto y la captura dentro de
		// la sesión gráfica. Va como LaunchAgent, y como BUNDLE con identificador
		// propio, por dos motivos distintos: la cámara de macOS sólo entrega
		// cuadros en el contexto Aqua, y un permiso concedido a un ejecutable
		// suelto no se puede reparar cuando el update lo rompe. Todo el porqué
		// está en agent/capturaaqua.go.
		//
		// Acá sólo se deja el plist: el bundle lo arma el servicio, porque tiene
		// que rehacerse en cada actualización del binario y no sólo al instalar.
		os.MkdirAll(capturaAquaDir, 0755)
		os.WriteFile(capturaAquaPlist, []byte(capturaAquaPlistContenido()), 0644)

		// Feature 030 · T016b: la marca hace que el servicio pida los permisos
		// de Cámara y Grabación de pantalla en su primer arranque, que es
		// AHORA — con la persona que está instalando todavía frente al equipo.
		//
		// Se deja una marca en vez de pedirlos acá mismo porque el instalador
		// corre desde Terminal.app y macOS le atribuye el permiso al
		// `responsible process`: la fila nacería a nombre del terminal (que
		// además suele tener cámara concedida, con lo que ni siquiera saldría
		// un diálogo) y el agente se quedaría sin nada. El permiso tiene que
		// pedirlo el servicio para que quede a nombre del servicio.
		a.marcarPedidoDePermisosTCC()

		opts := a.NewCMDOpts()
		opts.Command = fmt.Sprintf("launchctl bootstrap system %s", macPlistPath)
		a.CmdV2(opts)
	}

	if runtime.GOOS == "windows" {
		// Deja grabado dónde quedó instalado el agente, para los procesos que no
		// corren desde el directorio de instalación (check-in, taskrunner,
		// scripts, diagnóstico). La clave ya existe: la creó createAgentConfig
		// unas líneas más arriba.
		if err := persistInstallDir(); err != nil {
			a.Logger.Debugln("Install() persistInstallDir:", err)
		}

		os.MkdirAll(filepath.Join(a.ProgramDir, "bin"), 0755)

		// When installed from the raw release binary (PowerShell/manual method)
		// the agent is not yet present at the service path, so the auto-start
		// service would point at a missing file. Copy ourselves there before
		// installing the service. Mirrors the macOS branch above; no-op when we
		// are already running from the install location (Inno Setup flow).
		if self, exeErr := os.Executable(); exeErr == nil {
			dst := filepath.Join(a.ProgramDir, winExeName)
			if !strings.EqualFold(self, dst) {
				if cerr := copyFile(self, dst); cerr != nil {
					a.Logger.Errorln("Install() copy agent to ProgramDir:", cerr)
				}
			}
		}

		// send software api
		a.SendSoftware()

		a.Logger.Debugln("Creating temp dir")
		err := createWinTempDir()
		if err != nil {
			a.Logger.Errorln("Install() createWinTempDir():", err)
		}

		a.Logger.Debugln("Disabling automatic windows updates")
		a.PatchMgmnt(true)

		a.Logger.Infoln("Installing service...")
		err = a.InstallService()
		if err != nil {
			a.installerMsg(err.Error(), "error", i.Silent)
		}

		time.Sleep(1 * time.Second)
		a.Logger.Infoln("Starting service...")
		out := a.ControlService(winSvcName, "start")
		if !out.Success {
			a.installerMsg(out.ErrorMsg, "error", i.Silent)
		}

		if i.Power {
			a.Logger.Infoln("Disabling sleep/hibernate...")
			DisableSleepHibernate()
		}

		if i.Ping {
			a.Logger.Infoln("Enabling ping...")
			EnablePing()
		}

		if i.RDP {
			a.Logger.Infoln("Enabling RDP...")
			EnableRDP()
		}
	}

	a.installerMsg("Installation was successful!\nAllow a few minutes for the agent to properly display in the RMM", "info", i.Silent)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}
	return nil
}
