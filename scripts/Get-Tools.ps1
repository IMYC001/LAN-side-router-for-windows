$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$tools = Join-Path $projectRoot '.tools'
New-Item -ItemType Directory -Path $tools -Force | Out-Null

function Install-Archive($Url, $Hash, $Algorithm, $Archive, $Destination, $ExpectedFile) {
    if (Test-Path -LiteralPath $ExpectedFile) { return }
    $archivePath = Join-Path $tools $Archive
    if (-not (Test-Path -LiteralPath $archivePath)) {
        Write-Host "Downloading $Archive"
        Invoke-WebRequest -UseBasicParsing $Url -OutFile $archivePath
    }
    $actual = (Get-FileHash -LiteralPath $archivePath -Algorithm $Algorithm).Hash
    if ($actual -ine $Hash) { throw "Checksum mismatch for $Archive" }
    New-Item -ItemType Directory -Path $Destination -Force | Out-Null
    Expand-Archive -LiteralPath $archivePath -DestinationPath $Destination -Force
}

$dotnetRelease = Invoke-RestMethod 'https://dotnetcli.blob.core.windows.net/dotnet/release-metadata/10.0/releases.json'
$sdk = $dotnetRelease.releases | Where-Object { $_.sdk.version -eq '10.0.401' } | Select-Object -First 1
if (-not $sdk) { throw 'Pinned .NET SDK was not found in official metadata' }
$sdkArchive = $sdk.sdk.files | Where-Object { $_.rid -eq 'win-x64' -and $_.url.EndsWith('.zip') } | Select-Object -First 1
Install-Archive $sdkArchive.url $sdkArchive.hash 'SHA512' 'dotnet-sdk.zip' (Join-Path $tools 'dotnet') (Join-Path $tools 'dotnet\dotnet.exe')
Install-Archive 'https://go.dev/dl/go1.27.2.windows-amd64.zip' '1314008898bd40df77af4b014f777f08873dbdfbcd3d92308728ee03304fe04f' 'SHA256' 'go-sdk.zip' $tools (Join-Path $tools 'go\bin\go.exe')
Write-Host 'Portable tools ready. No system SDK, PATH, driver or network settings were changed.'
