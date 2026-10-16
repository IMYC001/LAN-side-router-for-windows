param(
    [Parameter(Mandatory = $true)][string]$DeviceIP,
    [Parameter(Mandatory = $true)][string]$DeviceMAC,
    [Parameter(Mandatory = $true)][string]$AdapterGUID,
    [string]$OutputName = 'Accelerator-0.5.13',
    [switch]$ProbeTarget
)
$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
if ($OutputName -notmatch '^Accelerator(?:-[A-Za-z0-9.-]+)?$') { throw 'Invalid release directory.' }
$enginePath = Join-Path $projectRoot "dist\$OutputName\accelerator-engine.exe"

function Read-EngineData($request) {
    $response = ($request | ConvertTo-Json -Depth 8 -Compress) | & $enginePath
    if ($LASTEXITCODE -ne 0) { throw 'Read-only engine request failed.' }
    $parsed = $response | ConvertFrom-Json
    if (-not $parsed.ok) { throw $parsed.error }
    return $parsed.data
}

function Read-Status($path) {
    if (-not (Test-Path -LiteralPath $path)) { return $null }
    $stream = $null
    $reader = $null
    try {
        $stream = [IO.FileStream]::new($path, [IO.FileMode]::Open, [IO.FileAccess]::Read,
            [IO.FileShare]::ReadWrite -bor [IO.FileShare]::Delete)
        $reader = [IO.StreamReader]::new($stream)
        return $reader.ReadToEnd() | ConvertFrom-Json
    }
    catch [IO.IOException] { return $null }
    catch [ArgumentException] { return $null }
    finally {
        if ($null -ne $reader) { $reader.Dispose() }
        elseif ($null -ne $stream) { $stream.Dispose() }
    }
}

$snapshot = Read-EngineData @{ command = 'diagnostics' }
$adapter = @($snapshot.adapters | Where-Object {
    $_.physical -and $_.status -eq 'Up' -and $_.ipv4 -and
    $_.guid -eq $AdapterGUID
})
if ($adapter.Count -ne 1) { throw 'The confirmed physical WLAN is unavailable; no capture started.' }
$adapter = $adapter[0]
$directory = Join-Path $projectRoot ('.state\ps5-passive-' + (Get-Date -Format 'yyyyMMdd-HHmmss-fff'))
New-Item -ItemType Directory -Path $directory | Out-Null
$encoding = [Text.UTF8Encoding]::new($false)
$requestPath = Join-Path $directory 'request.json'
$leasePath = Join-Path $directory 'lease'
$stopPath = Join-Path $directory 'stop'
$statusPath = Join-Path $directory 'status.json'
$request = @{
    mode = 'passive'
    confirmed = $true
    seconds = 20
    plan = @{
        adapterIndex = $adapter.index
        deviceIP = $DeviceIP
        deviceMAC = $DeviceMAC
        proxy = '127.0.0.1:7897'
        ipv6Absent = $false
    }
    expected = @{
        adapterGUID = $adapter.guid
        adapterIP = $adapter.ipv4
        adapterMAC = $adapter.mac
        adapterPrefix = $adapter.prefix
        router = $adapter.gateway
        deviceIP = ''
        gateway = ''
    }
}
[IO.File]::WriteAllText($requestPath, ($request | ConvertTo-Json -Depth 8), $encoding)
[IO.File]::WriteAllText($leasePath, '', $encoding)
[IO.File]::WriteAllText((Join-Path $directory 'before.json'),
    ($snapshot | ConvertTo-Json -Depth 8), $encoding)
$worker = $null
$lastStatus = $null
$probePing = $null
$probeTask = $null
$probeAttempted = $false
$probeResult = @{ requested = [bool]$ProbeTarget; sent = $false }
try {
    $worker = Start-Process -FilePath $enginePath -ArgumentList "worker `"$requestPath`"" `
        -Verb RunAs -WindowStyle Hidden -PassThru
    [IO.File]::SetLastWriteTimeUtc($leasePath, [DateTime]::UtcNow)
    $watch = [Diagnostics.Stopwatch]::StartNew()
    while ($watch.Elapsed.TotalSeconds -lt 40) {
        [IO.File]::SetLastWriteTimeUtc($leasePath, [DateTime]::UtcNow)
        $state = Read-Status $statusPath
        if ($null -ne $state) { $lastStatus = $state }
        if ($worker.HasExited -or ($null -ne $state -and $state.stopped)) { break }
        if ($ProbeTarget -and -not $probeAttempted -and $null -ne $state -and $state.phase -eq 'listening') {
            $probeAttempted = $true
            $route = @(Find-NetRoute -RemoteIPAddress $DeviceIP | Where-Object { $_.DestinationPrefix })
            if ($route.Count -eq 1 -and $route[0].InterfaceIndex -eq $adapter.index -and $route[0].NextHop -eq '0.0.0.0') {
                # One ordinary ICMP request; no crafted Ethernet/ARP injection.
                $probePing = [Net.NetworkInformation.Ping]::new()
                $probeTask = $probePing.SendPingAsync($DeviceIP, 1500)
                $probeResult.sent = $true
            }
            else { $probeResult.reason = 'Target is no longer directly on the confirmed WLAN; probe skipped.' }
        }
        Start-Sleep -Milliseconds 500
    }
}
finally {
    [IO.File]::WriteAllText($stopPath, '', $encoding)
    if ($null -ne $worker) {
        $exited = $worker.WaitForExit(5000)
        $state = Read-Status $statusPath
        if ($null -ne $state) { $lastStatus = $state }
        $workerID = $worker.Id
        $exitCode = if ($exited) { $worker.ExitCode } else { $null }
        $worker.Dispose()
    }
    if ($null -ne $probeTask -and $probeTask.IsCompleted) {
        try {
            $reply = $probeTask.GetAwaiter().GetResult()
            $probeResult.status = $reply.Status.ToString()
            $probeResult.roundtripMilliseconds = $reply.RoundtripTime
        }
        catch { $probeResult.status = 'ProbeFailed' }
    }
    if ($null -ne $probePing) { $probePing.Dispose() }
}
$after = Read-EngineData @{ command = 'diagnostics' }
[IO.File]::WriteAllText((Join-Path $directory 'after.json'),
    ($after | ConvertTo-Json -Depth 8), $encoding)
$result = @{
    directory = $directory
    workerID = $workerID
    workerExited = $exited
    exitCode = $exitCode
    targetProbe = $probeResult
    status = $lastStatus
    physicalAdapterUnchanged = @($after.adapters | Where-Object {
        $_.guid -eq $adapter.guid -and $_.ipv4 -eq $adapter.ipv4 -and
        $_.prefix -eq $adapter.prefix -and $_.gateway -eq $adapter.gateway -and
        $_.status -eq $adapter.status -and $_.forwarding -eq $adapter.forwarding
    }).Count -eq 1
}
[IO.File]::WriteAllText((Join-Path $directory 'result.json'),
    ($result | ConvertTo-Json -Depth 8), $encoding)
$result | ConvertTo-Json -Depth 8
if (-not $exited) { throw 'Passive helper has not yet exited; no additional session may be started.' }
if ($null -eq $lastStatus -or -not $lastStatus.stopped -or $exitCode -ne 0) {
    throw 'Passive observation did not finish successfully; inspect the recorded status.'
}
