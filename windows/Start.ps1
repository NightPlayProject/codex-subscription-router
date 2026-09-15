$ErrorActionPreference = 'Stop'
$data = Split-Path $PSScriptRoot -Parent
$config = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'install.json') -Raw | ConvertFrom-Json
$destination = [IO.Path]::GetFullPath($config.destination)
$request = Join-Path $data 'update-request.json'
$buildInfo = Join-Path $destination 'build-info.json'
$mutex = New-Object Threading.Mutex($false, 'Local.CodexRouter.CombinedLaunch')
$locked = $false

function Test-RouterInstallState {
    if (-not (Test-Path -LiteralPath $buildInfo)) { return $false }
    try {
        $info = Get-Content -LiteralPath $buildInfo -Raw | ConvertFrom-Json
        return ($info.routerVersion -eq '26.908.9136.0' -and $info.revision -match '^[a-f0-9]{40}$')
    } catch {
        return $false
    }
}
function Invoke-Native([string]$File, [string[]]$Arguments) {
    & $File @Arguments | Out-Host
    if ($LASTEXITCODE -ne 0) { throw "$File failed ($LASTEXITCODE)." }
}
try {
    try { $locked = $mutex.WaitOne(0) } catch [Threading.AbandonedMutexException] { $locked = $true }
    if (-not $locked) { return }
    if (-not (Test-RouterInstallState) -and -not (Test-Path -LiteralPath $request)) {
        # Older installations do not have migration metadata. Keep startup
        # working, but mark them so the normal update queue can migrate them.
        @{ reason = 'legacy-install-metadata'; createdAt = (Get-Date).ToUniversalTime().ToString('o') } |
            ConvertTo-Json | Set-Content -LiteralPath (Join-Path $data 'migration-needed.json') -Encoding UTF8
    }
    $prefix = $destination.TrimEnd('\') + '\'
    $running = @(Get-CimInstance Win32_Process -ErrorAction Stop | Where-Object {
        $_.ExecutablePath -and $_.ExecutablePath.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)
    })
    if ((Test-Path -LiteralPath $request) -and -not $running.Count) {
        try {
            $revision = (Get-Content -LiteralPath $request -Raw | ConvertFrom-Json).revision
            if ($revision -cnotmatch '^[a-f0-9]{40}$') { throw 'Invalid update revision.' }
            $source = Join-Path $data ('Updates\' + [guid]::NewGuid().ToString('N'))
            New-Item -ItemType Directory -Path $source -Force | Out-Null
            Invoke-Native 'git.exe' @('init', $source)
            Invoke-Native 'git.exe' @('-C', $source, 'remote', 'add', 'origin', 'https://github.com/NightPlayProject/codex-subscription-router.git')
            Invoke-Native 'git.exe' @('-C', $source, 'fetch', '--depth', '1', 'origin', $revision)
            Invoke-Native 'git.exe' @('-C', $source, 'checkout', '--detach', $revision)
            $argsList = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', (Join-Path $source 'install.ps1'), '-Destination', $destination)
            if ($config.goExe) { $argsList += @('-GoExe', $config.goExe) }
            Invoke-Native 'powershell.exe' $argsList
            Remove-Item -LiteralPath $request
        } catch {
            $_ | Out-String | Set-Content -LiteralPath (Join-Path $data 'update-error.log')
            Add-Type -AssemblyName System.Windows.Forms
            [Windows.Forms.MessageBox]::Show("Update could not finish. Your update remains queued. Details: $(Join-Path $data 'update-error.log')", 'Codex Subscription Router') | Out-Null
        }
    }
    & (Join-Path $destination 'Launch-CodexSubscriptionRouter.ps1')
} catch {
    $_ | Out-String | Set-Content -LiteralPath (Join-Path $data 'launcher-error.log')
    Add-Type -AssemblyName System.Windows.Forms
    [Windows.Forms.MessageBox]::Show($_.Exception.Message, 'Codex Subscription Router') | Out-Null
} finally {
    if ($locked) { $mutex.ReleaseMutex() }
    $mutex.Dispose()
}

