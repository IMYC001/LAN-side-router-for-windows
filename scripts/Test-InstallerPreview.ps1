param([switch]$Verify, [string]$Version = '0.5.14')
$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw 'Invalid version.' }
$setupName = "Accelerator-$Version-Setup-win-x64"
$setup = Join-Path $root "dist\$setupName.exe"
$reportPath = Join-Path $root 'test-results\installer-checks.json'
$log = Join-Path $root 'test-results\installer-preview.log'
$report = Get-Content -LiteralPath $reportPath -Raw | ConvertFrom-Json
if ($report.version -ne $Version -or (Get-FileHash -LiteralPath $setup -Algorithm SHA256).Hash -ne $report.installerSHA256) {
    throw 'Installer does not match the packaging report.'
}
if (-not $Verify) {
    if (Get-Process -Name $setupName, "$setupName.tmp" -ErrorAction SilentlyContinue) {
        throw 'Close the existing installer before preview.'
    }
    $report.installerWizardPreviewPassed = $false
    $report | ConvertTo-Json | Set-Content -LiteralPath $reportPath -Encoding UTF8
    if (Test-Path -LiteralPath $log) { Remove-Item -LiteralPath $log }
    Start-Process -FilePath $setup -ArgumentList @('/PREVIEWONLY=1', ('/LOG="' + $log + '"')) -WindowStyle Hidden | Out-Null
    Write-Host 'Preview only: navigate to Ready to Install, then cancel and close. Installation is blocked.'
    return
}
if (Get-Process -Name $setupName, "$setupName.tmp" -ErrorAction SilentlyContinue) {
    throw 'Close the installer preview before verification.'
}
$text = Get-Content -LiteralPath $log -Raw
$unexpected = @(
    $text -split "`r?`n" | Where-Object {
        $_ -match '(?i)type mismatch|runtime error|InstallDriver:' -or
        ($_ -match '(?i)\bexception\b' -and $_ -notmatch '(?i)Got EAbort exception')
    }
)
if (-not $text.Contains('Installer preview: initialized; installation disabled.') -or
    -not $text.Contains('Installer preview: ready page reached.') -or
    -not $text.Contains('Log closed.') -or
    $unexpected.Count -gt 0) {
    throw 'Preview did not complete safely or encountered an error.'
}
$report.installerWizardPreviewPassed = $true
$report | Add-Member -NotePropertyName installerWizardPreviewTime -NotePropertyValue ([DateTime]::UtcNow.ToString('o')) -Force
$report | ConvertTo-Json | Set-Content -LiteralPath $reportPath -Encoding UTF8
Write-Host 'Installer wizard preview passed. No real installation or driver changes were performed.'
