//go:build windows
// +build windows

package agent

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"github.com/gonutz/w32/v2"
	"golang.org/x/sys/windows"
)

// Feature 030 · Fase 1 · captura de pantalla en Windows (T008).
//
// EL PROBLEMA: el agente es un servicio y vive en la sesión 0, que tiene un
// escritorio propio donde no hay nada dibujado. Un `BitBlt` hecho desde ahí no
// falla —devuelve 0 y escribe un PNG perfectamente válido— y ese PNG es NEGRO.
// Es el "ok falso" en su forma más pura: código de salida 0, archivo con peso,
// cero información.
//
// LA SOLUCIÓN, y por qué es una re-ejecución y no una llamada directa: para
// leer el escritorio del usuario hay que ser un proceso DE ESA SESIÓN. El
// puente de la 028 (`newUserSessionCmd`, token primario de
// `WTSQueryUserToken`) ya sabe lanzar procesos ahí, así que el agente se
// re-ejecuta a sí mismo con `-m screencapture` y el hijo —que sí está en la
// sesión del usuario— hace el BitBlt y escribe el archivo. Es exactamente el
// patrón `-m geoprobe` de la 025, donde CoreLocation tampoco atendía a la
// sesión 0.
//
// 🟡 LO QUE FALTA MEDIR EN TERRENO: `os/exec` no expone `STARTUPINFO.lpDesktop`,
// así que el hijo hereda el escritorio en vez de pedir `winsta0\default`
// explícitamente. En la 028 eso alcanzó para `LockWorkStation` —que también
// exige escritorio interactivo y funcionó en equipos reales—, pero la captura es
// más exigente. Si en algún equipo no alcanza, el síntoma será una imagen negra,
// y esa imagen NO se sube: `CaptureScreen` la detecta y reporta
// `pantalla_en_negro`. O sea que el modo de falla ya es honesto; lo que falta es
// saber si ocurre.

// capturarPantalla (lado servicio) lanza el hijo en la sesión del usuario y
// recoge la ruta del archivo que dejó.
func (a *Agent) capturarPantalla(nombre string) ScreenShot {
	exe, err := os.Executable()
	if err != nil {
		a.Logger.Debugln("capturarPantalla: no se pudo resolver el ejecutable:", err)
		return ScreenShot{Reason: captureFailed}
	}

	// 30 s: el BitBlt tarda milisegundos, pero el arranque del proceso hijo en
	// un equipo cargado puede demorar. Sin plazo, un hijo colgado dejaría el
	// ciclo de captura clavado para siempre.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// `-log ERROR` para que el hijo no escriba en el log del agente: corre con
	// los permisos del usuario y el archivo de log es del servicio. Sin esto
	// cada captura dejaría un error de escritura por línea.
	cmd, err := a.newUserSessionCmd(ctx, exe, "-m", modoCapturaPantalla, "-log", "ERROR", nombre)
	if err != nil {
		a.Logger.Debugln("capturarPantalla: no hay sesión de usuario donde capturar:", err)
		return ScreenShot{Reason: captureNoSession}
	}

	salida, err := cmd.Output()
	if err != nil {
		a.Logger.Debugln("capturarPantalla: el proceso de captura falló:", err)
		return ScreenShot{Reason: captureFailed}
	}

	return parsearSalidaCaptura(string(salida))
}

// RunScreenCapture es el modo `-m screencapture`: ya corremos DENTRO de la
// sesión del usuario, así que acá sí se puede leer la pantalla.
//
// Imprime una sola línea y nada más — es la única salida que el padre lee.
func (a *Agent) RunScreenCapture(nombre string) {
	if nombre == "" {
		nombre = screenshotFilename(time.Now())
	}

	// El temporal del USUARIO, que es donde este proceso puede escribir. El
	// padre (SYSTEM) lo lee y lo borra en cuanto lo sube.
	destino := filepath.Join(os.TempDir(), filepath.Base(nombre))

	if err := capturarPantallaVirtual(destino); err != nil {
		a.Logger.Debugln("RunScreenCapture:", err)
		fmt.Println(prefijoCapturaFallo + captureFailed)
		return
	}

	fmt.Println(prefijoCapturaOK + destino)
}

// capturarPantallaVirtual copia la PANTALLA VIRTUAL completa a un PNG.
//
// Virtual y no primaria: un equipo con dos monitores muestra la mitad de lo que
// pasa en cada uno, y en un caso de robo la ventana que interesa puede estar en
// cualquiera de los dos. `SM_?VIRTUALSCREEN` da el rectángulo que las abarca a
// todas, con origen que puede ser NEGATIVO (un monitor a la izquierda del
// principal), y por eso el origen se pasa a BitBlt en vez de asumir (0,0).
func capturarPantallaVirtual(destino string) error {
	// Sin esto, en una pantalla con escalado (125 %, 150 % — lo normal en un
	// portátil moderno) Windows le miente al proceso: GetSystemMetrics devuelve
	// las medidas escaladas y la captura sale recortada o borrosa. Se pide antes
	// de tocar nada gráfico y se ignora el error: si falla, la captura sigue
	// sirviendo, sólo que con menos resolución.
	hacerProcesoConscienteDeDPI()

	x := w32.GetSystemMetrics(w32.SM_XVIRTUALSCREEN)
	y := w32.GetSystemMetrics(w32.SM_YVIRTUALSCREEN)
	ancho := w32.GetSystemMetrics(w32.SM_CXVIRTUALSCREEN)
	alto := w32.GetSystemMetrics(w32.SM_CYVIRTUALSCREEN)
	if ancho <= 0 || alto <= 0 {
		return fmt.Errorf("la pantalla virtual mide %dx%d: no hay escritorio que capturar", ancho, alto)
	}

	hdcPantalla := w32.GetDC(0)
	if hdcPantalla == 0 {
		return fmt.Errorf("GetDC devolvió 0: el proceso no tiene escritorio asignado")
	}
	defer w32.ReleaseDC(0, hdcPantalla)

	hdcMem := w32.CreateCompatibleDC(hdcPantalla)
	if hdcMem == 0 {
		return fmt.Errorf("CreateCompatibleDC falló")
	}
	defer w32.DeleteDC(hdcMem)

	bmp := w32.CreateCompatibleBitmap(hdcPantalla, ancho, alto)
	if bmp == 0 {
		return fmt.Errorf("CreateCompatibleBitmap falló")
	}
	defer w32.DeleteObject(w32.HGDIOBJ(bmp))

	anterior := w32.SelectObject(hdcMem, w32.HGDIOBJ(bmp))
	defer w32.SelectObject(hdcMem, anterior)

	// CAPTUREBLT incluye las ventanas por capas (layered): sin él, ciertos
	// diálogos y el menú de inicio salen como agujeros transparentes.
	if !w32.BitBlt(hdcMem, 0, 0, ancho, alto, hdcPantalla, x, y, w32.SRCCOPY|w32.CAPTUREBLT) {
		return fmt.Errorf("BitBlt falló")
	}

	img, err := bitmapAImagen(hdcMem, bmp, ancho, alto)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(destino, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	return png.Encode(f, img)
}

// bitmapAImagen baja los píxeles del HBITMAP a memoria del proceso y arma la
// imagen de Go.
//
// `BiHeight` va en NEGATIVO para pedir las filas de arriba hacia abajo: los DIB
// de Windows son bottom-up por herencia de OS/2 y sin esto la captura sale
// dada vuelta. Y los canales llegan en orden BGRA, no RGBA — el intercambio del
// bucle no es decorativo: sin él la evidencia sale con los azules y los rojos
// cambiados, que es sutil y pasa desapercibido hasta que alguien mira una foto
// de piel humana.
func bitmapAImagen(hdc w32.HDC, bmp w32.HBITMAP, ancho, alto int) (image.Image, error) {
	info := w32.BITMAPINFO{
		BmiHeader: w32.BITMAPINFOHEADER{
			BiSize:        uint32(unsafe.Sizeof(w32.BITMAPINFOHEADER{})),
			BiWidth:       int32(ancho),
			BiHeight:      int32(-alto),
			BiPlanes:      1,
			BiBitCount:    32,
			BiCompression: w32.BI_RGB,
		},
	}

	crudo := make([]byte, ancho*alto*4)
	if w32.GetDIBits(hdc, bmp, 0, uint(alto), unsafe.Pointer(&crudo[0]), &info, w32.DIB_RGB_COLORS) == 0 {
		return nil, fmt.Errorf("GetDIBits falló")
	}

	img := image.NewRGBA(image.Rect(0, 0, ancho, alto))
	for i := 0; i < len(crudo); i += 4 {
		img.Pix[i+0] = crudo[i+2] // R <- B
		img.Pix[i+1] = crudo[i+1] // G
		img.Pix[i+2] = crudo[i+0] // B <- R
		// El canal alfa que devuelve GDI para una captura de pantalla es basura
		// (queda en 0 y dejaría el PNG completamente transparente, que se ve
		// negro en cualquier visor). Se fuerza opaco.
		img.Pix[i+3] = 0xff
	}

	return img, nil
}

// hacerProcesoConscienteDeDPI pide `SetProcessDPIAware`. No está en w32, así que
// se llama directo a user32. Se ignora el resultado a propósito: la función
// falla si el proceso ya declaró su conciencia de DPI por manifiesto, y eso no
// es un problema.
func hacerProcesoConscienteDeDPI() {
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("SetProcessDPIAware")
	if err := proc.Find(); err != nil {
		return
	}
	_, _, _ = proc.Call()
}

// motivoDePantallaNegra: en Windows no se consulta el estado de la pantalla.
//
// La distinción "apagada" vs "negra sin explicación" sí existe acá —un equipo
// con la pantalla en ahorro de energía da negro igual que en Linux—, pero
// averiguarlo desde la sesión 0 exige otra ida y vuelta al proceso hijo
// (`GetDevicePowerState` / `SPI_GETSCREENSAVERRUNNING` viven en la sesión del
// usuario). Se deja para cuando haya terreno en Windows que lo justifique:
// inventar el motivo sin poder medirlo sería peor que el motivo genérico, que
// al menos no afirma nada falso.
func (a *Agent) motivoDePantallaNegra() string {
	return captureBlank
}

// usuarioDeLaSesionGrafica devuelve el usuario de la sesión interactiva elegida
// por `pickUserSession` — la misma a la que se le capturó la pantalla, no "la
// primera que aparezca".
func (a *Agent) usuarioDeLaSesionGrafica() string {
	_, user, err := activeUserSession()
	if err != nil {
		return ""
	}
	return user
}
