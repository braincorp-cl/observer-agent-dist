// Feature 030 · Fase 2 · T016 — puente C para fotografiar con AVFoundation.
//
// Existe porque macOS **no trae ninguna herramienta de línea de comandos para
// tomar una foto**. `screencapture` es del sistema y por eso la Fase 1 no tuvo
// que instalar nada; su equivalente de cámara no existe. La versión anterior de
// esta feature dependía de `imagesnap` o `ffmpeg`, que no vienen con el sistema
// y en el Mac medido no estaban — y aunque hubieran estado, habrían chocado con
// el mismo muro de contexto que resuelve el ayudante Aqua (ver webcamaqua.go).
//
// La implementación vive en captura_darwin.m y sólo se compila con cgo. El
// release de macOS lleva cgo desde la feature 025 porque CoreLocation lo exige.

#ifndef OBSERVER_CAPTURA_DARWIN_H
#define OBSERVER_CAPTURA_DARWIN_H

#include <stddef.h>

// Códigos de retorno. El 0 es el éxito; los negativos se traducen a los motivos
// de webcam.go, que son los que llegan a la consola.
#define OBSERVER_CAM_OK 0
#define OBSERVER_CAM_SIN_CAMARA (-1)   // no hay ningún dispositivo de video
#define OBSERVER_CAM_DENEGADO (-2)     // TCC no tiene la cámara concedida
#define OBSERVER_CAM_OCUPADA (-3)      // otro programa tiene la cámara tomada
#define OBSERVER_CAM_SIN_CUADROS (-4)  // la sesión corrió y no entregó ni un cuadro
#define OBSERVER_CAM_FALLO (-5)        // cualquier otra cosa; el detalle va en `detalle`

// observer_webcam_capturar toma una foto y la escribe en `destino` como JPEG.
//
// `descartar` son los cuadros de calentamiento: el sensor arranca con la
// exposición automática sin converger y los primeros cuadros salen oscuros o
// completamente negros. Pedir "un cuadro" devuelve casi siempre ese primero, y
// ahí nace el peor resultado posible — una foto negra que parece evidencia.
//
// 🔑 NO PIDE EL PERMISO. Si TCC no lo tiene concedido devuelve
// OBSERVER_CAM_DENEGADO sin abrir nada. Pedirlo mostraría el diálogo del sistema
// **en la cara de quien tiene el equipo**, que en un caso de robo es justo lo que
// esta feature evita: el permiso se pide en la instalación y tras cada
// actualización (T016b/T016c), nunca durante un ciclo activo.
//
// `detalle` recibe un texto corto para el log del agente; puede ser NULL.
int observer_webcam_capturar(const char *destino, int descartar, double timeout_s,
                             char *detalle, size_t detalle_len);

// observer_camara_estado devuelve el AVAuthorizationStatus de la Camara SIN
// pedir nada: 0 no determinado, 1 restringido, 2 denegado, 3 concedido.
int observer_camara_estado(void);

// observer_pantalla_preflight consulta si se puede grabar la pantalla, SIN
// efectos secundarios. Devuelve 1 o 0.
//
// 🪤 Es un BOOLEANO, no una escala: un permiso denegado y uno sin determinar se
// ven idénticos desde acá. Esa ambigüedad es la que obliga a la escalada de
// T016c a intentar el diálogo primero y el reset después.
int observer_pantalla_preflight(void);

// observer_pantalla_pedir dispara el diálogo de Grabación de pantalla y
// devuelve el estado resultante (1 concedido, 0 no).
//
// 🚫 NO se llama durante un ciclo de modo perdido: el diálogo delata al ladrón.
// Y si la fila de TCC ya dice "permitido" con un csreq viejo, el sistema NO
// pregunta y responde que sí de inmediato — por eso el resultado hay que
// verificarlo contra el comportamiento real, no creerle.
int observer_pantalla_pedir(void);

#endif // OBSERVER_CAPTURA_DARWIN_H
