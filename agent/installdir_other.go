//go:build !windows

package agent

import (
	"os"
	"path/filepath"
)

// Fuera de Windows no hay ni WOW64 ni registro que consultar: el valor histórico
// se mantiene tal cual. En Linux y macOS el agente vive en rutas fijas
// (`nixAgentDir`, `macPlistPath`) y `ProgramDir` no se usa para ubicarlo.
func ResolveProgramDir() string {
	return filepath.Join(os.Getenv("ProgramFiles"), progFilesName)
}

func persistInstallDir() error { return nil }
