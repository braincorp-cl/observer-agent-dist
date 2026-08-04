//go:build !windows

package agent

// En Linux y macOS la salida de un proceso ya viene en UTF-8: el locale del servicio
// lo garantiza y no existe la nocion de pagina de codigos de consola. Si los bytes no
// eran UTF-8 validos, el script escribio basura y no hay nada que reinterpretar.
func decodeSystemCodePage(_ []byte) (string, bool) {
	return "", false
}
