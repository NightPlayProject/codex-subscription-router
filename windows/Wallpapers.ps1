param([string]$Executable, [string]$UserDataArgument)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'wallpapers\windows\identity.ps1')
$data = if ($env:CODEX_WALLPAPERS_DATA) { $env:CODEX_WALLPAPERS_DATA } else { Join-Path $env:LOCALAPPDATA 'CodexWallpapers' }
New-Item -ItemType Directory -Path $data -Force | Out-Null
$node = Get-Command node.exe -ErrorAction SilentlyContinue
$apply = Join-Path $PSScriptRoot 'wallpapers\src\apply.mjs'
$running = @(Get-CimInstance Win32_Process -Filter "Name='ChatGPT.exe'" -ErrorAction Stop | Where-Object { $_.ExecutablePath -ieq $Executable })
$available = -not @(Get-NetTCPConnection -State Listen -LocalPort 9349 -ErrorAction SilentlyContinue).Count
$supported = $false
if ($node) {
    & $node.Source $apply --check 1> (Join-Path $data 'router-preflight.log') 2> (Join-Path $data 'router-preflight-error.log')
    $supported = $LASTEXITCODE -eq 0
}
$launchArgs = $UserDataArgument
if ($supported -and $available -and -not $running.Count) { $launchArgs += ' --remote-debugging-address=127.0.0.1 --remote-debugging-port=9349' }
Start-Process -FilePath $Executable -ArgumentList $launchArgs -WorkingDirectory (Split-Path $Executable -Parent)
if (-not $supported) { return }
if ($running.Count -and $available) {
    'The staged app is already open without wallpapers. Reopen from the combined shortcut when ready.' | Set-Content -LiteralPath (Join-Path $data 'router-listener-error.log')
    return
}
$deadline = (Get-Date).AddSeconds(40)
do {
    try {
        $version = Invoke-RestMethod 'http://127.0.0.1:9349/json/version' -TimeoutSec 2 -MaximumRedirection 0
        $endpoint = @{ port = 9349; browserId = ([Uri]$version.webSocketDebuggerUrl).Segments[-1] }
        Test-RouterEndpoint $endpoint $Executable
        $watchers = @(Get-CimInstance Win32_Process -Filter "Name='node.exe'" -ErrorAction Stop | Where-Object {
            $_.CommandLine -and $_.CommandLine.Contains($apply) -and $_.CommandLine.Contains('--watch')
        })
        if ($watchers.Count) { return }
        $endpoint | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $data 'router-endpoint.json') -Encoding UTF8
        $env:CODEX_WALLPAPERS_ENDPOINT = 'router-endpoint.json'
        Start-Process -FilePath $node.Source -ArgumentList ('"' + $apply + '" --watch') -WindowStyle Hidden -RedirectStandardOutput (Join-Path $data 'router-listener.log') -RedirectStandardError (Join-Path $data 'router-listener-error.log')
        return
    } catch { Start-Sleep -Milliseconds 400 }
} while ((Get-Date) -lt $deadline)
'Wallpapers could not attach. Close the staged app and reopen the combined shortcut when ready.' | Set-Content -LiteralPath (Join-Path $data 'router-listener-error.log')
