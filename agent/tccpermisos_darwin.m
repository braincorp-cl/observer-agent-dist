// Feature 030 · Fase 2 · T016b — implementación del pedido de Cámara (macOS).
//
// 🔑 EL SUJETO DEL PERMISO ES EL `responsible process`, NO ESTE PROCESO.
// Medido en terreno el 2026-08-13: con la petición lanzada como hija del
// servicio, `tccd` publicó
//
//   AUTHREQ_PROMPTING: service=kTCCServiceCamera,
//     subject=Sub:{/opt/observeragent/observeragent}
//     Resp:{identifier=cl.observer.agent, responsible_path=/opt/observeragent/observeragent}
//
// y la fila nació a nombre del agente, con su cdhash vigente y auth_reason=2
// (consentimiento). Por eso este binario puede pedir un permiso que va a usar
// otro proceso, siempre que ese otro sea el mismo ejecutable en la misma ruta.
//
// 🚫 Y por eso mismo NO sirve lanzarlo desde SSH: ahí el `responsible` es
// `sshd-keygen-wrapper`, el sistema responde `Policy disallows prompt` y
// DENIEGA SIN PREGUNTAR, dejando el estado en 0 — indistinguible de una persona
// que dice que no.

#import <Foundation/Foundation.h>
#import <AVFoundation/AVFoundation.h>
#include <dispatch/dispatch.h>

#include "tccpermisos_darwin.h"

int observer_tcc_pedir_camara(double timeout_s) {
  @autoreleasepool {
    AVAuthorizationStatus previo =
        [AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeVideo];

    // Ya concedido, o imposible de pedir: se devuelve el estado sin tocar nada.
    // Pedirlo igual no mostraría diálogo (el sistema sólo pregunta cuando está
    // "no determinado") y sí gastaría el plazo completo esperando una respuesta
    // que nunca llega.
    if (previo != AVAuthorizationStatusNotDetermined) {
      return (int)previo;
    }

    dispatch_semaphore_t espera = dispatch_semaphore_create(0);

    [AVCaptureDevice requestAccessForMediaType:AVMediaTypeVideo
                             completionHandler:^(BOOL concedido) {
                               (void)concedido;
                               dispatch_semaphore_signal(espera);
                             }];

    dispatch_time_t plazo =
        dispatch_time(DISPATCH_TIME_NOW, (int64_t)(timeout_s * NSEC_PER_SEC));

    if (dispatch_semaphore_wait(espera, plazo) != 0) {
      return OBSERVER_TCC_TIMEOUT;
    }

    // Se devuelve el ESTADO y no el booleano del callback: el booleano dice qué
    // contestó la persona, y lo que importa es en qué quedó la fila de TCC.
    return (int)[AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeVideo];
  }
}
