param([switch]$SkipTests, [switch]$SmokeTest, [string]$OutputName = 'Accelerator')
$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$go = Join-Path $projectRoot '.tools\go\bin\go.exe'
$dotnet = Join-Path $projectRoot '.tools\dotnet\dotnet.exe'
if (-not (Test-Path -LiteralPath $go) -or -not (Test-Path -LiteralPath $dotnet)) {
    throw 'Run scripts\Get-Tools.ps1 first.'
}
$env:GOTOOLCHAIN = 'local'
$env:GOPATH = Join-Path $projectRoot 'engine\.tools\gopath'
$env:GOCACHE = Join-Path $projectRoot 'engine\.tools\gocache'
$env:DOTNET_CLI_HOME = Join-Path $projectRoot '.tools\dotnet-home'
$env:DOTNET_SKIP_FIRST_TIME_EXPERIENCE = '1'
$env:DOTNET_CLI_TELEMETRY_OPTOUT = '1'
$env:DOTNET_GENERATE_ASPNET_CERTIFICATE = 'false'
if ($OutputName -notmatch '^Accelerator(?:-[A-Za-z0-9.-]+)?$') { throw 'Invalid output directory name.' }
$destination = Join-Path $projectRoot "dist\$OutputName"
$lockDirectory = Join-Path $projectRoot '.state'
New-Item -ItemType Directory -Path $lockDirectory -Force | Out-Null
$lockPath = Join-Path $lockDirectory "build-$OutputName.lock"
$releaseLock = [IO.File]::Open($lockPath, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
try {
$active = Get-CimInstance Win32_Process | Where-Object {
    $_.ExecutablePath -and $_.ExecutablePath.StartsWith($destination + '\', [StringComparison]::OrdinalIgnoreCase)
}
if ($active) { throw 'This release is running. Use another OutputName; do not interrupt an active gateway.' }
New-Item -ItemType Directory -Path $destination -Force | Out-Null
$oldChecks = Join-Path $destination 'validation\desktop-checks.json'
if (Test-Path -LiteralPath $oldChecks) { Remove-Item -LiteralPath $oldChecks }
Push-Location (Join-Path $projectRoot 'engine')
try {
    if (-not $SkipTests) {
        & $go test -timeout 45s ./...
        if ($LASTEXITCODE -ne 0) { throw 'Engine tests failed.' }
        & $go vet ./...
        if ($LASTEXITCODE -ne 0) { throw 'Engine static checks failed.' }
    }
    & $go build -trimpath -ldflags '-s -w' -o (Join-Path $destination 'accelerator-engine.exe') ./cmd/accelerator-engine
    if ($LASTEXITCODE -ne 0) { throw 'Engine build failed.' }
    $modules = & $go list -m -f '{{.Path}}|{{.Dir}}' all
    if ($LASTEXITCODE -ne 0) { throw 'Dependency inventory failed.' }
    $licenses = Join-Path $destination 'licenses'
    New-Item -ItemType Directory -Path $licenses -Force | Out-Null
    foreach ($module in $modules) {
        $parts = $module.Split('|', 2)
        if ($parts.Length -ne 2 -or -not $parts[1]) { continue }
        $notices = @(Get-ChildItem -LiteralPath $parts[1] -File | Where-Object { $_.Name -match '^(LICENSE|COPYING|NOTICE)' })
        if ($notices.Count -eq 0) { continue }
        $moduleDestination = Join-Path $licenses ($parts[0] -replace '[^A-Za-z0-9._-]', '_')
        New-Item -ItemType Directory -Path $moduleDestination -Force | Out-Null
        foreach ($notice in $notices) {
            Copy-Item -LiteralPath $notice.FullName -Destination (Join-Path $moduleDestination $notice.Name) -Force
        }
    }
}
finally { Pop-Location }
& $dotnet publish (Join-Path $projectRoot 'src\Accelerator.App\Accelerator.App.csproj') -c Release -r win-x64 --self-contained true -p:PublishSingleFile=false -p:DebugType=None -p:DebugSymbols=false --nologo -o $destination
if ($LASTEXITCODE -ne 0) { throw 'Desktop build failed.' }
Copy-Item -LiteralPath (Join-Path $projectRoot 'README.md') -Destination (Join-Path $destination 'README.md')
foreach ($document in @('README.en.md', 'LICENSE', 'CHANGELOG.md', 'CONTRIBUTING.md', 'SECURITY.md')) {
    Copy-Item -LiteralPath (Join-Path $projectRoot $document) -Destination (Join-Path $destination $document)
}
Copy-Item -LiteralPath (Join-Path $projectRoot 'THIRD-PARTY-NOTICES.md') -Destination (Join-Path $destination 'THIRD-PARTY-NOTICES.md')
Copy-Item -LiteralPath (Join-Path $projectRoot '.tools\dotnet\LICENSE.txt') -Destination (Join-Path $destination 'licenses\dotnet-LICENSE.txt')
Copy-Item -LiteralPath (Join-Path $projectRoot '.tools\dotnet\ThirdPartyNotices.txt') -Destination (Join-Path $destination 'licenses\dotnet-ThirdPartyNotices.txt')
Copy-Item -LiteralPath (Join-Path $projectRoot '.tools\go\LICENSE') -Destination (Join-Path $destination 'licenses\go-LICENSE')
$mihomo = Join-Path $projectRoot '.tools\mihomo\mihomo.exe'
if (-not (Test-Path -LiteralPath $mihomo)) { throw 'Run scripts\Get-Mihomo.ps1 first.' }
Copy-Item -LiteralPath $mihomo -Destination (Join-Path $destination 'mihomo.exe')
Copy-Item -LiteralPath (Join-Path $projectRoot '.tools\mihomo\LICENSE') -Destination (Join-Path $destination 'licenses\mihomo-LICENSE')
$source = Join-Path $projectRoot '.tools\mihomo-v1.19.32-source.zip'
if (-not (Test-Path -LiteralPath $source) -or
    (Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash -ine '135f16d1e309f8c0ce2303c86fd38a38624a229e7185ceb74d1d3a4f0d4b66f1') {
    throw 'Run scripts\Get-Mihomo.ps1 to obtain the pinned source archive.'
}
New-Item -ItemType Directory -Path (Join-Path $destination 'sources') -Force | Out-Null
Copy-Item -LiteralPath $source -Destination (Join-Path $destination 'sources\mihomo-v1.19.32-source.zip')
New-Item -ItemType Directory -Path (Join-Path $destination 'docs') -Force | Out-Null
foreach ($document in @('PORTABILITY.md', 'INSTALLATION.md', 'BUILDING.md', 'TEST-REPORT.md')) {
    Copy-Item -LiteralPath (Join-Path $projectRoot "docs\$document") -Destination (Join-Path $destination "docs\$document")
}
foreach ($folder in @('images', 'releases')) {
    $target = Join-Path $destination "docs\$folder"
    New-Item -ItemType Directory -Path $target -Force | Out-Null
    Get-ChildItem -LiteralPath (Join-Path $projectRoot "docs\$folder") -File |
        Copy-Item -Destination $target -Force
}
if ($SmokeTest) {
    $process = Start-Process -FilePath (Join-Path $destination 'Accelerator.exe') -ArgumentList '--smoke-test' -WindowStyle Hidden -PassThru
    if (-not $process.WaitForExit(60000)) { $process.Kill(); throw 'UI rendering timed out.' }
    if ($process.ExitCode -ne 0) { throw 'UI rendering failed.' }
    $checks = Join-Path $projectRoot 'test-results\desktop-checks.json'
    if (-not (Test-Path -LiteralPath $checks)) { throw 'Desktop verification report missing.' }
    $report = Get-Content -LiteralPath $checks -Raw | ConvertFrom-Json
    if (-not $report.passed -or $report.version -ne '0.5.13' -or -not $report.subscriptionFetchChecksPassed -or -not $report.subscriptionTlsChecksPassed -or
        -not $report.languageChecksPassed -or -not $report.languageRestartChecksPassed -or -not $report.aboutChecksPassed -or $report.physicalGatewayStarted) {
        throw 'Desktop verification report is invalid.'
    }
    Copy-Item -LiteralPath $checks -Destination (Join-Path $projectRoot "test-results\$OutputName-checks.json")
}
Write-Host "Ready: $destination\Accelerator.exe"
}
finally {
    $releaseLock.Dispose()
    Remove-Item -LiteralPath $lockPath -ErrorAction SilentlyContinue
}
