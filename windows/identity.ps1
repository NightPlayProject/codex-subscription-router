[CmdletBinding()]
param([string]$VerifyEndpoint)
$ErrorActionPreference = 'Stop'
function Test-RouterEndpoint($Endpoint, [string]$Executable) {
    if ($Endpoint.port -ne 9349 -or $Endpoint.browserId -notmatch '^[\w-]+$') { throw 'Invalid wallpaper endpoint.' }
    $listeners = @(Get-NetTCPConnection -State Listen -LocalPort $Endpoint.port -ErrorAction Stop)
    if (-not $listeners.Count) { throw 'Wallpaper endpoint is offline.' }
    foreach ($listener in $listeners) {
        if ($listener.LocalAddress -notin @('127.0.0.1', '::1')) { throw 'Wallpaper endpoint must be loopback only.' }
        $owner = Get-CimInstance Win32_Process -Filter "ProcessId=$($listener.OwningProcess)" -ErrorAction Stop
        if ($owner.ExecutablePath -ine $Executable) { throw 'Wallpaper endpoint belongs to another application.' }
    }
    $version = Invoke-RestMethod "http://127.0.0.1:$($Endpoint.port)/json/version" -TimeoutSec 3 -MaximumRedirection 0
    $uri = [Uri]$version.webSocketDebuggerUrl
    if ($uri.Scheme -ne 'ws' -or $uri.Host -notin @('127.0.0.1', 'localhost', '[::1]') -or $uri.Port -ne $Endpoint.port -or $uri.AbsolutePath -ne "/devtools/browser/$($Endpoint.browserId)") { throw 'Wallpaper browser identity changed.' }
}
if ($VerifyEndpoint) {
    # This file is installed under wallpapers/windows, beside the staged app.
    $installRoot = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
    Test-RouterEndpoint (Get-Content -LiteralPath $VerifyEndpoint -Raw | ConvertFrom-Json) (Join-Path $installRoot 'app\ChatGPT.exe')
}
