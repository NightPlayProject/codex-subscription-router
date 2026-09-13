$ErrorActionPreference = 'Stop'
. (Join-Path (Split-Path $PSScriptRoot -Parent) 'windows\identity.ps1')
$script:ownerPath = 'C:\Example\app\ChatGPT.exe'
$script:address = '127.0.0.1'
$script:browserId = 'expected-browser'
function Get-NetTCPConnection {
    param($State,$LocalPort,$ErrorAction)
    [pscustomobject]@{LocalAddress=$script:address;OwningProcess=123}
}
function Get-CimInstance {
    param($ClassName,$Filter,$ErrorAction)
    [pscustomobject]@{ExecutablePath=$script:ownerPath}
}
function Invoke-RestMethod {
    param($Uri,$TimeoutSec,$MaximumRedirection)
    [pscustomobject]@{webSocketDebuggerUrl="ws://127.0.0.1:9349/devtools/browser/$script:browserId"}
}
$endpoint = @{port=9349;browserId='expected-browser'}
Test-RouterEndpoint $endpoint 'C:\Example\app\ChatGPT.exe'
function Assert-Rejected {
    $rejected = $false
    try { Test-RouterEndpoint $endpoint 'C:\Example\app\ChatGPT.exe' } catch { $rejected = $true }
    if (-not $rejected) { throw 'Untrusted endpoint accepted.' }
}
$script:ownerPath = 'C:\Other\ChatGPT.exe'
Assert-Rejected
$script:ownerPath = 'C:\Example\app\ChatGPT.exe'
$script:address = '0.0.0.0'
Assert-Rejected
$script:address = '127.0.0.1'
$script:browserId = 'replacement-browser'
Assert-Rejected
Write-Host 'Endpoint checks passed: staged executable, loopback address and browser identity required.'
