param([string]$OutputName = 'Accelerator-0.5.14', [switch]$SkipAppBuild)
$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
if ($OutputName -notmatch '^Accelerator(?:-[A-Za-z0-9.-]+)?$') { throw 'Invalid release directory.' }
$release = Join-Path $root "dist\$OutputName"
$assets = Join-Path $root '.tools\installer'
$compiler = Join-Path $root '.tools\inno\ISCC.exe'
if (-not $SkipAppBuild) {
    & (Join-Path $PSScriptRoot 'Build.ps1') -OutputName $OutputName -SmokeTest
}
if (-not (Test-Path -LiteralPath $compiler)) { throw 'Run scripts\Get-InstallerTools.ps1 first.' }
$lockDirectory = Join-Path $root '.state'
New-Item -ItemType Directory -Path $lockDirectory -Force | Out-Null
$lockPath = Join-Path $lockDirectory "build-$OutputName.lock"
$releaseLock = [IO.File]::Open($lockPath, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
try {
$executable = Join-Path $release 'Accelerator.exe'
if ((Get-Item -LiteralPath $executable).VersionInfo.FileVersion -ne '0.5.14.0') {
    throw 'Installer requires release 0.5.14.'
}
$checks = Get-Content -LiteralPath (Join-Path $root "test-results\$OutputName-checks.json") -Raw | ConvertFrom-Json
if (-not $checks.passed -or $checks.version -ne '0.5.14' -or -not $checks.subscriptionFetchChecksPassed -or -not $checks.subscriptionTlsChecksPassed -or
    -not $checks.languageChecksPassed -or -not $checks.languageRestartChecksPassed -or -not $checks.aboutChecksPassed -or $checks.physicalGatewayStarted) {
    throw 'Valid offline desktop checks are required.'
}
if ($checks.appAssemblySHA256 -ine (Get-FileHash -LiteralPath (Join-Path $release 'Accelerator.dll') -Algorithm SHA256).Hash -or
    $checks.engineSHA256 -ine (Get-FileHash -LiteralPath (Join-Path $release 'accelerator-engine.exe') -Algorithm SHA256).Hash -or
    -not $checks.uninstallReparseFixturePassed) {
    throw 'Desktop checks do not match these binaries or uninstall link protection did not pass.'
}
$driver = Join-Path $assets 'Windows.Packet.Filter.3.6.2.1.x64.msi'
if ((Get-FileHash -LiteralPath $driver -Algorithm SHA256).Hash -ine '9c388c0b7f189f7fa98720bae2caecf7d64f30910838b80b438ecf8956b8502c' -or
    (Get-AuthenticodeSignature -LiteralPath $driver).Status -ne 'Valid') {
    throw 'Driver package verification failed.'
}
$eula = Get-Content -LiteralPath (Join-Path $assets 'Windows-Packet-Filter-EULA.rtf') -Raw
if (-not $eula.Contains('may be included in any free or non-profit packages')) {
    throw 'Non-commercial redistribution clause missing.'
}
Copy-Item -LiteralPath (Join-Path $root '.tools\inno\license.txt') -Destination (Join-Path $release 'licenses\inno-setup-LICENSE.txt')
$inspectionPath = Join-Path $root 'test-results\installer-checks.json'
if (Test-Path -LiteralPath $inspectionPath) { Remove-Item -LiteralPath $inspectionPath }
& $compiler '/Q' ("/DReleaseDir=$release") ("/DInstallerAssets=$assets") (Join-Path $root 'installer\Accelerator.iss')
if ($LASTEXITCODE -ne 0) { throw 'Installer compilation failed.' }
$setup = Join-Path $root 'dist\Accelerator-0.5.14-Setup-win-x64.exe'
$inspection = [ordered]@{
    version = '0.5.14'
    generated = [DateTime]::UtcNow.ToString('o')
    installerSHA256 = (Get-FileHash -LiteralPath $setup -Algorithm SHA256).Hash
    installerBytes = (Get-Item -LiteralPath $setup).Length
    driverSHA256 = (Get-FileHash -LiteralPath $driver -Algorithm SHA256).Hash
    driverSignature = 'Valid'
    license = 'Personal/free/non-profit; commercial redistribution requires vendor permission'
    desktopChecksPassed = $true
    installerWizardPreviewPassed = $false
    realInstallationTested = $false
    realUninstallTested = $false
    networkChanged = $false
}
$inspection | ConvertTo-Json | Set-Content -LiteralPath $inspectionPath -Encoding UTF8
Write-Host "Ready: $setup"
Write-Host 'Built only. Real installation/uninstall and cross-PC packet forwarding remain untested.'
}
finally {
    $releaseLock.Dispose()
    Remove-Item -LiteralPath $lockPath -ErrorAction SilentlyContinue
}
