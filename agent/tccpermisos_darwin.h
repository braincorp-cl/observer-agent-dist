// Feature 030 · Fase 2 · T016b — puente C para pedir el permiso de Cámara.
//
// Existe por UNA razón: `requestAccessForMediaType:completionHandler:` recibe
// un bloque de Objective-C, y AppleScript no sabe construir bloques. Todo lo
// demás de esta feature —consultar la cámara, consultar y pedir la pantalla— se
// hace con `osascript` cargando el framework, sin compilar nada.
//
// La implementación vive en tccpermisos_darwin.m y sólo se compila con cgo.

#ifndef OBSERVER_TCC_PERMISOS_DARWIN_H
#define OBSERVER_TCC_PERMISOS_DARWIN_H

// Códigos de retorno de observer_tcc_pedir_camara: los de AVAuthorizationStatus
// (0 no determinado, 1 restringido, 2 denegado, 3 concedido) y uno propio.
#define OBSERVER_TCC_TIMEOUT (-1)  // nadie contestó el diálogo dentro del plazo

// observer_tcc_pedir_camara dispara el diálogo del sistema y bloquea hasta que
// la persona conteste o hasta agotar timeout_s. Devuelve el estado observado
// DESPUÉS de la respuesta.
//
// Si el permiso ya estaba concedido devuelve 3 sin mostrar nada; si la fila de
// TCC es una denegación explícita —lo que deja cada auto-update— el sistema NO
// pregunta y devuelve 2 de inmediato. Esa distinción es la que decide si hace
// falta el `tccutil reset` acotado del lado Go.
int observer_tcc_pedir_camara(double timeout_s);

#endif // OBSERVER_TCC_PERMISOS_DARWIN_H
