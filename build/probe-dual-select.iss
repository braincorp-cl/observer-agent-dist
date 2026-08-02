; Sonda de verificación — NO es un instalador de producto.
;
; Responde dos preguntas que gatean la enmienda propuesta al ADR-023:
;
;   P1. ¿InnoSetup reporta la versión VERDADERA de Windows, o miente con 6.2?
;       GetVersionEx miente en Windows 8.1+ si el ejecutable no está manifestado.
;       Si Inno mintiera, MinVersion: 10.0 nunca se cumpliría y todo Windows
;       moderno recibiría el binario legacy en silencio.
;
;   P2. ¿MinVersion:/OnlyBelowVersion: por archivo realmente DISCRIMINA, o el
;       resultado sale del orden de las entradas de [Files]?
;       Se compila una segunda vez con /DFLIP=1, que invierte las condiciones
;       sin tocar el orden. Si el archivo instalado cambia, discrimina.
;
; Ambos casos escriben "selected.txt" con el MISMO DestName a propósito: el
; contenido del archivo instalado ES el veredicto.

#define ProbeDir "dualprobe"
#ifndef Src
  #define Src "."
#endif

[Setup]
AppName=Observer Dual Select Probe
AppVersion=1.0
DefaultDirName={sd}\{#ProbeDir}
OutputBaseFilename=dualprobe
; Mismo piso que el instalador real del agente (Windows 7 SP1).
MinVersion=6.1
SetupLogging=yes
Uninstallable=no
DisableDirPage=yes
DisableProgramGroupPage=yes
PrivilegesRequired=admin

[Files]
#ifdef FLIP
; Control invertido: las MISMAS dos líneas en el MISMO orden, condiciones cruzadas.
Source: "{#Src}\modern.txt"; DestDir: "{app}"; DestName: "selected.txt"; OnlyBelowVersion: 10.0
Source: "{#Src}\legacy.txt"; DestDir: "{app}"; DestName: "selected.txt"; MinVersion: 10.0
#else
Source: "{#Src}\modern.txt"; DestDir: "{app}"; DestName: "selected.txt"; MinVersion: 10.0
Source: "{#Src}\legacy.txt"; DestDir: "{app}"; DestName: "selected.txt"; OnlyBelowVersion: 10.0
#endif

[Code]
procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
  begin
    Log('PROBE_DETECTED_VERSION=' + GetWindowsVersionString);
    Log('PROBE_NT_VERSION=' + IntToStr(GetWindowsVersion));
  end;
end;
