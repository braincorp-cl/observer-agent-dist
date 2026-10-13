//go:build windows && 386

package agent

import "time"

// Windows de 32 bits: el SQLite en Go puro no compila para esa arquitectura;
// el monitoreo funciona igual, sin URL.
func leerVisitas(string, tipoHistorial, time.Time) []visitaNav { return nil }
