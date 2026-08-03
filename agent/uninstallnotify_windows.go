package agent

import (
	"os/user"
	"strings"
)

// uninstallActor intenta responder "quién desinstaló esto" en Windows.
//
// Hay DOS identidades en juego y no siempre coinciden:
//
//   - La que EJECUTA. El uninstaller corre elevado, así que si alguien pasó por
//     UAC con una cuenta de administrador distinta de la suya, ésta es la del
//     administrador, no la de la persona sentada frente al equipo.
//   - La de la SESIÓN DE CONSOLA. Es quien está usando el equipo.
//
// Se informan las dos y el servidor las muestra juntas cuando difieren. No hay
// `SUDO_USER` en Windows: ese campo queda vacío siempre, y es correcto que así
// sea — el servidor lo usa para decidir si dice "vía sudo".
//
// Nada de esto es una identidad fuerte: quien tiene privilegios en el equipo
// tiene, por definición, formas de mentirle a esta función. Sirve para saber
// qué pasó, no para sostener una acusación.
func uninstallActor() (actor, sudoUser, loginUser, consoleUser string) {
	if u, err := user.Current(); err == nil {
		actor = u.Username
		loginUser = u.Username
	}

	if _, name, err := activeUserSession(); err == nil {
		consoleUser = name
	}

	// Sin cuenta elevada legible, la de consola es mejor que nada.
	if actor == "" {
		actor = consoleUser
	}
	if actor == "" {
		actor = "unknown"
	}

	// `DOMINIO\usuario` y `usuario` son la misma persona: si la única diferencia
	// es el prefijo del dominio, no tiene sentido que el servidor las reporte
	// como dos identidades distintas.
	if consoleUser != "" && strings.EqualFold(bareUser(actor), bareUser(consoleUser)) {
		consoleUser = ""
	}

	return actor, "", loginUser, consoleUser
}

func bareUser(name string) string {
	if i := strings.LastIndex(name, `\`); i >= 0 {
		return name[i+1:]
	}
	return name
}
