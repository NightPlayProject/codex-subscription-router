$ErrorActionPreference = 'Stop'
$project = Split-Path $PSScriptRoot -Parent
$fixture = Join-Path $project ('build\launcher-test-' + [guid]::NewGuid().ToString('N'))
$dispatcher = Join-Path $fixture 'Launcher'
$app = Join-Path $fixture 'App'
New-Item -ItemType Directory -Path $dispatcher,$app -Force | Out-Null
Copy-Item -LiteralPath (Join-Path $project 'windows\Start.ps1') -Destination $dispatcher
@{destination=$app;goExe='test-go.exe'} | ConvertTo-Json | Set-Content (Join-Path $dispatcher 'install.json')
'$global:launched++' | Set-Content (Join-Path $app 'Launch-CodexSubscriptionRouter.ps1')
$global:nativeCalls = [Collections.Generic.List[string]]::new()
$global:launched = 0
$global:simulateRunning = $false
function global:git.exe {
    $global:nativeCalls.Add('git ' + ($args -join ' '))
    $global:LASTEXITCODE = 0
}
function global:powershell.exe {
    $global:nativeCalls.Add('powershell ' + ($args -join ' '))
    $global:LASTEXITCODE = 0
}
function global:Get-CimInstance {
    param($ClassName,$ErrorAction)
    if ($global:simulateRunning) { [pscustomobject]@{ExecutablePath=(Join-Path $app 'app\ChatGPT.exe')} }
}
$request = Join-Path $fixture 'update-request.json'
$revision = 'b' * 40
@{revision=$revision} | ConvertTo-Json | Set-Content $request
$global:simulateRunning = $true
& (Join-Path $dispatcher 'Start.ps1')
if ($global:nativeCalls.Count -ne 0 -or -not (Test-Path $request) -or $global:launched -ne 1) { throw 'Running app must defer updates and keep its request.' }
$global:simulateRunning = $false
& (Join-Path $dispatcher 'Start.ps1')
if (Test-Path $request) { throw 'Successful update should consume request.' }
if ($global:launched -ne 2) { throw 'Updated app did not launch once.' }
if (-not ($global:nativeCalls | Where-Object { $_ -like "*fetch --depth 1 origin $revision" })) { throw 'Update did not fetch pinned revision.' }
if (-not ($global:nativeCalls | Where-Object { $_ -like "*install.ps1 -Destination $app -GoExe test-go.exe" })) { throw 'Update destination/tool settings changed.' }
$count = $global:nativeCalls.Count
& (Join-Path $dispatcher 'Start.ps1')
if ($global:nativeCalls.Count -ne $count -or $global:launched -ne 3) { throw 'Normal launch should not reinstall.' }
Write-Host 'Launcher checks passed: running-app deferral, pinned update, preserved destination and normal launch.'
