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
; Arquitectura de ESTE instalador ("386" o "amd64"). La pasa la CI:
;   iscc build\setup.iss /DArch=386
; Alimenta el guard de InitializeSetup que impide instalar el agente de 32 bits
; en un Windows de 64 bits. El valor por defecto NO es una arquitectura a
; propósito: si alguien compila sin el macro, el guard no se dispara y el
; instalador se comporta como antes. Que ese olvido no pase inadvertido lo
; custodia el job `estructura` de verify-inno-dual-select.yml, que comprueba que
; release.yml siga pasando el macro.
#ifndef Arch
  #define Arch "sin-especificar"
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
; `--no-embedded=1` va DESPUÉS del comando. El binario que MeshCentral entrega
; para instalar trae un instalador JS anexado que se dispara ante CUALQUIER
; argumento, también ante `-fulluninstall`: medido en Linux (HP-ProOne-400,
; 2026-07-29) abre un diálogo en el escritorio y se cuelga esperando un clic.
; Acá eso dejaría el nodo Mesh vivo en el servidor después de desinstalar.
Filename: "{app}\{#MESHEXE}"; Parameters: "-fulluninstall --no-embedded=1"; RunOnceId: "meshrm";

[UninstallDelete]
Type: filesandordirs; Name: "{app}";
Type: filesandordirs; Name: "{#MESHDIR}";
;
; ⚠️ ASIMETRÍA CONOCIDA Y PENDIENTE DE MEDICIÓN — no es un descuido.
;
; NO agregar acá "{commonpf32}\Mesh Agent", que es donde queda el Mesh Agent de
; 32 bits sobre un Windows x64 (medido en terreno). Se probó y se retiró: quien
; desregistra el nodo es el `-fulluninstall` del [UninstallRun] de arriba, y si
; ESE falla, borrar el directorio se lleva el único binario con el que se puede
; reintentar a mano. El equipo quedaría con el servicio "Mesh Agent" registrado
; apuntando a un ejecutable inexistente y el nodo vivo en MeshCentral, sin
; recuperación local posible. Dejar el directorio en pie es el comportamiento
; recuperable; borrarlo, el irreversible.
;
; PERO ESE MISMO RAZONAMIENTO CONDENA A LA LÍNEA `{#MESHDIR}` DE ARRIBA, que es
; el literal `{sd}\Program Files\Mesh Agent`: viva en amd64 y en Windows de 32
; bits genuino, inerte justo en 386 sobre x64. Se deja EN PIE a propósito, y por
; una razón que no es técnica sino de orden de las mediciones:
;
;   - La precondición del escenario malo NO es hipotética. Está en el log del
;     2026-08-06 (LenovoG480 amd64, `/LOG=`): `meshagent.exe -fulluninstall`
;     devolvió `CreateProcess failed (2)` porque `{app}\meshagent.exe` no
;     existía, e Inno siguió adelante — el mismo log prueba que Inno NO aborta
;     cuando un [UninstallRun] falla.
;   - Esa corrida es la investigación ABIERTA del residuo en ARP de amd64.
;     Cambiar esta línea ahora dejaría la medición pendiente hecha sobre código
;     distinto del que produjo el síntoma.
;
; Se resuelve con el paso 0 del plan de terreno —desinstalar el agente amd64 con
; `/LOG=`— que responde las tres preguntas de una vez: si `{app}\meshagent.exe`
; existe en un amd64 sano, si el `-fulluninstall` corre y retorna, y si el
; directorio y el servicio sobreviven. Con eso se decide con datos.
;
; La tercera salida evaluada fue condicionar la línea con un `Check:` que sólo
; borre si el servicio ya no existe. Es lo correcto en principio, pero NO está
; verificado que Inno evalúe funciones `Check` en [UninstallDelete] en tiempo de
; desinstalación, y acá no se meten mecanismos sin medir: es la misma razón por
; la que existe el gate del corte de versión.

[Code]
// El agente de 32 bits NO se instala de cero en un Windows de 64 bits.
//
// POR QUÉ EXISTE ESTE GUARD. Esa combinación no es una configuración soportada:
// es una instalación equivocada, y produce un equipo que se ve sano y no lo
// está. El instalador deja los archivos en `C:\Program Files\ObserverAgent`
// mientras el agente, corriendo bajo WOW64, deduce `C:\Program Files (x86)`; de
// esa divergencia salían la autoactualización que nunca surte efecto —el equipo
// re-descarga el instalador cada hora, para siempre, en silencio— y un inventario
// de software que veía 97 de 424 programas. Y no hay corrección automática: el
// servidor elige el instalador del update con el `goarch` que el propio agente
// reporta, así que un 386 pide 386 para siempre.
//
// Se ataca acá, en el único punto donde la situación se CREA: alguien eligiendo
// el instalador equivocado. Todo lo demás es tratar el síntoma.
//
// ⚠️ SÓLO BLOQUEA LA INSTALACIÓN NUEVA, y eso es esencial. Si bloqueara también
// la actualización, un equipo que YA está en ese estado nunca podría recibir el
// update — y perdería justamente el reapuntado del servicio que lo repara. La
// señal de «ya hay un agente acá» es la clave de configuración del registro, que
// crea `-m install` y borra la desinstalación.
//
// La comparación es en tiempo de ejecución y no con `#if` a propósito: deja el
// texto legible para el gate que afirma sobre este archivo.
function ArquitecturaEquivocada(): Boolean;
begin
  Result := ('{#Arch}' = '386') and IsWin64 and
            (not RegKeyExists(HKEY_LOCAL_MACHINE, 'SOFTWARE\ObserverRMM'));
end;

// Inno llama a DeinitializeSetup en TODA salida, también cuando InitializeSetup
// devuelve False. Este flag es cómo se entera de que no hubo instalación.
var
  GuardAborto: Boolean;

function InitializeSetup(): boolean;
var
  ResultCode: Integer;
begin
  if ArquitecturaEquivocada() then
  begin
    GuardAborto := True;
    Log('ABORTADO: instalador de 32 bits (386) sobre un Windows de 64 bits, y sin instalacion previa.');
    SuppressibleMsgBox(
      'Este es el instalador de 32 bits (386) y este equipo es de 64 bits.' + #13#10 + #13#10 +
      'Instale la version de 64 bits (amd64) del agente Observer RMM.' + #13#10 + #13#10 +
      'Instalar la version de 32 bits aca deja el equipo en un estado que se ve' + #13#10 +
      'correcto pero no lo esta: las actualizaciones no surten efecto y el' + #13#10 +
      'inventario de software queda incompleto.',
      mbCriticalError, MB_OK, IDOK);
    Result := False;
    Exit;
  end;

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
  // Sin esto, abortar el guard de arquitectura termina en un "Runtime error" crudo
  // en la cara del usuario, justo DESPUÉS del mensaje que le explica qué hacer.
  // Medido el 2026-08-09 en log-arch-guard.txt (corrida 31291029249):
  //
  //   InitializeSetup returned False; aborting.
  //   Got EAbort exception.
  //   Deinitializing Setup.
  //   DeinitializeSetup raised an exception.
  //   Runtime error (at 9:51):
  //   Internal error: An attempt was made to expand the "app" constant before it
  //   was initialized.
  //
  // Porque Inno llama a este procedimiento igual, y ahí abajo {app} nunca se
  // inicializó: no hubo wizard. Nada que instalar, nada que arrancar.
  //
  // El flag se pone SOLO en el camino del guard, a propósito. Que este
  // procedimiento asuma que hubo instalación es un supuesto más amplio —también
  // corre si el usuario cancela— pero ESO NO ESTÁ MEDIDO, y acá el que decide es
  // el que atraviesa toda instalación y actualización de la flota Windows: se
  // toca lo que se probó y nada más.
  if GuardAborto then
    Exit;

  // {app} y NO el literal '{sd}\Program Files\ObserverAgent'. El agente resuelve
  // su directorio de instalación a partir del ejecutable que corre, así que este
  // WorkingDir es lo que le dice dónde quedó instalado. Con el literal, un
  // instalador de 32 bits sobre x64 le mentía: el usuario puede haber elegido
  // otro directorio, y en una actualización manda el directorio previo
  // (UsePreviousAppDir), no este valor.
  WorkingDir := ExpandConstant('{app}');
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
