#define MyAppName "Observer RMM Agent"
#define MyAppVersion "2.10.0"
#define MyAppPublisher "BRAINCORP OP SPA"
#define MyAppURL "https://github.com/braincorp"
#define MyAppExeName "observeragent.exe"
#define MESHEXE "meshagent.exe"
#define MESHDIR "{sd}\Program Files\Mesh Agent"

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
VersionInfoVersion=2.10.0.0
VersionInfoOriginalFileName=observeragent.exe
AppCopyright="Copyright (C) 2025 {#MyAppPublisher}"

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Files]
Source: "C:\Users\Public\Documents\agent\observeragent.exe"; DestDir: "{app}"; Flags: ignoreversion;

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
