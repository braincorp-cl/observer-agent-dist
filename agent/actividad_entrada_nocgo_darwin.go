//go:build darwin && !cgo

package agent

func contadoresEntradaDarwin() (uint64, uint64, bool) { return 0, 0, false }
