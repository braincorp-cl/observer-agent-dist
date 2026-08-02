#define MyAppName "Observer RMM Agent"
#ifndef MyAppVersion
  #define MyAppVersion "2.10.0"
#endif
; VersionInfoVersion exige numérico puro (x.x.x); quitar sufijo prerelease (ej. -rc1).
#if Pos("-", MyAppVersion) > 0
  #define MyAppVersionNumeric Copy(MyAppVersion, 1, Pos("-", MyAppVersion) - 1)
#else
  #define MyAppVersionNumeric MyAppVersion
#endif
#define MyAppPublisher "BRAINCORP OP SPA"
#define MyAppURL "https://docs.observer.cl"
#define MyAppExeName "observeragent.exe"
#define MESHEXE "meshagent.exe"
#define MESHDIR "{sd}\Program Files\Mesh Agent"
; Directorio que contiene los DOS binarios a empaquetar (observeragent-modern.exe
; y observeragent-legacy.exe; ver [Files]). Override en CI:
;   iscc build\setup.iss /DAgentSrc=<dir> /DMyAppVersion=<ver>
#ifndef AgentSrc
  #define AgentSrc "C:\Users\Public\Documents\agent"
#endif

[Setup]
AppId={{0D34D278-5FAF-4159-A4A0-4E2D2C08139D}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppVerName={#MyAppName}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}
DefaultDirName={sd}\Program Files\ObserverAgent
DisableDirPage=yes
SetupLogging=yes
DisableProgramGroupPage=yes
UninstallDisplayIcon={app}\{#MyAppExeName}
Compression=lzma
SolidCompression=yes
WizardStyle=modern
RestartApplications=no
CloseApplications=no
MinVersion=6.1
VersionInfoVersion={#MyAppVersionNumeric}.0
VersionInfoOriginalFileName=observeragent.exe
AppCopyright="Copyright (C) 2025 {#MyAppPublisher}"

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Files]
; ─── Selección del binario por versión de Windows (ADR-023 §Enmienda E1 · T017) ───
;
; Un ÚNICO instalador por arquitectura lleva los dos binarios y deja en disco el
; que corresponde. Las dos entradas comparten DestName a propósito: en el equipo
; siempre queda "observeragent.exe" y nadie aguas abajo se entera de que hubo
; elección. Por eso `Agent.do_update` del backend no cambia y la actualización
; se vuelve a decidir sola en cada update, para siempre.
;
; El corte 10.0 ES 10.0.10240 (Windows 10 RTM): cubre Server 2016 (14393) y deja
; Server 2012 R2 (9600) del lado legacy. Es la misma frontera que el mínimo
; oficial de Go 1.21+ ("requires at least Windows 10 or Windows Server 2016"),
; que es lo que obliga a tener dos binarios. Si sube el Go del flujo moderno,
; hay que reverificar ese mínimo antes de tocar este corte (W-GOT-09).
;
; ⚠️ El `MinVersion=6.1` de [Setup] NO es el corte: es el PISO del instalador
; (Win7 SP1). Decide a QUIÉN se le permite instalar; estas dos líneas deciden
; QUÉ se le instala. Son cosas distintas y ninguna reemplaza a la otra.
;
; ⚠️ Decide la CONDICIÓN, no el orden de las líneas. Está medido, no supuesto:
; en un runner Windows real, invertir las condiciones sin mover una sola línea
; cambia el archivo instalado (run 30762134125). El gate "Corte de versión del
; instalador Windows" lo revalida cada lunes, porque las piezas que podrían
; romperlo —la versión de InnoSetup y la imagen del runner— no viven en este repo.
;
; 🔴 La rama OnlyBelowVersion está probada sólo por inversión lógica sobre un
; Windows moderno: GitHub no ofrece runners de Win7/8/8.1 (T026 de la 029).
;
; Los nombres van literales y no por macro: el gate "Corte de versión del
; instalador Windows" afirma sobre estas dos líneas leyéndolas, y una macro las
; volvería ilegibles para él sin ganar nada.
Source: "{#AgentSrc}\observeragent-modern.exe"; DestDir: "{app}"; DestName: "{#MyAppExeName}"; Flags: ignoreversion; MinVersion: 10.0
Source: "{#AgentSrc}\observeragent-legacy.exe"; DestDir: "{app}"; DestName: "{#MyAppExeName}"; Flags: ignoreversion; OnlyBelowVersion: 10.0

[Run]
Filename: "{app}\{#MyAppExeName}"; Description: "{cm:LaunchProgram,{#StringChange(MyAppName, '&', '&&')}}"; Flags: nowait postinstall skipifsilent runascurrentuser

[UninstallRun]
Filename: "{app}\{#MyAppExeName}"; Parameters: "-m cleanup"; RunOnceId: "cleanuprm";
Filename: "{cmd}"; Parameters: "/c taskkill /F /IM observeragent.exe"; RunOnceId: "killobsagent";
Filename: "{app}\{#MESHEXE}"; Parameters: "-fulluninstall"; RunOnceId: "meshrm";

[UninstallDelete]
Type: filesandordirs; Name: "{app}";
Type: filesandordirs; Name: "{#MESHDIR}";

[Code]
function InitializeSetup(): boolean;
var
  ResultCode: Integer;
begin
  Exec('cmd.exe', '/c ping 127.0.0.1 -n 2 && net stop observeragent', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Log('Stop observeragent: ' + IntToStr(ResultCode));

  Exec('cmd.exe', '/c taskkill /F /IM observeragent.exe', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Log('taskkill: ' + IntToStr(ResultCode));

  Result := True;
end;

procedure DeinitializeSetup();
var
  ResultCode: Integer;
  WorkingDir:   String;
begin

  WorkingDir := ExpandConstant('{sd}\Program Files\ObserverAgent');
  Exec('cmd.exe', ' /c observeragent.exe -m installsvc', WorkingDir, SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Log('install service: ' + IntToStr(ResultCode));

  Exec('cmd.exe', '/c net start observeragent', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Log('Start observeragent: ' + IntToStr(ResultCode));
end;

function InitializeUninstall(): Boolean;
var
  ResultCode: Integer;
begin
  Exec('cmd.exe', '/c ping 127.0.0.1 -n 2 && net stop observeragent', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Exec('cmd.exe', '/c taskkill /F /IM observeragent.exe', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);

  Exec('cmd.exe', '/c sc delete observeragent', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Log('delete observeragent: ' + IntToStr(ResultCode));

  Result := True;
end;
