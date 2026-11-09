#define AppName "Work Anywhere"
#define AppPublisher "Work Anywhere Project"
#define AppExeName "work-anywhere.exe"
#ifndef AppVersion
  #define AppVersion "0.1.0"
#endif
#ifndef AppFileVersion
  #define AppFileVersion "0.1.0.0"
#endif
#ifndef AppCommit
  #define AppCommit "unknown"
#endif
#ifndef AppBuildDate
  #define AppBuildDate "unknown"
#endif

[Setup]
AppId={{A0B3A2A8-1B37-4B5D-9F8C-6A20E0D4B2D1}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
AppPublisherURL=https://github.com/hurf/work-anywhere
DefaultDirName={localappdata}\Programs\WorkAnywhere
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64
ArchitecturesInstallIn64BitMode=x64
OutputDir=..\..\dist\windows-amd64
OutputBaseFilename=work-anywhere-setup-{#AppVersion}
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
UninstallDisplayName={#AppName}
UninstallDisplayIcon={app}\{#AppExeName}
VersionInfoVersion={#AppFileVersion}
VersionInfoCompany={#AppPublisher}
VersionInfoDescription={#AppName} installer
VersionInfoProductName={#AppName}
VersionInfoProductVersion={#AppFileVersion}
VersionInfoOriginalFileName=work-anywhere-setup-{#AppVersion}.exe
CloseApplications=no
OutputManifestFile=yes

[Tasks]
Name: "autostart"; Description: "登录 Windows 时启动 Work Anywhere"
Name: "codex"; Description: "集成 ChatGPT Codex Desktop（写入 CODEX_CLI_PATH，需重启 ChatGPT Desktop）"; Flags: unchecked

[Files]
Source: "..\..\dist\windows-amd64\work-anywhere.exe"; DestDir: "{app}"; Flags: ignoreversion

[Registry]
; Written by Setup itself (same value as `autostart enable`) so login autostart
; does not depend on launching the freshly installed, unsigned EXE.
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "WorkAnywhere"; ValueData: """{app}\{#AppExeName}"" daemon start --background"; Tasks: autostart; Flags: uninsdeletevalue

[Icons]
Name: "{group}\Work Anywhere"; Filename: "{app}\{#AppExeName}"; Parameters: "ui"; WorkingDir: "{app}"
Name: "{group}\卸载 Work Anywhere"; Filename: "{uninstallexe}"

[Run]
Filename: "{app}\{#AppExeName}"; Parameters: "daemon start --background"; WorkingDir: "{app}"; Description: "立即启动 Work Anywhere"; Flags: postinstall nowait runhidden

[UninstallRun]
Filename: "{app}\{#AppExeName}"; Parameters: "daemon stop"; WorkingDir: "{app}"; Flags: runhidden waituntilterminated skipifdoesntexist; RunOnceId: "StopWorkAnywhereDaemon"
Filename: "{app}\{#AppExeName}"; Parameters: "autostart disable"; WorkingDir: "{app}"; Flags: runhidden waituntilterminated skipifdoesntexist; RunOnceId: "DisableWorkAnywhereAutostart"
Filename: "{app}\{#AppExeName}"; Parameters: "uninstall"; WorkingDir: "{app}"; Flags: runhidden waituntilterminated skipifdoesntexist; RunOnceId: "RestoreCodexCliPath"

[Code]
var
  CodexDefaultApplied: Boolean;

function CodexDesktopDetected(): Boolean;
begin
  Result := DirExists(ExpandConstant('{localappdata}\OpenAI\Codex'));
end;

function RunApp(const Parameters: String; var Detail: String): Boolean;
var
  ResultCode: Integer;
begin
  Detail := '';
  if not Exec(ExpandConstant('{app}\{#AppExeName}'), Parameters, ExpandConstant('{app}'), SW_HIDE, ewWaitUntilTerminated, ResultCode) then
  begin
    Detail := '无法启动程序：' + SysErrorMessage(ResultCode) + ' (' + IntToStr(ResultCode) + ')';
    Result := False;
  end
  else if ResultCode <> 0 then
  begin
    Detail := '退出码 ' + IntToStr(ResultCode);
    Result := False;
  end
  else
    Result := True;
  if not Result then
    Log('work-anywhere.exe ' + Parameters + ' failed: ' + Detail);
end;

procedure CurPageChanged(CurPageID: Integer);
begin
  { Pre-select Codex integration only on a fresh install where ChatGPT Codex
    Desktop is present; upgrades keep the previously chosen tasks. }
  if (CurPageID = wpSelectTasks) and not CodexDefaultApplied then
  begin
    CodexDefaultApplied := True;
    if (GetPreviousData('CodexTaskChosen', '') = '') and CodexDesktopDetected() then
      WizardSelectTasks('codex');
  end;
end;

procedure RegisterPreviousData(PreviousDataKey: Integer);
begin
  SetPreviousData(PreviousDataKey, 'CodexTaskChosen', '1');
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ExistingExe: String;
  ResultCode: Integer;
begin
  Result := '';
  { Stop a running daemon so its executable can be replaced. A Desktop Bridge
    started by ChatGPT Desktop may still hold the file; Setup then shows its
    retry dialog and the user can quit ChatGPT Desktop before retrying. }
  ExistingExe := ExpandConstant('{app}\{#AppExeName}');
  if FileExists(ExistingExe) then
    Exec(ExistingExe, 'daemon stop', ExpandConstant('{app}'), SW_HIDE, ewWaitUntilTerminated, ResultCode);
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  Problems: String;
  Detail: String;
begin
  if CurStep <> ssPostInstall then
    exit;
  Problems := '';
  { The autostart Run value itself is written by the [Registry] section; an
    unchecked task removes an entry left by a previous install or the UI. }
  if not WizardIsTaskSelected('autostart') then
    RegDeleteValue(HKEY_CURRENT_USER, 'Software\Microsoft\Windows\CurrentVersion\Run', 'WorkAnywhere');
  if WizardIsTaskSelected('codex') then
  begin
    if RunApp('install', Detail) then
      SuppressibleMsgBox('ChatGPT Codex Desktop 集成已启用。' + #13#10#13#10 +
        '请完全退出并重新启动所有 ChatGPT Desktop 实例，使 CODEX_CLI_PATH 配置生效。', mbInformation, MB_OK, IDOK)
    else
      Problems := Problems + #13#10 + 'CODEX_CLI_PATH 写入失败（' + Detail + '），可稍后执行：' + #13#10 + ExpandConstant('{app}\{#AppExeName}') + ' install';
  end;
  if Problems <> '' then
    SuppressibleMsgBox('程序文件已安装，但以下步骤未完成：' + #13#10 + Problems, mbError, MB_OK, IDOK);
end;
