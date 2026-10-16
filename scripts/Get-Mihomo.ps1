$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$destination = Join-Path $projectRoot '.tools\mihomo'
$archive = Join-Path $projectRoot '.tools\mihomo-v1.19.32.zip'
$expected = '974a4d7ad69aed27aa2e8f91d61113573c14dadb14562c63e58effabf59816f0'
New-Item -ItemType Directory -Path $destination -Force | Out-Null
if (-not (Test-Path -LiteralPath $archive)) {
    Invoke-WebRequest -UseBasicParsing -Uri 'https://github.com/MetaCubeX/mihomo/releases/download/v1.19.32/mihomo-windows-amd64-compatible-v1.19.32.zip' -OutFile $archive
}
if ((Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash -ine $expected) {
    throw 'Mihomo archive checksum mismatch.'
}
Expand-Archive -LiteralPath $archive -DestinationPath $destination -Force
$binary = Get-ChildItem -LiteralPath $destination -Filter '*.exe' -File | Select-Object -First 1
if (-not $binary) { throw 'Mihomo executable missing from archive.' }
if ($binary.FullName -ne (Join-Path $destination 'mihomo.exe')) {
    Copy-Item -LiteralPath $binary.FullName -Destination (Join-Path $destination 'mihomo.exe') -Force
}
Invoke-WebRequest -UseBasicParsing -Uri 'https://raw.githubusercontent.com/MetaCubeX/mihomo/v1.19.32/LICENSE' -OutFile (Join-Path $destination 'LICENSE')
$source = Join-Path $projectRoot '.tools\mihomo-v1.19.32-source.zip'
$sourceExpected = '135f16d1e309f8c0ce2303c86fd38a38624a229e7185ceb74d1d3a4f0d4b66f1'
if (-not (Test-Path -LiteralPath $source)) {
    Invoke-WebRequest -UseBasicParsing -Uri 'https://codeload.github.com/MetaCubeX/mihomo/zip/refs/tags/v1.19.32' -OutFile $source
}
if ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash -ine $sourceExpected) {
    throw 'Mihomo source archive checksum mismatch.'
}
Write-Host 'Pinned standalone Mihomo core ready. No networking settings changed.'
