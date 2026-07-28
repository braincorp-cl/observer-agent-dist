//go:build windows
// +build windows

package agent

import (
	"context"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Feature 028 · puente hacia la sesión del usuario en Windows.
//
// El agente es un servicio: vive en la sesión 0, que no tiene escritorio ni
// audio. Para las tres acciones de respuesta rápida usamos dos mecanismos
// distintos, y la elección importa:
//
//  1. `alert` → WTSSendMessageW. Es una API pensada exactamente para esto:
//     un servicio le pide al subsistema de Terminal Services que muestre un
//     cuadro de diálogo en una sesión determinada. No hace falta token, ni
//     escritorio, ni procesos hijos.
//
//     Es también la razón por la que NO usamos `msg.exe`, que era lo que sugería
//     el backlog 024 (y lo que hace Prey): `msg.exe` no viene en las ediciones
//     Home de Windows ni en instalaciones de Server sin Desktop Experience.
//     WTSSendMessageW es la API que `msg.exe` usa por debajo y está siempre.
//
//  2. `lock` y `alarm` → proceso hijo con el token del usuario interactivo.
//     Acá sí necesitamos ejecutar código en la sesión del usuario, porque
//     `LockWorkStation` sólo funciona llamada desde la sesión interactiva y el
//     audio sólo existe en la sesión del usuario.
//
//     Detalle que evita el error clásico: ninguno de estos dos procesos hijos
//     DIBUJA una ventana. Si lo hicieran, no bastaría el token — habría que
//     fijar `STARTUPINFO.lpDesktop` a `winsta0\default`, campo que `os/exec` de
//     Go no expone en `SysProcAttr`, y el diálogo se crearía en un escritorio
//     invisible. Al dejar el único diálogo (el `alert`) en manos de
//     WTSSendMessageW, ese problema no se presenta en ninguna de las tres
//     acciones.

var (
	modwtsapi32 = windows.NewLazySystemDLL("wtsapi32.dll")

	procWTSSendMessageW             = modwtsapi32.NewProc("WTSSendMessageW")
	procWTSQuerySessionInformationW = modwtsapi32.NewProc("WTSQuerySessionInformationW")
)

const (
	wtsCurrentServerHandle = 0
	wtsUserName            = 5 // WTS_INFO_CLASS.WTSUserName

	// Estilo del cuadro de diálogo: icono de exclamación, botón OK, y las dos
	// banderas que importan para que el mensaje se vea de verdad — traerlo al
	// frente y ponerlo sobre las demás ventanas. Un aviso que aparece detrás del
	// navegador no cumple su función.
	mbIconExclamation    = 0x00000030
	mbSetForeground      = 0x00010000
	mbTopMost            = 0x00040000
	alertMessageBoxStyle = mbIconExclamation | mbSetForeground | mbTopMost
)

// activeUserSession devuelve el ID de la sesión interactiva activa y el usuario
// que la tiene abierta.
//
// Acá vive sólo la consulta a la API; la regla de elección está en
// `pickUserSession` (usersession.go), fuera de la etiqueta de plataforma para que se
// pueda probar en la CI. Se recorren todas las sesiones en vez de usar únicamente
// WTSGetActiveConsoleSessionId para cubrir también RDP: en un servidor el usuario
// puede estar conectado remotamente y esa es igualmente su sesión.
func activeUserSession() (uint32, string, error) {
	var (
		sessions     *windows.WTS_SESSION_INFO
		sessionCount uint32
	)

	if err := windows.WTSEnumerateSessions(
		wtsCurrentServerHandle, 0, 1, &sessions, &sessionCount,
	); err != nil {
		return 0, "", err
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(sessions)))

	// unsafe.Slice sobre el puntero tipado en vez de aritmética con uintptr: el
	// resultado es el mismo recorrido pero sin la conversión uintptr→Pointer que
	// `go vet` marca como uso dudoso (y con razón: el GC no ve esa aritmética).
	candidatas := make([]wtsSession, 0, sessionCount)
	for _, s := range unsafe.Slice(sessions, sessionCount) {
		c := wtsSession{ID: s.SessionID, Active: s.State == windows.WTSActive}

		// El nombre se consulta sólo para las activas: es una llamada más a la API y
		// las demás se descartan igual.
		if c.Active {
			if user, err := wtsSessionUserName(s.SessionID); err == nil {
				c.User = user
			}
		}

		candidatas = append(candidatas, c)
	}

	id, user, ok := pickUserSession(candidatas, windows.WTSGetActiveConsoleSessionId())
	if !ok {
		return 0, "", errNoUserSession
	}

	return id, user, nil
}

// wtsSessionUserName consulta el usuario dueño de una sesión.
func wtsSessionUserName(sessionID uint32) (string, error) {
	var (
		buf   *uint16
		bytes uint32
	)

	ret, _, err := procWTSQuerySessionInformationW.Call(
		uintptr(wtsCurrentServerHandle),
		uintptr(sessionID),
		uintptr(wtsUserName),
		uintptr(unsafe.Pointer(&buf)),
		uintptr(unsafe.Pointer(&bytes)),
	)
	if ret == 0 {
		return "", err
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(buf)))

	return windows.UTF16PtrToString(buf), nil
}

// wtsSendMessage muestra un cuadro de diálogo en la sesión indicada.
//
// `bWait = 0`: no esperamos que el usuario cierre el diálogo. Si esperáramos, un
// mensaje que nadie cierra dejaría colgada la respuesta al servidor. El operador
// necesita saber que el mensaje se entregó, no que se leyó.
func wtsSendMessage(sessionID uint32, title, message string) error {
	titleUTF16, err := windows.UTF16FromString(title)
	if err != nil {
		return err
	}
	messageUTF16, err := windows.UTF16FromString(message)
	if err != nil {
		return err
	}

	var response uint32

	// Los largos van en BYTES y sin contar el terminador nulo — de ahí el -1 y
	// el *2. Pasar el largo en caracteres hace que el diálogo salga con el texto
	// cortado a la mitad.
	ret, _, callErr := procWTSSendMessageW.Call(
		uintptr(wtsCurrentServerHandle),
		uintptr(sessionID),
		uintptr(unsafe.Pointer(&titleUTF16[0])),
		uintptr((len(titleUTF16)-1)*2),
		uintptr(unsafe.Pointer(&messageUTF16[0])),
		uintptr((len(messageUTF16)-1)*2),
		uintptr(alertMessageBoxStyle),
		0, // Timeout 0 = el diálogo no se cierra solo
		uintptr(unsafe.Pointer(&response)),
		0, // bWait = FALSE
	)
	if ret == 0 {
		return callErr
	}
	return nil
}

// interactiveUserToken devuelve un token PRIMARIO del usuario dueño de la sesión
// indicada.
//
// Se pide primario y no de impersonación: cuando `SysProcAttr.Token` está definido,
// Go usa CreateProcessAsUser, que documentadamente requiere un token primario. (El
// camino `runasuser` heredado del upstream, en agent_windows.go, pide uno de
// impersonación; no se toca acá para no cambiar comportamiento probado ajeno a esta
// feature.)
//
// ⚠️ Esto reemplaza a `wintoken.GetInteractiveToken`, y el motivo es una
// inconsistencia real. Esa función elige **la primera sesión WTSActive, sin mirar si
// hay alguien adentro** (`gettoken.go`, v1.0.0), mientras `activeUserSession` exige
// además nombre de usuario no vacío. En un equipo con la consola en la pantalla de
// inicio de sesión —que es WTSActive y no tiene usuario— y la persona conectada por
// RDP, las dos elegían sesiones distintas: el `alert` llegaba bien porque
// WTSSendMessageW usa el ID que resolvimos nosotros, y `lock` y `alarm` contestaban
// `no_user_session` porque el token se pedía para la sesión vacía. Tres acciones y
// dos respuestas distintas sobre el mismo equipo con un usuario dentro.
//
// Pasando el sessionID explícito, las tres acciones hablan siempre de la misma
// sesión.
func interactiveUserToken(sessionID uint32) (windows.Token, error) {
	var userToken windows.Token
	if err := windows.WTSQueryUserToken(sessionID, &userToken); err != nil {
		return 0, err
	}
	// El token que entrega WTSQueryUserToken es primario pero no se puede heredar
	// tal cual: se duplica con los permisos que CreateProcessAsUser necesita y el
	// original se cierra acá mismo.
	defer userToken.Close()

	var primary windows.Token
	if err := windows.DuplicateTokenEx(userToken, windows.MAXIMUM_ALLOWED, nil,
		windows.SecurityDelegation, windows.TokenPrimary, &primary); err != nil {
		return 0, err
	}

	return primary, nil
}

// newUserSessionCmd prepara un comando para ejecutarse en la sesión del usuario
// interactivo, con su token y su entorno.
func (a *Agent) newUserSessionCmd(ctx context.Context, exe string, args ...string) (*exec.Cmd, error) {
	sessionID, user, err := activeUserSession()
	if err != nil {
		return nil, err
	}

	token, err := interactiveUserToken(sessionID)
	if err != nil {
		a.Logger.Debugln("newUserSessionCmd: no se pudo obtener el token de la sesión",
			sessionID, "de", user, ":", err)
		return nil, errNoUserSession
	}

	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Token: syscall.Token(token),
		// CREATE_NO_WINDOW evita el destello de una consola en el escritorio del
		// usuario. No afecta al audio ni a la llamada a LockWorkStation.
		CreationFlags: windows.CREATE_NO_WINDOW,
	}

	// Entorno del usuario, no el del servicio. Sin esto el hijo heredaría
	// USERPROFILE/APPDATA de SYSTEM, que apuntan a otro perfil.
	if envBlock, err := CreateEnvironmentBlock(syscall.Token(token)); err == nil {
		cmd.Env = EnvironmentBlockToSlice(envBlock)
		DestroyEnvironmentBlock(envBlock)
	}

	// El token se cierra cuando el proceso hijo termina, no acá: CreateProcessAsUser
	// ya lo duplicó internamente, pero cerrarlo antes de Start() invalidaría el
	// handle que quedó en SysProcAttr.
	go func() {
		<-ctx.Done()
		_ = token.Close()
	}()

	return cmd, nil
}

// killUserSessionCmd mata el proceso y sus hijos. El bucle de la alarma es un
// PowerShell que va lanzando reproducciones: matar sólo al padre dejaría el
// último sonido suelto.
func killUserSessionCmd(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = KillProc(int32(cmd.Process.Pid))
}
