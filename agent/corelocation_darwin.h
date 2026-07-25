// Feature 025 · puente C para CoreLocation (macOS).
//
// Se expone una única función bloqueante para que el lado Go no tenga que
// manejar el run-loop ni el delegate de Objective-C. La implementación vive en
// corelocation_darwin.m y solo se compila cuando cgo está habilitado.

#ifndef OBSERVER_CORELOCATION_DARWIN_H
#define OBSERVER_CORELOCATION_DARWIN_H

// Códigos de retorno de observer_corelocation_fix.
#define OBSERVER_CL_OK        0  // fix obtenido; lat/lng/acc escritos
#define OBSERVER_CL_TIMEOUT   1  // sin respuesta dentro del plazo
#define OBSERVER_CL_ERROR     2  // el manager falló (típicamente sin autorización)
#define OBSERVER_CL_DISABLED  3  // Location Services apagado a nivel sistema

// observer_corelocation_fix pide una ubicación única y bloquea hasta obtenerla
// o hasta agotar timeout_s. Los punteros de salida solo se escriben cuando el
// retorno es OBSERVER_CL_OK; auth_status siempre recibe el CLAuthorizationStatus
// observado (0 notDetermined, 1 restricted, 2 denied, 3 authorizedAlways).
int observer_corelocation_fix(double timeout_s,
                              double *lat,
                              double *lng,
                              double *acc,
                              int *auth_status);

#endif // OBSERVER_CORELOCATION_DARWIN_H
