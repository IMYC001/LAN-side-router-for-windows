param([switch]$SkipCompilerSetup)
$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$cache = Join-Path $root '.tools\installer'
New-Item -ItemType Directory -Path $cache -Force | Out-Null
function Get-VerifiedFile($Url, $Name, $Hash, $Publisher) {
    $path = Join-Path $cache $Name
    if (-not (Test-Path -LiteralPath $path)) {
        Invoke-WebRequest -Uri $Url -OutFile $path
    }
    if ((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash -ine $Hash) {
        throw "Hash mismatch: $Name"
    }
    $signature = Get-AuthenticodeSignature -LiteralPath $path
    if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notlike $Publisher) {
        throw "Signature mismatch: $Name"
    }
    return $path
}
$driver = Get-VerifiedFile `
    'https://github.com/wiresock/ndisapi/releases/download/v3.6.2/Windows.Packet.Filter.3.6.2.1.x64.msi' `
    'Windows.Packet.Filter.3.6.2.1.x64.msi' `
    '9c388c0b7f189f7fa98720bae2caecf7d64f30910838b80b438ecf8956b8502c' `
    '*O=The Anti-Cloud Corporation*'
$compilerSetup = Get-VerifiedFile `
    'https://github.com/jrsoftware/issrc/releases/download/is-6_7_3/innosetup-6.7.3.exe' `
    'innosetup-6.7.3.exe' `
    '9c73c3bae7ed48d44112a0f48e66742c00090bdb5bef71d9d3c056c66e97b732' `
    '*O=Pyrsys B.V.*'
$compiler = Join-Path $root '.tools\inno\ISCC.exe'
if (-not (Test-Path -LiteralPath $compiler) -and -not $SkipCompilerSetup) {
    $destination = Join-Path $root '.tools\inno'
    $process = Start-Process -FilePath $compilerSetup -ArgumentList @(
        '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/SP-', '/CURRENTUSER',
        '/NOICONS', '/TASKS=', ('/DIR="' + $destination + '"')
    ) -WindowStyle Hidden -PassThru
    if (-not $process.WaitForExit(45000)) { throw 'Compiler setup has not exited; inspect before retrying.' }
    if ($process.ExitCode -ne 0) { throw "Compiler setup failed: $($process.ExitCode)" }
}
$language = Join-Path $cache 'ChineseSimplified.isl'
if (-not (Test-Path -LiteralPath $language)) {
    Invoke-WebRequest 'https://raw.githubusercontent.com/jrsoftware/issrc/is-6_7_3/Files/Languages/Unofficial/ChineseSimplified.isl' -OutFile $language
}

# Read the MSI database and embedded cabinet without executing MSI actions.
Add-Type -TypeDefinition @'
using System;
using System.IO;
using System.Runtime.InteropServices;
public static class MsiCabinetReader {
    [DllImport("msi.dll", CharSet=CharSet.Unicode)] static extern uint MsiOpenDatabase(string path, IntPtr mode, out uint db);
    [DllImport("msi.dll", CharSet=CharSet.Unicode)] static extern uint MsiDatabaseOpenView(uint db, string query, out uint view);
    [DllImport("msi.dll")] static extern uint MsiViewExecute(uint view, uint record);
    [DllImport("msi.dll")] static extern uint MsiViewFetch(uint view, out uint record);
    [DllImport("msi.dll")] static extern uint MsiRecordReadStream(uint record, uint field, byte[] data, ref uint size);
    [DllImport("msi.dll")] static extern uint MsiCloseHandle(uint handle);
    static void Check(uint error) { if (error != 0) throw new InvalidOperationException("MSI read error " + error); }
    public static void Extract(string source, string output) {
        uint db = 0, view = 0, record = 0;
        try {
            Check(MsiOpenDatabase(source, IntPtr.Zero, out db));
            Check(MsiDatabaseOpenView(db, "SELECT `Data` FROM `_Streams` WHERE `Name`='cab1.cab'", out view));
            Check(MsiViewExecute(view, 0));
            Check(MsiViewFetch(view, out record));
            using (var file = File.Create(output)) {
                byte[] data = new byte[65536];
                while (true) {
                    uint length = (uint)data.Length;
                    Check(MsiRecordReadStream(record, 1, data, ref length));
                    if (length == 0) break;
                    file.Write(data, 0, (int)length);
                }
            }
        } finally {
            if (record != 0) MsiCloseHandle(record);
            if (view != 0) MsiCloseHandle(view);
            if (db != 0) MsiCloseHandle(db);
        }
    }
}
'@
$cab = Join-Path $cache 'driver.cab'
[MsiCabinetReader]::Extract($driver, $cab)
$extracted = Join-Path $cache 'driver-inspection'
New-Item -ItemType Directory -Path $extracted -Force | Out-Null
& "$env:SystemRoot\System32\expand.exe" $cab '-F:*' $extracted | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'Cabinet inspection failed.' }
$installer = New-Object -ComObject WindowsInstaller.Installer
$database = $installer.OpenDatabase($driver, 0)
$view = $database.OpenView("SELECT ``Text`` FROM ``Control`` WHERE ``Dialog_``='WelcomeEulaDlg' AND ``Control``='LicenseText'")
$view.Execute()
$record = $view.Fetch()
$eula = $record.GetType().InvokeMember('StringData', [Reflection.BindingFlags]::GetProperty, $null, $record, @(1))
[IO.File]::WriteAllText((Join-Path $cache 'Windows-Packet-Filter-EULA.rtf'), $eula, [Text.Encoding]::ASCII)
$view.Close()
Get-ChildItem -LiteralPath $extracted -File | ForEach-Object {
    [pscustomobject]@{ Name = $_.Name; SHA256 = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash }
} | Format-Table -AutoSize
Write-Host 'Only build tools and read-only driver inspection were performed. No packet driver was installed.'
