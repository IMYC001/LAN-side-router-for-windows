#ifndef ReleaseDir
  #error ReleaseDir must be supplied by Build-Installer.ps1
#endif
#ifndef InstallerAssets
  #error InstallerAssets must be supplied by Build-Installer.ps1
#endif

[Setup]
AppId={{9ECBE427-356D-41FA-AFE7-E835A85CC1D4}
AppName=局域网设备加速（旁路由模式）
AppVersion=0.5.14
VersionInfoVersion=0.5.14.0
VersionInfoTextVersion=0.5.14.0
AppPublisher=IMYC001
AppPublisherURL=https://github.com/IMYC001/LAN-side-router-for-windows
AppSupportURL=https://github.com/IMYC001/LAN-side-router-for-windows/issues
AppUpdatesURL=https://github.com/IMYC001/LAN-side-router-for-windows/releases
VersionInfoDescription=局域网设备加速（旁路由模式）安装程序
VersionInfoProductName=局域网设备加速器
DefaultDirName={localappdata}\Programs\LANDeviceAccelerator
DefaultGroupName=局域网设备加速
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64os
ArchitecturesInstallIn64BitMode=x64os
MinVersion=10.0.19041
OutputDir=..\dist
OutputBaseFilename=Accelerator-0.5.14-Setup-win-x64
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
SetupMutex=Local\LANDeviceAccelerator-Setup
CloseApplications=no
RestartApplications=no
UninstallDisplayIcon={app}\Accelerator.exe
SetupIconFile=..\src\Accelerator.App\Assets\accelerator.ico
InfoBeforeFile=NOTICE.txt
LicenseFile={#InstallerAssets}\Windows-Packet-Filter-EULA.rtf
DisableWelcomePage=no
DisableReadyPage=no
Uninstallable=yes
UninstallFilesDir={app}\uninstall

[Languages]
Name: "chinesesimplified"; MessagesFile: "{#InstallerAssets}\ChineseSimplified.isl"

[Tasks]
Name: "desktopicon"; Description: "创建桌面快捷方式"; Flags: unchecked

[Files]
Source: "{#ReleaseDir}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "{#InstallerAssets}\Windows.Packet.Filter.3.6.2.1.x64.msi"; DestDir: "{app}\drivers"; Flags: ignoreversion
Source: "{#InstallerAssets}\Windows-Packet-Filter-EULA.rtf"; DestDir: "{app}\licenses"; Flags: ignoreversion
Source: "NOTICE.txt"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{userprograms}\局域网设备加速"; Filename: "{app}\Accelerator.exe"; WorkingDir: "{app}"; Comment: "局域网设备加速（旁路由模式）"
Name: "{userdesktop}\局域网设备加速"; Filename: "{app}\Accelerator.exe"; WorkingDir: "{app}"; Tasks: desktopicon; Comment: "局域网设备加速（旁路由模式）"

[Registry]
Root: HKCU; Subkey: "Software\LANDeviceAccelerator"; Flags: uninsdeletekeyifempty
Root: HKCU; Subkey: "Software\LANDeviceAccelerator\Installer"; ValueType: string; ValueName: "Owner"; ValueData: "{code:GetOwner}"; Flags: uninsdeletekey

[Code]
const
  InstallerKey = 'Software\LANDeviceAccelerator\Installer';
  DriverService = 'SYSTEM\CurrentControlSet\Services\ndisrd';
  DriverProduct = 'SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{4EC4289E-7F4B-424C-8EA9-B7AC9850FFBE}';
var
  Owner: String;
  InstallDriver: Boolean;
  RebootRequired: Boolean;
  PreviewOnly: Boolean;
  DriverPage: TInputOptionWizardPage;

function GetOwner(Param: String): String;
begin
  Result := Owner;
end;

function ExistingBackend: Boolean;
begin
  Result := FileExists(ExpandConstant('{sys}\Npcap\wpcap.dll')) or
    RegKeyExists(HKLM64, DriverService) or RegKeyExists(HKLM64, DriverProduct);
end;

function Idle: Boolean;
var
  ExitCode: Integer;
begin
  Result := Exec(ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe'),
    '-NoLogo -NoProfile -NonInteractive -Command "if (@(Get-Process -Name Accelerator,accelerator-engine -ErrorAction SilentlyContinue).Count -gt 0) { exit 10 }; exit 0"',
    '', SW_HIDE, ewWaitUntilTerminated, ExitCode) and (ExitCode = 0);
end;

procedure InitializeWizard;
var
  Raw: String;
begin
  PreviewOnly := ExpandConstant('{param:PREVIEWONLY|0}') = '1';
  if not RegQueryStringValue(HKCU, InstallerKey, 'Owner', Owner) then
  begin
    Raw := Copy(GetSHA256OfUnicodeString(ExpandConstant('{localappdata}') +
      GetDateTimeString('yyyymmddhhnnsszzz', #0, #0)), 1, 32);
    Owner := Copy(Raw, 1, 8) + '-' + Copy(Raw, 9, 4) + '-' + Copy(Raw, 13, 4) +
      '-' + Copy(Raw, 17, 4) + '-' + Copy(Raw, 21, 12);
  end;
  InstallDriver := not ExistingBackend;
  DriverPage := CreateInputOptionPage(wpSelectTasks,
    '安装必要的网络驱动', '可能短暂断网，请先保存其他工作',
    '这台电脑没有可用驱动。安装程序会请求管理员权限，安装包内的原厂 Windows Packet Filter。' +
    ' ' + #13#10 + '驱动安装可能短暂断网或要求重启，不会修改 Clash 设置。', False, False);
  DriverPage.Add('我已保存工作，同意安装驱动并接受可能的短暂断网');
  DriverPage.Values[0] := False;
  if PreviewOnly then Log('Installer preview: initialized; installation disabled.');
end;

procedure CurPageChanged(CurPageID: Integer);
begin
  if PreviewOnly and (CurPageID = wpReady) then
    Log('Installer preview: ready page reached.');
end;

function ShouldSkipPage(PageID: Integer): Boolean;
begin
  Result := (PageID = DriverPage.ID) and not InstallDriver;
end;

function NextButtonClick(CurPageID: Integer): Boolean;
begin
  Result := True;
  if (CurPageID = DriverPage.ID) and InstallDriver and not DriverPage.Values[0] then
  begin
    MsgBox('需要确认驱动安装后才能继续。', mbInformation, MB_OK);
    Result := False;
  end;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  if PreviewOnly then
  begin
    Result := '当前为安装界面验证模式，不会执行安装。请取消并退出。';
    Exit;
  end;
  Result := '';
  if not Idle then
    Result := '请先停止加速并退出软件（包括托盘），将设备恢复原来的 IP 和 DNS 设置，然后重试。'
  else if InstallDriver and ExistingBackend then
    InstallDriver := False;
  if (Result = '') and InstallDriver and WizardSilent and
    (ExpandConstant('{param:ALLOWDRIVERCHANGE|0}') <> '1') then
    Result := '静默安装未获准改动网络驱动，请使用交互安装并确认网络中断提示。';
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  ExitCode: Integer;
begin
  if (CurStep = ssPostInstall) and InstallDriver then
  begin
    { Retain ownership even if MSI partially fails, so uninstall can recover. }
    RegWriteStringValue(HKCU, InstallerKey, 'DriverOwner', Owner);
    if not ShellExec('runas', ExpandConstant('{app}\Accelerator.exe'),
      '--installer-install-driver "' + Owner + '"', ExpandConstant('{app}'),
      SW_HIDE, ewWaitUntilTerminated, ExitCode) then
      RaiseException('驱动安装未启动或管理员授权被取消。软件已安装，可重新运行安装器或从系统设置卸载。');
    if ExitCode = 3010 then RebootRequired := True
    else if ExitCode <> 0 then
      RaiseException('驱动安装尚未完成。请按维护窗口提示处理，再重新安装或卸载。');
  end;
end;

function NeedRestart: Boolean;
begin
  Result := RebootRequired;
end;

function InitializeUninstall: Boolean;
var
  DriverOwner: String;
  Choice, ExitCode: Integer;
  Command: String;
begin
  Result := False;
  CreateMutex('Local\LANDeviceAccelerator-Setup');
  if not Idle then
  begin
    MsgBox('请先停止加速并退出软件（包括托盘），将设备恢复原来的 IP 和 DNS 设置，然后重试卸载。',
      mbInformation, MB_OK);
    Exit;
  end;
  if UninstallSilent then
  begin
    MsgBox('为避免意外删除订阅或共享驱动，请使用交互卸载。', mbInformation, MB_OK);
    Exit;
  end;
  if MsgBox('卸载会删除当前账户的全部订阅、备份和临时配置。' + #13#10 +
    '请确认设备已恢复原来的 IP 和 DNS 设置。继续卸载？',
    mbConfirmation, MB_YESNO or MB_DEFBUTTON2) <> IDYES then Exit;
  if RegQueryStringValue(HKCU, InstallerKey, 'DriverOwner', DriverOwner) then
  begin
    Choice := MsgBox('是否同时卸载本软件安装的 Windows Packet Filter 驱动？' + #13#10 +
      '卸载驱动可能短暂断网，需要管理员权限，也可能需要重启。' + #13#10 +
      '若其他软件或账户已共用该驱动，请选择“否”保留；选择“取消”停止卸载。',
      mbConfirmation, MB_YESNOCANCEL or MB_DEFBUTTON3);
    if Choice = IDCANCEL then Exit;
    if Choice = IDYES then Command := '--installer-remove-driver'
    else Command := '--installer-release-driver';
    if not ShellExec('runas', ExpandConstant('{app}\Accelerator.exe'),
      Command + ' "' + DriverOwner + '"', ExpandConstant('{app}'),
      SW_HIDE, ewWaitUntilTerminated, ExitCode) then
    begin
      MsgBox('未完成管理员授权，已停止卸载。', mbInformation, MB_OK);
      Exit;
    end;
    if ExitCode = 3010 then RebootRequired := True
    else if ExitCode <> 0 then
    begin
      MsgBox('驱动处理尚未完成，已保留软件及卸载入口。请按维护窗口提示处理后重试。',
        mbError, MB_OK);
      Exit;
    end;
    RegDeleteValue(HKCU, InstallerKey, 'DriverOwner');
  end;
  if not Exec(ExpandConstant('{app}\Accelerator.exe'), '--installer-clean-data',
    ExpandConstant('{app}'), SW_HIDE, ewWaitUntilTerminated, ExitCode) or (ExitCode <> 0) then
  begin
    MsgBox('设置文件清理尚未完成，已保留软件及卸载入口。请关闭占用文件的程序后重试。',
      mbError, MB_OK);
    Exit;
  end;
  Result := True;
end;

function UninstallNeedRestart: Boolean;
begin
  Result := RebootRequired;
end;
