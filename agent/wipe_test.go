package agent

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Feature 043 · T006 — contrato del borrado seguro por rutas y su verificación por
// relectura (RN-08). Se prueba el núcleo portable `ejecutarBorradoSeguro`/
// `sobrescribirArchivo` directamente (los `borrarRutasSeguro` por-SO delegan en él),
// con control POSITIVO (borra y verifica) y NEGATIVO (nunca reporta un "ok falso").

// Control positivo: un archivo real queda borrado y verificado.
func TestBorradoControlPositivo(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "secreto.txt")
	if err := os.WriteFile(f, []byte("dato sensible"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, verified, metodo, err := ejecutarBorradoSeguro([]string{f}, "test/1pass")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if !verified {
		t.Fatalf("esperaba verified=true, result=%v", result)
	}
	if metodo != "test/1pass" {
		t.Fatalf("method_applied no propagado: %q", metodo)
	}
	if result[f] != "borrado+verificado" {
		t.Fatalf("reporte por-ruta inesperado: %q", result[f])
	}
	if _, err := os.Lstat(f); !os.IsNotExist(err) {
		t.Fatalf("el archivo debería haber desaparecido, Lstat err=%v", err)
	}
}

// Control negativo: una ruta que no puede borrarse-verificar deja verified=false
// (y jamás un error duro que se confunda con éxito). Aquí la ruta no existe.
func TestBorradoControlNegativo(t *testing.T) {
	dir := t.TempDir()
	inexistente := filepath.Join(dir, "no-esta.txt")

	result, verified, _, err := ejecutarBorradoSeguro([]string{inexistente}, "test/1pass")
	if err != nil {
		t.Fatalf("no debe devolver error duro por fallo por-ruta: %v", err)
	}
	if verified {
		t.Fatal("una ruta no borrable JAMÁS debe reportar verified=true (ok falso)")
	}
	if got := result[inexistente]; len(got) < 6 || got[:6] != "error:" {
		t.Fatalf("esperaba reporte de error por-ruta, obtuve: %q", got)
	}
}

// Mezcla: si UNA ruta falla, la orden completa no se verifica (todo-o-nada).
func TestBorradoMixtoNoVerifica(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "ok.txt")
	if err := os.WriteFile(ok, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	malo := filepath.Join(dir, "no-esta.txt")

	result, verified, _, _ := ejecutarBorradoSeguro([]string{ok, malo}, "test/1pass")
	if verified {
		t.Fatal("con una ruta fallida, verified debe ser false")
	}
	if result[ok] != "borrado+verificado" {
		t.Fatalf("la ruta buena debió borrarse igual: %q", result[ok])
	}
	if _, err := os.Lstat(ok); !os.IsNotExist(err) {
		t.Fatal("la ruta buena debería haber desaparecido")
	}
}

// Sin archivos resueltos no se verifica: no se emite certificado por la nada.
func TestBorradoVacioNoVerifica(t *testing.T) {
	_, verified, _, err := ejecutarBorradoSeguro(nil, "test/1pass")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if verified {
		t.Fatal("una orden sin archivos no debe reportar verified=true")
	}
}

// La sobrescritura pisa el contenido antes de eliminar: el dato original no
// sobrevive en el archivo. Se prueba `sobrescribirArchivo` sin borrar para poder
// releer los bytes resultantes.
func TestSobrescribePisaContenido(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "claro.bin")
	original := []byte("PASSWORD=hunter2 ---- fin del secreto ----")
	if err := os.WriteFile(f, original, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := sobrescribirArchivo(f, int64(len(original))); err != nil {
		t.Fatalf("sobrescribirArchivo falló: %v", err)
	}

	tras, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(tras) != len(original) {
		t.Fatalf("el tamaño cambió: %d vs %d", len(tras), len(original))
	}
	if bytes.Equal(tras, original) {
		t.Fatal("el contenido no fue sobrescrito (sigue idéntico al original)")
	}
	if bytes.Contains(tras, []byte("hunter2")) {
		t.Fatal("el secreto sobrevivió a la sobrescritura")
	}
}

// No sigue symlinks: elimina el enlace, nunca el archivo destino.
func TestBorradoNoSigueSymlink(t *testing.T) {
	dir := t.TempDir()
	destino := filepath.Join(dir, "destino.txt")
	if err := os.WriteFile(destino, []byte("intacto"), 0o600); err != nil {
		t.Fatal(err)
	}
	enlace := filepath.Join(dir, "enlace.txt")
	if err := os.Symlink(destino, enlace); err != nil {
		t.Skipf("symlinks no disponibles en este entorno: %v", err)
	}

	_, verified, _, _ := ejecutarBorradoSeguro([]string{enlace}, "test/1pass")
	if !verified {
		t.Fatal("borrar un symlink válido debería verificar")
	}
	if _, err := os.Lstat(enlace); !os.IsNotExist(err) {
		t.Fatal("el enlace debería haber desaparecido")
	}
	got, err := os.ReadFile(destino)
	if err != nil || !bytes.Equal(got, []byte("intacto")) {
		t.Fatalf("el destino del symlink NO debe tocarse: err=%v contenido=%q", err, got)
	}
}
