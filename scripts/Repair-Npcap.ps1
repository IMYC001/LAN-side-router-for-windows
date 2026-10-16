param([switch]$RemoveLegacyComponent)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$systemDirectory = Join-Path $env:WINDIR 'System32'
$legacyId = 'INSECURE_NPCAP'
$componentRoot = 'HKLM:\SYSTEM\CurrentControlSet\Control\Network\{4d36e974-e325-11ce-bfc1-08002be10318}'

function Get-LegacyNpcap {
    foreach ($key in @(Get-ChildItem -LiteralPath $componentRoot)) {
        $entry = Get-ItemProperty -LiteralPath $key.PSPath
        $component = $entry.PSObject.Properties['ComponentId']
        $description = $entry.PSObject.Properties['Description']
        if ($component -and $component.Value -ieq $legacyId) {
            if (-not $description -or $description.Value -notmatch 'Npcap') {
                throw 'The legacy component description does not match Npcap. Stopping.'
            }
            [pscustomobject]@{
                RegistryPath = $key.Name
                ComponentId = [string]$component.Value
                Description = [string]$description.Value
            }
        }
    }
}

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = [Security.Principal.WindowsPrincipal]::new($identity)
$elevated = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
$components = @(Get-LegacyNpcap)
$bindings = @(Get-NetAdapterBinding -AllBindings | Where-Object {
    $_.ComponentID -ieq $legacyId
} | Select-Object Name,DisplayName,ComponentID,Enabled)
$drivers = @(Get-CimInstance Win32_SystemDriver | Where-Object {
    $_.Name -in @('npcap', 'npf', 'npcap_wifi')
} | Select-Object Name,State,StartMode,PathName)
$sessionManager = Get-ItemProperty -LiteralPath 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager'
$pendingProperty = $sessionManager.PSObject.Properties['PendingFileRenameOperations']
$pendingNpcap = @()
if ($pendingProperty) {
    $pendingNpcap = @($pendingProperty.Value | Where-Object { $_ -match '\\(?:npcap|npf)\.sys|\\Program Files\\Npcap' })
}

$snapshot = [pscustomobject]@{
    Captured = (Get-Date).ToString('o')
    Elevated = $elevated
    ReadOnly = -not $RemoveLegacyComponent
    LegacyComponents = $components
    LegacyBindings = $bindings
    NpcapDrivers = $drivers
    PendingNpcapFileOperations = $pendingNpcap
}

if (-not $RemoveLegacyComponent) {
    $snapshot | ConvertTo-Json -Depth 6
    Write-Host 'INSPECTION ONLY: no repair or cleanup was attempted.'
    if ($components.Count -gt 0) {
        Write-Host 'The obsolete Npcap component is still installed. Restarting alone will not remove it.'
        Write-Host 'Cleanup requires -RemoveLegacyComponent in an administrator session and may interrupt networking.'
    }
    Write-Host 'No driver, binding, registry or network settings changed.'
    return
}

if (-not $elevated) {
    throw 'Administrator approval is required. No cleanup was performed.'
}
if ($components.Count -ne 1) {
    throw 'Expected exactly one INSECURE_NPCAP component. No cleanup was performed.'
}
if (@($drivers | Where-Object State -ne 'Stopped').Count -gt 0) {
    throw 'An Npcap driver is active or changing state. Stop and review before cleanup.'
}

$backupDirectory = Join-Path $projectRoot ('.state\npcap-repair-' + (Get-Date -Format 'yyyyMMdd-HHmmss-fff'))
New-Item -ItemType Directory -Path $backupDirectory | Out-Null
$snapshot | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $backupDirectory 'before.json') -Encoding UTF8
$registryBackup = Join-Path $backupDirectory 'legacy-component.reg'
& (Join-Path $systemDirectory 'reg.exe') export $components[0].RegistryPath $registryBackup /y
if ($LASTEXITCODE -ne 0) {
    throw 'The component record could not be backed up. No cleanup was performed.'
}

# Remove only the obsolete Npcap component; a full network reset would also affect other adapters.
$cleanupOutput = & (Join-Path $systemDirectory 'netcfg.exe') -u $legacyId 2>&1
$cleanupExitCode = $LASTEXITCODE
$cleanupOutput | Set-Content -LiteralPath (Join-Path $backupDirectory 'netcfg-output.txt') -Encoding UTF8
$cleanupOutput | ForEach-Object { Write-Host $_ }
$remainingComponents = @(Get-LegacyNpcap)
$remainingBindings = @(Get-NetAdapterBinding -AllBindings | Where-Object {
    $_.ComponentID -ieq $legacyId
} | Select-Object Name,DisplayName,ComponentID,Enabled)
[pscustomobject]@{
    Captured = (Get-Date).ToString('o')
    CleanupExitCode = $cleanupExitCode
    RemainingLegacyComponents = $remainingComponents
    RemainingLegacyBindings = $remainingBindings
} | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $backupDirectory 'after.json') -Encoding UTF8
Write-Host "Cleanup evidence: $backupDirectory"
if ($cleanupExitCode -notin @(0, 3010)) {
    throw "Legacy Npcap removal failed (exit $cleanupExitCode). Stop; do not reset networking or retry."
}
if ($remainingComponents.Count -ne 0) {
    throw 'The obsolete component is still registered. Stop before installing another driver.'
}
if ($remainingBindings.Count -ne 0) {
    throw 'Legacy adapter bindings remain. Stop and review the saved evidence before installing another driver.'
}

[pscustomobject]@{
    LegacyComponentRemoved = $true
    BackupDirectory = $backupDirectory
    RestartRequiredBeforeInstallation = $true
    AutomaticRestart = $false
    InstallerLaunched = $false
} | ConvertTo-Json
Write-Host 'Stop here. Save work and restart manually before installing Npcap 1.89.'
Write-Host 'The exported registry record is diagnostic evidence, not a complete driver rollback.'
