; Focus Guard installer. Build: ISCC /DAppVersion=1.2.0 installer\focusguard.iss
; Expects dist\focusguard.exe to exist (go build -o dist\focusguard.exe ./cmd/focusguard).
#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif

[Setup]
AppId={{B5E6F9C2-7A1D-4B0E-9C57-3F1D8E2A6C41}
AppName=Focus Guard
AppVersion={#AppVersion}
AppPublisher=Focus Guard
DefaultDirName={autopf}\FocusGuard
DisableProgramGroupPage=yes
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir=..\dist
OutputBaseFilename=FocusGuard-Setup-{#AppVersion}
SetupIconFile=focusguard.ico
UninstallDisplayIcon={app}\focusguard.ico
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
CloseApplications=no
RestartApplications=no
UsePreviousAppDir=yes

[Files]
Source: "..\dist\focusguard.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "focusguard.ico"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{commondesktop}\Focus Guard"; Filename: "{app}\focusguard.exe"; Parameters: "app"; IconFilename: "{app}\focusguard.ico"
Name: "{commonprograms}\Focus Guard"; Filename: "{app}\focusguard.exe"; Parameters: "app"; IconFilename: "{app}\focusguard.ico"

[Registry]
; the tray helper starts for every user at logon; standard users cannot edit HKLM
Root: HKLM; Subkey: "SOFTWARE\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "FocusGuardTray"; ValueData: """{app}\focusguard.exe"" tray"; Flags: uninsdeletevalue

[Run]
Filename: "{app}\focusguard.exe"; Parameters: "install-service"; Flags: runhidden waituntilterminated; StatusMsg: "Starting the Focus Guard service..."
Filename: "{app}\focusguard.exe"; Parameters: "app"; Description: "Open Focus Guard (parent setup)"; Flags: postinstall nowait skipifsilent runasoriginaluser

[UninstallRun]
Filename: "{app}\focusguard.exe"; Parameters: "uninstall-service"; Flags: runhidden waituntilterminated; RunOnceId: "RemoveService"
Filename: "{sys}\taskkill.exe"; Parameters: "/F /IM focusguard-tray.exe"; Flags: runhidden; RunOnceId: "StopTray"

[UninstallDelete]
Type: filesandordirs; Name: "{commonappdata}\FocusGuard"

[Code]
const
  UninstallKey = 'Software\Microsoft\Windows\CurrentVersion\Uninstall\{B5E6F9C2-7A1D-4B0E-9C57-3F1D8E2A6C41}_is1';

// Replacing or removing the program needs the parent: the running service
// asks for the passcode in the dashboard and this waits for the answer.
// The automatic updater passes /FGUPDATE=1 (it only runs outside focus windows).
function ParentApproves(const Dir: String): Boolean;
var
  Code: Integer;
begin
  Result := True;
  if FileExists(Dir + '\focusguard.exe') then
    if not (Exec(Dir + '\focusguard.exe', 'authorize-uninstall', '', SW_HIDE, ewWaitUntilTerminated, Code) and (Code = 0)) then
    begin
      MsgBox('The parent passcode was not entered, so Focus Guard was left as it is.', mbError, MB_OK);
      Result := False;
    end;
end;

function InitializeSetup(): Boolean;
var
  Dir: String;
begin
  Result := True;
  if ExpandConstant('{param:FGUPDATE|0}') = '1' then Exit;
  if RegQueryStringValue(HKLM, UninstallKey, 'InstallLocation', Dir) then
    Result := ParentApproves(RemoveBackslashUnlessRoot(Dir));
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  Code: Integer;
begin
  Result := '';
  if FileExists(ExpandConstant('{app}\focusguard.exe')) then
    Exec(ExpandConstant('{app}\focusguard.exe'), 'stop-service', '', SW_HIDE, ewWaitUntilTerminated, Code);
end;

function InitializeUninstall(): Boolean;
begin
  Result := ParentApproves(ExpandConstant('{app}'));
end;
