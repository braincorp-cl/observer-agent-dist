// Feature 030 · Fase 2 · T016 — la foto de webcam en macOS, nativa.
//
// SE CORRE DENTRO DE LA SESIÓN AQUA Y SÓLO AHÍ. Medido en terreno el 2026-08-13
// con el permiso de Cámara concedido: este mismo código, lanzado por el
// LaunchDaemon vía `launchctl asuser`, entrega **0 cuadros en 25 s sin ningún
// error** —sesión corriendo, un input, un output, formato 1920×1080, dispositivo
// libre—; cargado como LaunchAgent en `gui/<uid>`, fotografía con el rostro
// legible. Quien mueva esta llamada fuera del ayudante Aqua va a ver exactamente
// ese fallo silencioso. Ver webcamaqua.go.

#import <Foundation/Foundation.h>
#import <AVFoundation/AVFoundation.h>
#import <CoreImage/CoreImage.h>
#import <CoreMedia/CoreMedia.h>
#include <string.h>

#include "webcam_darwin.h"

static void observer_cam_detalle(char *detalle, size_t largo, const char *texto) {
  if (detalle == NULL || largo == 0 || texto == NULL) {
    return;
  }
  strncpy(detalle, texto, largo - 1);
  detalle[largo - 1] = '\0';
}

// ObserverCamCaptor recibe los cuadros y se queda con el primero que sirve.
//
// Sin ARC (cgo no lo activa), así que los ivars se retienen y se sueltan a mano.
@interface ObserverCamCaptor : NSObject <AVCaptureVideoDataOutputSampleBufferDelegate> {
  int _porDescartar;
  NSData *_jpeg;
  NSLock *_candado;
}
- (instancetype)initDescartando:(int)cuadros;
- (NSData *)jpegRetenido;
@end

@implementation ObserverCamCaptor

- (instancetype)initDescartando:(int)cuadros {
  self = [super init];
  if (self) {
    _porDescartar = cuadros;
    _jpeg = nil;
    _candado = [[NSLock alloc] init];
  }
  return self;
}

- (void)dealloc {
  [_jpeg release];
  [_candado release];
  [super dealloc];
}

- (NSData *)jpegRetenido {
  [_candado lock];
  NSData *copia = [_jpeg retain];
  [_candado unlock];
  return [copia autorelease];
}

- (void)captureOutput:(AVCaptureOutput *)output
    didOutputSampleBuffer:(CMSampleBufferRef)sampleBuffer
           fromConnection:(AVCaptureConnection *)connection {
  (void)output;
  (void)connection;

  [_candado lock];
  BOOL yaTengo = (_jpeg != nil);
  if (!yaTengo && _porDescartar > 0) {
    // El descarte se cuenta acá dentro, con el candado tomado, porque los
    // cuadros llegan en una cola propia de AVFoundation y no en este hilo.
    _porDescartar--;
    [_candado unlock];
    return;
  }
  [_candado unlock];

  if (yaTengo) {
    return;
  }

  CVImageBufferRef pixeles = CMSampleBufferGetImageBuffer(sampleBuffer);
  if (pixeles == NULL) {
    return;
  }

  @autoreleasepool {
    CIImage *imagen = [CIImage imageWithCVImageBuffer:pixeles];
    if (imagen == nil) {
      return;
    }

    CIContext *contexto = [CIContext contextWithOptions:nil];
    CGColorSpaceRef espacio = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
    NSData *datos = nil;
    if (espacio != NULL) {
      datos = [contexto JPEGRepresentationOfImage:imagen
                                       colorSpace:espacio
                                          options:@{}];
      CGColorSpaceRelease(espacio);
    }

    if (datos == nil || [datos length] == 0) {
      return;
    }

    [_candado lock];
    if (_jpeg == nil) {
      _jpeg = [datos retain];
    }
    [_candado unlock];
  }
}

@end

int observer_webcam_capturar(const char *destino, int descartar, double timeout_s,
                             char *detalle, size_t detalle_len) {
  if (destino == NULL) {
    observer_cam_detalle(detalle, detalle_len, "destino nulo");
    return OBSERVER_CAM_FALLO;
  }

  @autoreleasepool {
    // Se CONSULTA el permiso y no se pide. Ver la nota del encabezado del .h.
    if ([AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeVideo] !=
        AVAuthorizationStatusAuthorized) {
      return OBSERVER_CAM_DENEGADO;
    }

    AVCaptureDevice *camara =
        [AVCaptureDevice defaultDeviceWithMediaType:AVMediaTypeVideo];
    if (camara == nil) {
      return OBSERVER_CAM_SIN_CAMARA;
    }

    NSError *error = nil;
    AVCaptureDeviceInput *entrada =
        [AVCaptureDeviceInput deviceInputWithDevice:camara error:&error];
    if (entrada == nil) {
      // Que la cámara esté tomada por otro programa es información del caso
      // —alguien está en una videollamada— y no un fallo del producto, así que
      // se distingue del resto.
      // 🪤 `AVErrorDeviceIsNotAvailableInBackground` NO existe en macOS: es de
      // iOS, y el compilador lo dice. Lo que macOS sí tiene es la propiedad
      // `isInUseByAnotherApplication`, que además responde antes de intentar
      // abrir nada.
      if ([camara isInUseByAnotherApplication] ||
          (error != nil && [[error domain] isEqualToString:AVFoundationErrorDomain] &&
           [error code] == AVErrorDeviceAlreadyUsedByAnotherSession)) {
        return OBSERVER_CAM_OCUPADA;
      }
      observer_cam_detalle(detalle, detalle_len,
                           error != nil ? [[error localizedDescription] UTF8String]
                                        : "no se pudo abrir la camara");
      return OBSERVER_CAM_FALLO;
    }

    AVCaptureSession *sesion = [[[AVCaptureSession alloc] init] autorelease];
    if (![sesion canAddInput:entrada]) {
      observer_cam_detalle(detalle, detalle_len, "la sesion no acepta la camara");
      return OBSERVER_CAM_FALLO;
    }
    [sesion addInput:entrada];

    AVCaptureVideoDataOutput *salida =
        [[[AVCaptureVideoDataOutput alloc] init] autorelease];
    // Descartar los cuadros atrasados: interesa el más reciente, no la fila.
    [salida setAlwaysDiscardsLateVideoFrames:YES];
    if (![sesion canAddOutput:salida]) {
      observer_cam_detalle(detalle, detalle_len, "la sesion no acepta la salida");
      return OBSERVER_CAM_FALLO;
    }
    [sesion addOutput:salida];

    ObserverCamCaptor *captor =
        [[[ObserverCamCaptor alloc] initDescartando:descartar] autorelease];
    dispatch_queue_t cola =
        dispatch_queue_create("cl.observer.agent.camara", DISPATCH_QUEUE_SERIAL);
    [salida setSampleBufferDelegate:captor queue:cola];

    [sesion startRunning];

    // Se sondea en vez de esperar un semáforo: el ayudante es un proceso de una
    // sola tarea, el sondeo son tres líneas sin objetos de dispatch que liberar,
    // y el plazo se respeta igual.
    NSDate *limite = [NSDate dateWithTimeIntervalSinceNow:timeout_s];
    NSData *jpeg = nil;
    while ([limite timeIntervalSinceNow] > 0) {
      jpeg = [captor jpegRetenido];
      if (jpeg != nil) {
        break;
      }
      [NSThread sleepForTimeInterval:0.05];
    }

    [sesion stopRunning];
    [salida setSampleBufferDelegate:nil queue:NULL];
    dispatch_release(cola);

    if (jpeg == nil) {
      // La sesión corrió y no llegó ni un cuadro utilizable. En terreno esto es
      // la firma exacta de haber corrido FUERA del contexto Aqua.
      return OBSERVER_CAM_SIN_CUADROS;
    }

    NSString *ruta = [NSString stringWithUTF8String:destino];
    NSError *errEscritura = nil;
    // Sin `NSDataWritingAtomic`: el archivo ya existe, lo creó el servicio con
    // 0600 y a nombre de este usuario, y escribir atómicamente lo reemplazaría
    // por uno nuevo con los permisos por omisión — dejando la foto de una cara
    // legible para cualquiera con sesión en el equipo.
    if (![jpeg writeToFile:ruta options:0 error:&errEscritura]) {
      observer_cam_detalle(detalle, detalle_len,
                           errEscritura != nil
                               ? [[errEscritura localizedDescription] UTF8String]
                               : "no se pudo escribir el archivo");
      return OBSERVER_CAM_FALLO;
    }

    return OBSERVER_CAM_OK;
  }
}
