// Feature 025 · implementación CoreLocation (macOS).
//
// CLLocationManager es asíncrono y entrega por delegate, así que se corre un
// run-loop acotado hasta que llega el primer fix o vence el plazo. El proceso
// que ejecuta esto DEBE correr en el contexto del usuario: validado en el spike
// Fase 0, un LaunchDaemon en sesión 0 queda en notDetermined para siempre
// (mismo límite que Windows/SYSTEM con WinRT). El lado Go se encarga de
// re-ejecutarse con `-m geoprobe` vía `launchctl asuser`.

#import <Foundation/Foundation.h>
#import <CoreLocation/CoreLocation.h>

#include "corelocation_darwin.h"

@interface ObserverGeoDelegate : NSObject <CLLocationManagerDelegate>
@property(nonatomic, assign) BOOL done;
@property(nonatomic, assign) BOOL failed;
@property(nonatomic, assign) double lat;
@property(nonatomic, assign) double lng;
@property(nonatomic, assign) double acc;
@end

@implementation ObserverGeoDelegate

- (void)locationManager:(CLLocationManager *)manager
     didUpdateLocations:(NSArray<CLLocation *> *)locations {
  CLLocation *loc = [locations lastObject];
  if (loc == nil) {
    return;
  }
  // horizontalAccuracy negativa = coordenada inválida según Apple.
  if (loc.horizontalAccuracy < 0) {
    return;
  }
  self.lat = loc.coordinate.latitude;
  self.lng = loc.coordinate.longitude;
  self.acc = loc.horizontalAccuracy;
  self.failed = NO;
  self.done = YES;
}

- (void)locationManager:(CLLocationManager *)manager
       didFailWithError:(NSError *)error {
  self.failed = YES;
  self.done = YES;
}

@end

// authorizationStatus pasó de método de clase a propiedad de instancia en
// macOS 11. Se consultan ambas rutas para no romper en versiones anteriores.
static int observer_auth_status(CLLocationManager *manager) {
  if (@available(macOS 11.0, *)) {
    return (int)[manager authorizationStatus];
  }
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
  return (int)[CLLocationManager authorizationStatus];
#pragma clang diagnostic pop
}

int observer_corelocation_fix(double timeout_s,
                              double *lat,
                              double *lng,
                              double *acc,
                              int *auth_status) {
  @autoreleasepool {
    CLLocationManager *manager = [[CLLocationManager alloc] init];
    ObserverGeoDelegate *delegate = [[ObserverGeoDelegate alloc] init];
    delegate.done = NO;
    delegate.failed = NO;

    manager.delegate = delegate;
    manager.desiredAccuracy = kCLLocationAccuracyBest;

#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
    BOOL servicesEnabled = [CLLocationManager locationServicesEnabled];
#pragma clang diagnostic pop
    if (!servicesEnabled) {
      if (auth_status != NULL) {
        *auth_status = observer_auth_status(manager);
      }
      return OBSERVER_CL_DISABLED;
    }

    [manager requestAlwaysAuthorization];
    [manager startUpdatingLocation];

    NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:timeout_s];
    while (!delegate.done && [deadline timeIntervalSinceNow] > 0) {
      @autoreleasepool {
        [[NSRunLoop currentRunLoop]
                runMode:NSDefaultRunLoopMode
             beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.25]];
      }
    }

    [manager stopUpdatingLocation];

    if (auth_status != NULL) {
      *auth_status = observer_auth_status(manager);
    }
    if (!delegate.done) {
      return OBSERVER_CL_TIMEOUT;
    }
    if (delegate.failed) {
      return OBSERVER_CL_ERROR;
    }
    if (lat != NULL) {
      *lat = delegate.lat;
    }
    if (lng != NULL) {
      *lng = delegate.lng;
    }
    if (acc != NULL) {
      *acc = delegate.acc;
    }
    return OBSERVER_CL_OK;
  }
}
