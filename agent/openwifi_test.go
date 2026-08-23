package agent

import (
	"testing"
	"time"
)

// TestDeadManConectividad ejercita la máquina de estados del dead-man de
// conectividad (T036 · capa C): línea base en la primera observación offline,
// umbral, y reset al recuperar conectividad. Usa los globales del paquete, así
// que resetea el estado al empezar.
func TestDeadManConectividad(t *testing.T) {
	a := &Agent{}

	ultimaConectividadMu.Lock()
	ultimaConectividadSet = false
	ultimaConectividadUnix = 0
	ultimaConectividadMu.Unlock()

	// Primera observación sin conectividad: fija línea base, NO dispara.
	if a.deadManConectividadDisparado() {
		t.Fatal("no debe disparar en la primera observación (línea base)")
	}

	// Aún dentro del umbral (última conectividad hace deadManConectividad-1min).
	ultimaConectividadMu.Lock()
	ultimaConectividadUnix = time.Now().Unix() - int64((deadManConectividad-time.Minute)/time.Second)
	ultimaConectividadMu.Unlock()
	if a.deadManConectividadDisparado() {
		t.Fatal("no debe disparar antes del umbral")
	}

	// Pasado el umbral (última conectividad hace deadManConectividad+1min).
	ultimaConectividadMu.Lock()
	ultimaConectividadUnix = time.Now().Unix() - int64((deadManConectividad+time.Minute)/time.Second)
	ultimaConectividadMu.Unlock()
	if !a.deadManConectividadDisparado() {
		t.Fatal("debe disparar pasado el umbral")
	}

	// Recuperar conectividad resetea el dead-man.
	a.marcarConectividadUtilizable()
	if a.deadManConectividadDisparado() {
		t.Fatal("marcar conectividad debe resetear el dead-man")
	}
}
