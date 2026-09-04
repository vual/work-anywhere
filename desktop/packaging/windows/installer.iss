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
VersionInfoVersion={#AppFileVersion}
VersionInfoCompany={#AppPublisher}
VersionInfoDescription={#AppName} Desktop Bridge and daemon installer
VersionInfoProductName={#AppName}
VersionInfoProductVersion={#AppFileVersion}
VersionInfoOriginalFileName=work-anywhere-setup-{#AppVersion}.exe
CloseApplications=no
OutputManifestFile=yes

[Files]
Source: "..\..\dist\windows-amd64\work-anywhere.exe"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\Work Anywhere 管理页"; Filename: "{app}\{#AppExeName}"; Parameters: "ui"; WorkingDir: "{app}"
Name: "{group}\卸载 Work Anywhere"; Filename: "{uninstallexe}"

[UninstallRun]
Filename: "{app}\{#AppExeName}"; Parameters: "daemon stop"; WorkingDir: "{app}"; Flags: runhidden waituntilterminated skipifdoesntexist; RunOnceId: "StopWorkAnywhereDaemon"
Filename: "{app}\{#AppExeName}"; Parameters: "uninstall"; WorkingDir: "{app}"; Flags: runhidden waituntilterminated skipifdoesntexist; RunOnceId: "RestoreCodexCliPath"

[Code]
function InstallBridge(): Boolean;
var
  ResultCode: Integer;
begin
  Result := Exec(ExpandConstant('{app}\{#AppExeName}'), 'install', ExpandConstant('{app}'), SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Result := Result and (ResultCode = 0);
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
  begin
    if InstallBridge() then
    begin
      MsgBox('安装完成。' + #13#10#13#10 +
        '请完全退出并重新启动所有 ChatGPT Desktop 实例，' +
        '使 CODEX_CLI_PATH 配置生效。', mbInformation, MB_OK);
    end
    else
    begin
      MsgBox('程序文件已安装，但 CODEX_CLI_PATH 写入失败。' + #13#10#13#10 +
        '请关闭安装器后，以当前用户执行：' + #13#10 +
        ExpandConstant('{app}\{#AppExeName}') + ' install', mbError, MB_OK);
    end;
  end;
end;
