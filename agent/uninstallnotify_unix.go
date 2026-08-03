//go:build !windows
// +build !windows

package agent

import (
	"os"
	"os/user"
)

// uninstallActor en Unix.
//
// En la práctica el aviso de Linux y macOS lo manda el SCRIPT de
// desinstalación, no el binario: el script es quien corre en la sesión de quien
// escribió el comando y por eso ve `SUDO_USER`. Esta versión existe para que
// `NotifyUninstall` compile y funcione en las tres plataformas, y para cubrir a
// cualquier ruta futura que llame al binario directamente.
//
// `SUDO_USER` es lo único que distingue una persona de "root". No es prueba de
// nada: quien tiene root puede exportarlo con el nombre que quiera.
func uninstallActor() (actor, sudoUser, loginUser, consoleUser string) {
	sudoUser = os.Getenv("SUDO_USER")

	if u, err := user.Current(); err == nil {
		loginUser = u.Username
	}

	actor = sudoUser
	if actor == "" {
		actor = loginUser
	}
	if actor == "" {
		actor = "unknown"
	}

	return actor, sudoUser, loginUser, ""
}
