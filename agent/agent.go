package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"time"

	rmm "github.com/braincorp-cl/observer-agent-dist/shared"
	ps "github.com/elastic/go-sysinfo"
	gocmd "github.com/go-cmd/cmd"
	"github.com/go-resty/resty/v2"
	"github.com/kardianos/service"
	nats "github.com/nats-io/nats.go"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/sirupsen/logrus"
	"github.com/ugorji/go/codec"
)

// Agent struct
type Agent struct {
	Hostname   string
	Arch       string
	AgentID    string
	BaseURL    string
	ApiURL     string
	Token      string
	AgentPK    int
	Cert       string
	ProgramDir string
	// Feature 030 · Fase 2: interruptor global de la foto de webcam, refrescado
	// desde la config del servidor. En memoria y nunca en disco (ver svc.go).
	LostModeWebcam     bool
	EXE                string
	SystemDrive        string
	WinTmpDir          string
	UnixTmpDir         string
	WinRunAsUserTmpDir string
	MeshInstaller      string
	MeshSystemEXE      string
	MeshSVC            string
	PyBin              string
	PyVer              string
	PyBaseDir          string
	PyDir              string
	NuBin              string
	DenoBin            string
	AgentHeader        string
	Headers            map[string]string
	Logger             *logrus.Logger
	Version            string
	Debug              bool
	rClient            *resty.Client
	Proxy              string
	LogTo              string
	LogFile            *os.File
	Platform           string
	GoArch             string
	ServiceConfig      *service.Config
	NatsServer         string
	NatsProxyPath      string
	NatsProxyPort      string
	NatsPingInterval   int
	NatsWSCompression  bool
	Insecure           bool
	// Feature 023 · gap 3: force-on del sensor de ubicación/radio WiFi. Se setea
	// desde la config de check-in (geo_force_on) al arrancar el servicio; lo lee
	// osLocation antes de escanear. Activo corporativo: no hay opt-in del usuario.
	GeoForceOn bool
}

const (
	progFilesName        = "ObserverAgent"
	winExeName           = "observeragent.exe"
	winSvcName           = "observeragent"
	meshSvcName          = "mesh agent"
	etcConfig            = "/etc/observeragent"
	nixAgentDir          = "/opt/observeragent"
	nixMeshDir           = "/opt/observermesh"
	nixAgentBin          = nixAgentDir + "/observeragent"
	nixAgentBinDir       = nixAgentDir + "/bin"
	nixAgentEtcDir       = nixAgentDir + "/etc"
	nixMeshAgentBin      = nixMeshDir + "/meshagent"
	macPlistPath         = "/Library/LaunchDaemons/observeragent.plist"
	macPlistName         = "observeragent"
	defaultMacMeshSvcDir = "/usr/local/mesh_services"
)

var defaultWinTmpDir = filepath.Join(os.Getenv("PROGRAMDATA"), "ObserverRMM")
var natsCheckin = []string{"agent-hello", "agent-agentinfo", "agent-disks", "agent-winsvc", "agent-publicip", "agent-wmi"}
var limitNatsData = []string{"agent-winsvc", "agent-wmi"}

func New(logger *logrus.Logger, version string) *Agent {
	host, _ := ps.Host()
	info := host.Info()
	// La ubicación instalada manda. Deducirla de %ProgramFiles% es justamente lo
	// que rompía al agente de 32 bits sobre x64: ver ResolveProgramDir.
	pd := ResolveProgramDir()
	exe := filepath.Join(pd, winExeName)
	sd := os.Getenv("SystemDrive")
	winTempDir := defaultWinTmpDir
	winRunAsUserTmpDir := defaultWinTmpDir

	hostname, err := os.Hostname()
	if err != nil {
		hostname = info.Hostname
	}

	pyver := "n/a"
	pybin := "n/a"
	pyBaseDir := "n/a"
	pydir := "n/a"

	if runtime.GOOS == "windows" {
		major := info.OS.Major
		minor := info.OS.Minor
		if major > 6 || (major == 6 && minor >= 3) {
			// Windows 8.1 or higher
			pyver = "3.11.9"
		} else {
			pyver = "3.8.7"
		}

		pydir = "py" + pyver + "_" + runtime.GOARCH
		pyBaseDir = filepath.Join(pd, "python")
		pybin = filepath.Join(pyBaseDir, pydir, "python.exe")
	}

	var nuBin string
	switch runtime.GOOS {
	case "windows":
		nuBin = filepath.Join(pd, "bin", "nu.exe")
	default:
		nuBin = filepath.Join(nixAgentBinDir, "nu")
	}

	var denoBin string
	switch runtime.GOOS {
	case "windows":
		denoBin = filepath.Join(pd, "bin", "deno.exe")
	default:
		denoBin = filepath.Join(nixAgentBinDir, "deno")
	}

	ac := NewAgentConfig()

	agentHeader := fmt.Sprintf("ormm/%s/%s/%s", version, runtime.GOOS, runtime.GOARCH)
	headers := make(map[string]string)
	if len(ac.Token) > 0 {
		headers["Content-Type"] = "application/json"
		headers["Authorization"] = fmt.Sprintf("Token %s", ac.Token)
	}

	insecure := ac.Insecure == "true"

	restyC := resty.New()
	restyC.SetBaseURL(ac.BaseURL)
	restyC.SetCloseConnection(true)
	restyC.SetHeaders(headers)
	restyC.SetTimeout(15 * time.Second)
	restyC.SetDebug(logger.IsLevelEnabled(logrus.DebugLevel))
	if insecure {
		insecureConf := &tls.Config{
			InsecureSkipVerify: true,
		}
		restyC.SetTLSClientConfig(insecureConf)
	}

	if len(ac.Proxy) > 0 {
		restyC.SetProxy(ac.Proxy)
	}
	if len(ac.Cert) > 0 {
		restyC.SetRootCertificate(ac.Cert)
	}

	if len(ac.WinTmpDir) > 0 {
		winTempDir = ac.WinTmpDir
	}

	if len(ac.WinRunAsUserTmpDir) > 0 {
		winRunAsUserTmpDir = ac.WinRunAsUserTmpDir
	}

	var MeshSysExe string
	switch runtime.GOOS {
	case "windows":
		if len(ac.CustomMeshDir) > 0 {
			MeshSysExe = filepath.Join(ac.CustomMeshDir, "MeshAgent.exe")
		} else {
			// %ProgramFiles% acá es DELIBERADO y no debe cambiarse por
			// ResolveProgramDir, aunque el resto del archivo haya dejado de
			// usarlo.
			//
			// Son dos acoplamientos distintos. El directorio del agente lo fija
			// nuestro instalador, y por eso hay que preguntárselo a él. El del
			// Mesh Agent lo fija el instalador de MeshCentral, que se ubica solo
			// según su propio bitness: el meshagent de 32 bits se instala en la
			// vista de 32 bits. Sobre un x64 eso es `C:\Program Files (x86)\Mesh
			// Agent`, que es justo lo que devuelve esta línea dentro de un
			// proceso de 32 bits. Está medido en terreno, no supuesto.
			//
			// Hacer que esta ruta siguiera al agente la mandaría a `C:\Program
			// Files\Mesh Agent`, donde no hay nada: se perdería el node id, el
			// `-fulluninstall` y la recuperación del mesh.
			MeshSysExe = filepath.Join(os.Getenv("ProgramFiles"), "Mesh Agent", "MeshAgent.exe")
		}
	case "darwin":
		if rmm.FileExists(nixMeshAgentBin) {
			MeshSysExe = nixMeshAgentBin
		} else {
			MeshSysExe = "/usr/local/mesh_services/meshagent/meshagent"
		}
	default:
		MeshSysExe = nixMeshAgentBin
	}

	svcConf := &service.Config{
		Executable:  exe,
		Name:        winSvcName,
		DisplayName: "Observer RMM Agent Service",
		Arguments:   []string{"-m", "svc"},
		Description: "Observer RMM Agent Service",
		Option: service.KeyValue{
			"StartType":              "automatic",
			"OnFailure":              "restart",
			"OnFailureDelayDuration": "12s",
			"OnFailureResetPeriod":   10,
		},
	}

	var natsProxyPath, natsProxyPort string
	if ac.NatsProxyPath == "" {
		natsProxyPath = "natsws"
	}

	if ac.NatsProxyPort == "" {
		natsProxyPort = "443"
	}

	// check if using nats standard tcp, otherwise use nats websockets by default
	var natsServer string
	var natsWsCompression bool
	if ac.NatsStandardPort != "" {
		natsServer = fmt.Sprintf("tls://%s:%s", ac.APIURL, ac.NatsStandardPort)
	} else {
		natsServer = fmt.Sprintf("wss://%s:%s", ac.APIURL, natsProxyPort)
		natsWsCompression = true
	}

	var natsPingInterval int
	if ac.NatsPingInterval == 0 {
		natsPingInterval = randRange(35, 45)
	} else {
		natsPingInterval = ac.NatsPingInterval
	}

	return &Agent{
		Hostname:           hostname,
		BaseURL:            ac.BaseURL,
		AgentID:            ac.AgentID,
		ApiURL:             ac.APIURL,
		Token:              ac.Token,
		AgentPK:            ac.PK,
		Cert:               ac.Cert,
		ProgramDir:         pd,
		EXE:                exe,
		SystemDrive:        sd,
		WinTmpDir:          winTempDir,
		WinRunAsUserTmpDir: winRunAsUserTmpDir,
		UnixTmpDir:         ac.UnixTmpDir,
		MeshInstaller:      "meshagent.exe",
		MeshSystemEXE:      MeshSysExe,
		MeshSVC:            meshSvcName,
		PyBin:              pybin,
		PyVer:              pyver,
		PyBaseDir:          pyBaseDir,
		PyDir:              pydir,
		NuBin:              nuBin,
		DenoBin:            denoBin,
		Headers:            headers,
		AgentHeader:        agentHeader,
		Logger:             logger,
		Version:            version,
		Debug:              logger.IsLevelEnabled(logrus.DebugLevel),
		rClient:            restyC,
		Proxy:              ac.Proxy,
		Platform:           runtime.GOOS,
		GoArch:             runtime.GOARCH,
		ServiceConfig:      svcConf,
		NatsServer:         natsServer,
		NatsProxyPath:      natsProxyPath,
		NatsProxyPort:      natsProxyPort,
		NatsPingInterval:   natsPingInterval,
		NatsWSCompression:  natsWsCompression,
		Insecure:           insecure,
	}
}

type CmdStatus struct {
	Status gocmd.Status
	Stdout string
	Stderr string
}

type CmdOptions struct {
	Shell        string
	Command      string
	Args         []string
	Timeout      time.Duration
	IsScript     bool
	IsExecutable bool
	Detached     bool
	EnvVars      []string
	Stream       bool
	Nc           *nats.Conn // nats connection
	CmdID        string
}

func (a *Agent) NewCMDOpts() *CmdOptions {
	return &CmdOptions{
		Shell:   "/bin/bash",
		Timeout: 60,
	}
}

func (a *Agent) CmdV2(c *CmdOptions) CmdStatus {

	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout*time.Second)
	defer cancel()

	// Disable output buffering, enable streaming
	cmdOptions := gocmd.Options{
		Buffered:  false,
		Streaming: true,
	}

	// have a child process that is in a different process group so that
	// parent terminating doesn't kill child
	if c.Detached {
		cmdOptions.BeforeExec = append(cmdOptions.BeforeExec, func(cmd *exec.Cmd) {
			cmd.SysProcAttr = SetDetached()
		})
	}

	if len(c.EnvVars) > 0 {
		cmdOptions.BeforeExec = append(cmdOptions.BeforeExec, func(cmd *exec.Cmd) {
			cmd.Env = os.Environ()
			cmd.Env = append(cmd.Env, c.EnvVars...)
		})
	}

	var envCmd *gocmd.Cmd
	if c.IsScript {
		envCmd = gocmd.NewCmdOptions(cmdOptions, c.Shell, c.Args...) // call script directly
	} else if c.IsExecutable {
		// c.Shell: binario; c.Command: primer argumento; c.Args: los siguientes.
		//
		// Los argumentos van como elementos separados y NO concatenados en una
		// sola cadena porque acá no hay shell que divida por espacios: exec
		// entrega cada elemento tal cual, así que "-a --b=1" llegaría como UN
		// argumento con un espacio adentro y el binario lo rechazaría.
		execArgs := append([]string{c.Command}, c.Args...)
		envCmd = gocmd.NewCmdOptions(cmdOptions, c.Shell, execArgs...)
	} else {
		commandArray := append(strings.Fields(c.Shell), "-c", c.Command)
		envCmd = gocmd.NewCmdOptions(cmdOptions, commandArray[0], commandArray[1:]...) // /bin/bash -c 'ls -l /var/log/...'
	}

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	// Print STDOUT and STDERR lines streaming from Cmd
	doneChan := make(chan struct{})
	go func() {
		defer close(doneChan)
		// Done when both channels have been closed
		// https://dave.cheney.net/2013/04/30/curious-channels
		for envCmd.Stdout != nil || envCmd.Stderr != nil {
			select {
			case line, open := <-envCmd.Stdout:
				if !open {
					envCmd.Stdout = nil
					continue
				}
				fmt.Fprintln(&stdoutBuf, line)
				a.Logger.Debugln(line)
				if c.Stream {
					streamLineToNats(line, a.AgentID, c.CmdID, c.Nc)
				}

			case line, open := <-envCmd.Stderr:
				if !open {
					envCmd.Stderr = nil
					continue
				}
				fmt.Fprintln(&stderrBuf, line)
				a.Logger.Debugln(line)
				if c.Stream {
					streamLineToNats(line, a.AgentID, c.CmdID, c.Nc)
				}
			}
		}
	}()

	statusChan := make(chan gocmd.Status, 1)
	// workaround for https://github.com/golang/go/issues/22315
	go func() {
		for i := 0; i < 5; i++ {
			finalStatus := <-envCmd.Start()
			if errors.Is(finalStatus.Error, syscall.ETXTBSY) {
				a.Logger.Errorln("CmdV2 syscall.ETXTBSY, retrying...")
				time.Sleep(500 * time.Millisecond)
				continue
			}
			statusChan <- finalStatus
			return
		}
	}()

	var finalStatus gocmd.Status

	select {
	case <-ctx.Done():
		a.Logger.Debugf("Command timed out after %d seconds\n", c.Timeout)
		pid := envCmd.Status().PID
		a.Logger.Debugln("Killing process with PID", pid)
		KillProc(int32(pid))
		finalStatus.Exit = 98
		ret := CmdStatus{
			Status: finalStatus,
			Stdout: DecodeCmdOutput(stdoutBuf.Bytes()),
			Stderr: fmt.Sprintf("%s\nTimed out after %d seconds", DecodeCmdOutput(stderrBuf.Bytes()), c.Timeout),
		}
		a.Logger.Debugf("%+v\n", ret)
		return ret
	case finalStatus = <-statusChan:
		// done
	}

	// Wait for goroutine to print everything
	<-doneChan

	ret := CmdStatus{
		Status: finalStatus,
		Stdout: DecodeCmdOutput(stdoutBuf.Bytes()),
		Stderr: DecodeCmdOutput(stderrBuf.Bytes()),
	}
	a.Logger.Debugf("%+v\n", ret)

	if c.Stream {
		finalPayload := map[string]interface{}{
			"done":      true,
			"exit_code": finalStatus.Exit,
		}
		var finalResp []byte
		retEnc := codec.NewEncoderBytes(&finalResp, new(codec.MsgpackHandle))
		_ = retEnc.Encode(finalPayload)
		subject := a.AgentID + ".cmdoutput." + c.CmdID
		_ = c.Nc.Publish(subject, finalResp)
	}

	return ret
}

func streamLineToNats(line string, agentID string, cmdID string, nc *nats.Conn) {
	var resp []byte
	ret := codec.NewEncoderBytes(&resp, new(codec.MsgpackHandle))
	_ = ret.Encode(line)
	subject := agentID + ".cmdoutput." + cmdID
	_ = nc.Publish(subject, resp)
}

func (a *Agent) GetCPULoadAvg() int {
	fallback := false
	pyCode := `
import psutil
try:
	print(int(round(psutil.cpu_percent(interval=10))), end='')
except:
	print("pyerror", end='')
`
	pypercent, err := a.RunPythonCode(pyCode, 13, []string{})
	if err != nil || pypercent == "pyerror" {
		fallback = true
	}

	i, err := strconv.Atoi(pypercent)
	if err != nil {
		fallback = true
	}

	if fallback {
		percent, err := cpu.Percent(10*time.Second, false)
		if err != nil {
			a.Logger.Debugln("Go CPU Check:", err)
			return 0
		}
		return int(math.Round(percent[0]))
	}
	return i
}

// ForceKillMesh kills all mesh agent related processes
func (a *Agent) ForceKillMesh() {
	pids := make([]int, 0)

	procs, err := ps.Processes()
	if err != nil {
		return
	}

	for _, process := range procs {
		p, err := process.Info()
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(p.Name), "meshagent") {
			pids = append(pids, p.PID)
		}
	}

	for _, pid := range pids {
		a.Logger.Debugln("Killing mesh process with pid:", pid)
		if err := KillProc(int32(pid)); err != nil {
			a.Logger.Debugln(err)
		}
	}
}

func (a *Agent) SyncMeshNodeID() {

	id, err := a.getMeshNodeID()
	if err != nil {
		a.Logger.Errorln("SyncMeshNodeID() getMeshNodeID()", err)
		return
	}

	payload := rmm.MeshNodeID{
		Func:    "syncmesh",
		Agentid: a.AgentID,
		NodeID:  StripAll(id),
	}

	_, err = a.rClient.R().SetBody(payload).Post("/api/v3/syncmesh/")
	if err != nil {
		a.Logger.Debugln("SyncMesh:", err)
	}
}

// ReinstallMesh descarga el instalador Mesh cacheado que sirve el servidor
// (endpoint /meshreinstall/, backport v1.5.1 C1+B1) y reinstala el servicio
// Mesh durante la auto-reparación: desinstala, limpia el directorio y vuelve
// a instalar. Windows-only por ahora (el servidor solo cachea el instalador
// Windows); en el resto de plataformas retorna sin efecto.
func (a *Agent) ReinstallMesh() {
	if runtime.GOOS != "windows" {
		return
	}
	meshOutput := filepath.Join(a.ProgramDir, a.MeshInstaller)
	url := fmt.Sprintf("/api/v3/%s/meshreinstall/", a.AgentID)
	r, err := a.rClient.R().SetOutput(meshOutput).Get(url)
	if err != nil {
		a.Logger.Errorln("ReinstallMesh() download:", err)
		return
	}
	if r.IsError() {
		a.Logger.Errorln("ReinstallMesh() status code:", r.StatusCode())
		return
	}
	// `--no-embedded=1` va DESPUÉS del comando y acá es especialmente
	// necesario: `meshOutput` es el instalador que ACABAMOS de bajar del
	// servidor, o sea el auto-instalador puro, el que sí trae el script JS
	// anexado. Ese script se dispara ante CUALQUIER argumento, también ante
	// `-fulluninstall`, y en vez de desinstalar se pone a instalar. Sin la
	// bandera, la auto-reparación puede terminar haciendo lo contrario de lo
	// que pretende.
	_, err = CMD(meshOutput, []string{"-fulluninstall", "--no-embedded=1"}, 30, false)
	if err != nil {
		a.Logger.Errorln("ReinstallMesh() uninstall:", err)
	}
	time.Sleep(2 * time.Second)
	err = os.RemoveAll(filepath.Dir(a.MeshSystemEXE))
	if err != nil {
		a.Logger.Errorln("ReinstallMesh() RemoveAll:", err)
	}
	time.Sleep(1 * time.Second)
	_, err = a.installMesh(meshOutput, a.MeshSystemEXE, a.Proxy)
	if err != nil {
		a.Logger.Errorln("ReinstallMesh() installMesh:", err)
	}
}

func (a *Agent) setupNatsOptions() []nats.Option {
	reconnectWait := randRange(2, 8)
	opts := make([]nats.Option, 0)
	opts = append(opts, nats.Name(a.AgentID))
	opts = append(opts, nats.UserInfo(a.AgentID, a.Token))
	opts = append(opts, nats.ReconnectWait(time.Duration(reconnectWait)*time.Second))
	opts = append(opts, nats.RetryOnFailedConnect(true))
	opts = append(opts, nats.IgnoreAuthErrorAbort())
	opts = append(opts, nats.PingInterval(time.Duration(a.NatsPingInterval)*time.Second))
	opts = append(opts, nats.Compression(a.NatsWSCompression))
	opts = append(opts, nats.MaxReconnects(-1))
	opts = append(opts, nats.ReconnectBufSize(-1))
	opts = append(opts, nats.ProxyPath(a.NatsProxyPath))
	opts = append(opts, nats.ReconnectJitter(500*time.Millisecond, 4*time.Second))

	if a.Proxy != "" {
		proxyURL, err := url.Parse(a.Proxy)
		if err != nil {
			a.Logger.Errorf("setupNatsOptions(): failed to parse proxy URL: %v", err)
		} else {
			baseDialer := &net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}

			var dialFn func(network, addr string) (net.Conn, error)

			switch proxyURL.Scheme {
			case "http", "https":
				dialFn = newHTTPConnectDialer(proxyURL, baseDialer)
			default:
				a.Logger.Errorf("setupNatsOptions(): unsupported proxy scheme: %s", proxyURL.Scheme)
			}

			if dialFn != nil {
				cd := &customDialer{dialer: dialFn}
				opts = append(opts, nats.SetCustomDialer(cd))
			}
		}
	}

	opts = append(opts, nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
		a.Logger.Debugln("NATS disconnected:", err)
		a.Logger.Debugf("%+v\n", nc.Statistics)
	}))
	opts = append(opts, nats.ReconnectHandler(func(nc *nats.Conn) {
		a.Logger.Debugln("NATS reconnected")
		a.Logger.Debugf("%+v\n", nc.Statistics)
	}))
	opts = append(opts, nats.ErrorHandler(func(nc *nats.Conn, sub *nats.Subscription, err error) {
		a.Logger.Errorln("NATS error:", err)
		a.Logger.Errorf("%+v\n", sub)
	}))
	if a.Insecure {
		insecureConf := &tls.Config{
			InsecureSkipVerify: true,
		}
		opts = append(opts, nats.Secure(insecureConf))
	}
	return opts
}

func (a *Agent) GetUninstallExe() string {
	cderr := os.Chdir(a.ProgramDir)
	if cderr == nil {
		files, err := filepath.Glob("unins*.exe")
		if err == nil {
			for _, f := range files {
				if strings.Contains(f, "001") {
					return f
				}
			}
		}
	}
	return "unins000.exe"
}

func (a *Agent) CleanupAgentUpdates() {
	// dirs: WinTmpDir and system TMP — ProgramDir removed (updates no longer downloaded there)
	dirs := []string{a.WinTmpDir, os.Getenv("TMP")}
	for _, dir := range dirs {
		err := os.Chdir(dir)
		if err != nil {
			a.Logger.Debugln("CleanupAgentUpdates()", dir, err)
			continue
		}

		// remove leftover Observer agent update artifacts
		globs := []string{"observeragent-v*", "is-*.tmp"}
		for _, glob := range globs {
			files, err := filepath.Glob(glob)
			if err == nil {
				for _, f := range files {
					a.Logger.Debugln("CleanupAgentUpdates() Removing file:", f)
					os.Remove(f)
				}
			}
		}
	}

	err := os.Chdir(os.Getenv("TMP"))
	if err == nil {
		dirs, err := filepath.Glob("observeragent*")
		if err == nil {
			for _, f := range dirs {
				os.RemoveAll(f)
			}
		}
	}
}

func (a *Agent) RunPythonCode(code string, timeout int, args []string) (string, error) {
	content := []byte(code)
	tmpfn, _ := os.CreateTemp(a.WinTmpDir, "*.py")
	if _, err := tmpfn.Write(content); err != nil {
		a.Logger.Debugln(err)
		return "", err
	}
	defer os.Remove(tmpfn.Name())
	if err := tmpfn.Close(); err != nil {
		a.Logger.Debugln(err)
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	var outb, errb bytes.Buffer
	cmdArgs := []string{tmpfn.Name()}
	if len(args) > 0 {
		cmdArgs = append(cmdArgs, args...)
	}
	a.Logger.Debugln(cmdArgs)
	cmd := exec.CommandContext(ctx, a.PyBin, cmdArgs...)
	cmd.Stdout = &outb
	cmd.Stderr = &errb

	cmdErr := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		a.Logger.Debugln("RunPythonCode:", ctx.Err())
		return "", ctx.Err()
	}

	if cmdErr != nil {
		a.Logger.Debugln("RunPythonCode:", cmdErr)
		return "", cmdErr
	}

	if errb.String() != "" {
		a.Logger.Debugln(errb.String())
		return errb.String(), errors.New("RunPythonCode stderr")
	}

	return outb.String(), nil

}

func createWinTempDir() error {
	if !rmm.FileExists(defaultWinTmpDir) {
		err := os.Mkdir(defaultWinTmpDir, 0775)
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *Agent) RunTask(id int, jitter bool) error {
	// jitter reparte en el tiempo el arranque de una tarea programada cuando
	// una flota grande la dispara a la vez (backport v2.11.0). Solo aplica en
	// la ruta de tarea programada; las ejecuciones manuales vía NATS pasan false.
	if jitter {
		delay := time.Duration(rand.Int63n(int64(15 * time.Second)))
		a.Logger.Debugf("Applying startup jitter before task id %d: sleeping for %s", id, delay)
		time.Sleep(delay)
	}

	data := rmm.AutomatedTask{}
	url := fmt.Sprintf("/api/v3/%d/%s/taskrunner/", id, a.AgentID)
	r1, gerr := a.rClient.R().Get(url)
	if gerr != nil {
		a.Logger.Debugln(gerr)
		return gerr
	}

	if r1.IsError() {
		a.Logger.Debugln("Run Task:", r1.String())
		return nil
	}

	if err := json.Unmarshal(r1.Body(), &data); err != nil {
		a.Logger.Debugln(err)
		return err
	}

	start := time.Now()

	type TaskResult struct {
		Stdout   string  `json:"stdout"`
		Stderr   string  `json:"stderr"`
		RetCode  int     `json:"retcode"`
		ExecTime float64 `json:"execution_time"`
	}

	payload := TaskResult{}

	// loop through all task actions
	for _, action := range data.TaskActions {

		action_start := time.Now()
		if action.ActionType == "script" {
			stdout, stderr, retcode, err := a.RunScript(action.Code, action.Shell, action.Args, action.Timeout, action.RunAsUser, action.EnvVars, action.NushellEnableConfig, action.DenoDefaultPermissions)

			if err != nil {
				a.Logger.Debugln(err)
			}

			// add text to stdout showing which script ran if more than 1 script
			action_exec_time := time.Since(action_start).Seconds()

			if len(data.TaskActions) > 1 {
				payload.Stdout += fmt.Sprintf("\n------------\nRunning Script: %s. Execution Time: %f\n------------\n\n", action.ScriptName, action_exec_time)
			}

			// save results
			payload.Stdout += stdout
			payload.Stderr += stderr
			payload.RetCode = retcode

			if !data.ContinueOnError && stderr != "" {
				break
			}

		} else if action.ActionType == "cmd" {
			var stdout, stderr string

			switch runtime.GOOS {
			case "windows":
				out, err := CMDShell(action.Shell, []string{}, action.Command, action.Timeout, false, action.RunAsUser, false, nil, nil, nil)
				if err != nil {
					a.Logger.Debugln(err)
				}
				stdout = out[0]
				stderr = out[1]

				if stderr == "" {
					payload.RetCode = 0
				} else {
					payload.RetCode = 1
				}

			default:
				opts := a.NewCMDOpts()
				opts.Shell = action.Shell
				opts.Command = action.Command
				opts.Timeout = time.Duration(action.Timeout)
				out := a.CmdV2(opts)

				if out.Status.Error != nil {
					a.Logger.Debugln("RunTask() cmd: ", out.Status.Error.Error())
				}

				stdout = out.Stdout
				stderr = out.Stderr
				payload.RetCode = out.Status.Exit
			}

			if len(data.TaskActions) > 1 {
				action_exec_time := time.Since(action_start).Seconds()

				// add text to stdout showing which script ran
				payload.Stdout += fmt.Sprintf("\n------------\nRunning Command: %s. Execution Time: %f\n------------\n\n", action.Command, action_exec_time)
			}
			// save results
			payload.Stdout += stdout
			payload.Stderr += stderr

			if payload.RetCode != 0 {
				if !data.ContinueOnError {
					break
				}
			}
		} else {
			a.Logger.Debugln("Invalid Action", action)
		}
	}

	payload.ExecTime = time.Since(start).Seconds()

	_, perr := a.rClient.R().SetBody(payload).Patch(url)
	if perr != nil {
		a.Logger.Debugln(perr)
		return perr
	}
	return nil
}
