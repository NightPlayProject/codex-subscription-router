[CmdletBinding()]
param(
  [switch]$Force,
  [switch]$SkipShortcut,
  [string]$Destination = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router'),
  [string]$GoExe = ''
)

$ErrorActionPreference = 'Stop'
$ExpectedVersion = '26.908.4834.0'
$ExpectedAsarSha256 = '2BD5B96A48232F3CCF3DF6BE50965920699EA3A1B4512DCDD770E209FD1F009E'
$ProjectRoot = $PSScriptRoot
$Destination = [System.IO.Path]::GetFullPath($Destination)
$FinalDestination = $Destination
if ($Destination -eq [IO.Path]::GetPathRoot($Destination) -or $Destination.StartsWith((Join-Path $env:ProgramFiles 'WindowsApps'), [StringComparison]::OrdinalIgnoreCase)) {
  throw 'Destination must be a dedicated writable application folder outside WindowsApps.'
}

function Invoke-Checked {
  param(
    [Parameter(Mandatory = $true)][string]$FilePath,
    [Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments
  )
  & $FilePath @Arguments
  if ($LASTEXITCODE -ne 0) {
    throw "$FilePath failed with exit code $LASTEXITCODE"
  }
}

function Get-OfficialCodexPackage {
  $package = Get-AppxPackage -Name 'OpenAI.Codex' | Sort-Object Version -Descending | Select-Object -First 1
  if (-not $package) { throw 'OpenAI.Codex is not installed for this Windows user.' }
  if ([string]$package.Version -ne $ExpectedVersion) {
    throw "Unsupported OpenAI.Codex package version $($package.Version); expected $ExpectedVersion."
  }
  return $package
}

function Resolve-GoExecutable {
  if ($GoExe) {
    $candidate = [System.IO.Path]::GetFullPath($GoExe)
    if (-not (Test-Path -LiteralPath $candidate -PathType Leaf)) { throw "Go executable not found: $candidate" }
    return $candidate
  }
  $command = Get-Command go.exe -ErrorAction SilentlyContinue
  if ($command) { return $command.Source }
  $portable = Join-Path $env:TEMP 'codex-router-go126\go\bin\go.exe'
  if (Test-Path -LiteralPath $portable -PathType Leaf) { return $portable }
  throw 'Go 1.26+ is required. Pass -GoExe with the path to go.exe.'
}

function Move-StagedDestinationToBackup {
  param(
    [Parameter(Mandatory = $true)]
    [string]$Path,
    [string]$Reason = ''
  )

  $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
  $backupRoot = Join-Path $HOME ".codex-mux\backups\windows\$stamp"
  New-Item -ItemType Directory -Path $backupRoot -Force | Out-Null
  $backup = Join-Path $backupRoot 'Codex Subscription Router'
  Move-Item -LiteralPath $Path -Destination $backup
  if ($Reason) { Write-Host $Reason }
  Write-Host "Existing staged copy moved to $backup"
  return $backup
}

function Assert-StagedRouterIsClosed {
  if (-not (Test-Path -LiteralPath $Destination)) { return }
  $prefix = $Destination.TrimEnd('\') + '\'
  $launcherPath = Join-Path $Destination 'Launch-CodexSubscriptionRouter.ps1'
  $running = Get-CimInstance Win32_Process -ErrorAction SilentlyContinue | Where-Object {
    ($_.ProcessId -ne $PID) -and (
      ($_.ExecutablePath -and $_.ExecutablePath.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase)) -or
      ($_.CommandLine -and $_.CommandLine.IndexOf($launcherPath, [System.StringComparison]::OrdinalIgnoreCase) -ge 0)
    )
  }
  if ($running) {
    $summary = ($running | ForEach-Object { "$($_.Name) (PID $($_.ProcessId))" }) -join ', '
    throw "Codex Subscription Router is still running: $summary. Close only the staged router, then rerun the same install command."
  }
}

$package = Get-OfficialCodexPackage
$source = [System.IO.Path]::GetFullPath($package.InstallLocation)
$sourceAsar = Join-Path $source 'app\resources\app.asar'
if (-not (Test-Path -LiteralPath $sourceAsar -PathType Leaf)) { throw "Official app.asar not found: $sourceAsar" }
$sourceHash = (Get-FileHash -LiteralPath $sourceAsar -Algorithm SHA256).Hash
if ($sourceHash -ne $ExpectedAsarSha256) { throw "Unsupported official app.asar hash $sourceHash; expected $ExpectedAsarSha256." }
if ($source.TrimEnd('\') -ieq $Destination.TrimEnd('\')) { throw 'Source and destination must be different.' }

Assert-StagedRouterIsClosed

$go = Resolve-GoExecutable
$npm = Get-Command npm.cmd -ErrorAction SilentlyContinue
if (-not $npm) { throw 'Node.js 22.12+ and npm are required.' }
$python = Get-Command python.exe -ErrorAction SilentlyContinue
if (-not $python) { throw 'Python 3 is required to patch the staged ASAR.' }

Push-Location $ProjectRoot
try {
  Write-Host 'Preparing locked build tools...'
  Invoke-Checked $npm.Source ci --ignore-scripts --no-audit --no-fund
} finally {
  Pop-Location
}

$buildDir = Join-Path $ProjectRoot 'build\windows'
New-Item -ItemType Directory -Path $buildDir -Force | Out-Null
$muxExe = Join-Path $buildDir 'codex-mux.exe'
Push-Location $ProjectRoot
try {
  & $go build -trimpath '-ldflags=-s -w' -o $muxExe .\cmd\codex-mux
  if ($LASTEXITCODE -ne 0) { throw "go build failed with exit code $LASTEXITCODE" }
} finally {
  Pop-Location
}

# Build the complete replacement first. A failed download/build/patch leaves
# the currently installed application intact.
$Destination = $FinalDestination + '.staging-' + [guid]::NewGuid().ToString('N')
New-Item -ItemType Directory -Path $Destination -Force | Out-Null
$null = & robocopy.exe $source $Destination /E /COPY:DAT /DCOPY:DAT /R:1 /W:1 /NFL /NDL /NJH /NJS /NP
if ($LASTEXITCODE -ge 8) { throw "robocopy failed with exit code $LASTEXITCODE" }
Get-ChildItem -LiteralPath $Destination -Force -Recurse -File -ErrorAction SilentlyContinue | ForEach-Object { $_.IsReadOnly = $false }

$stagedAsar = Join-Path $Destination 'app\resources\app.asar'
if ((Get-FileHash -LiteralPath $stagedAsar -Algorithm SHA256).Hash -ne $ExpectedAsarSha256) {
  throw 'Staged app.asar does not byte-match the official package after copying.'
}

& $python.Source (Join-Path $ProjectRoot 'scripts\patch_app_windows.py') --source $source --destination $Destination --mux-exe $muxExe
if ($LASTEXITCODE -ne 0) { throw "Windows patcher failed with exit code $LASTEXITCODE" }

$launcher = Join-Path $Destination 'Launch-CodexSubscriptionRouter.ps1'
$installedMux = Join-Path $Destination 'app\resources\codex.exe'
$installedRealCli = Join-Path $Destination 'app\resources\codex.real.exe'
foreach ($required in @($launcher, $installedMux, $installedRealCli)) {
  if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
    throw "Windows staging finished without required file: $required"
  }
}
if ((Get-FileHash -LiteralPath $installedMux -Algorithm SHA256).Hash -ne (Get-FileHash -LiteralPath $muxExe -Algorithm SHA256).Hash) {
  throw 'Installed router executable does not match the freshly built multiplexer.'
}

$officialAfter = (Get-FileHash -LiteralPath $sourceAsar -Algorithm SHA256).Hash
if ($officialAfter -ne $ExpectedAsarSha256) { throw 'Official app.asar changed unexpectedly during installation.' }

New-Item -ItemType Directory -Path (Join-Path $Destination 'wallpapers\windows') -Force | Out-Null
Copy-Item -LiteralPath (Join-Path $ProjectRoot 'third_party\codex-wallpapers\src') -Destination (Join-Path $Destination 'wallpapers\src') -Recurse
foreach ($notice in @('LICENSE', 'UPSTREAM.md')) {
  Copy-Item -LiteralPath (Join-Path $ProjectRoot "third_party\codex-wallpapers\$notice") -Destination (Join-Path $Destination 'wallpapers')
}
Copy-Item -LiteralPath (Join-Path $ProjectRoot 'windows\identity.ps1') -Destination (Join-Path $Destination 'wallpapers\windows\identity.ps1')
Copy-Item -LiteralPath (Join-Path $ProjectRoot 'windows\Wallpapers.ps1') -Destination $Destination
$revision = (& git.exe -C $ProjectRoot rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $revision -cnotmatch '^[a-f0-9]{40}$') { throw 'Could not identify the installed source revision.' }
@{
  revision = $revision
  wallpapers = '054348d193b4f68a0f96c1ae0f900776c2d2616c'
  routerVersion = '26.908.4834.0'
  muxSha256 = (Get-FileHash -LiteralPath $installedMux -Algorithm SHA256).Hash
  installedAt = (Get-Date).ToUniversalTime().ToString('o')
} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $Destination 'build-info.json') -Encoding ASCII

# The small shortcut dispatcher lives outside the replaceable application.
$launcherRoot = Join-Path $env:LOCALAPPDATA 'Codex Subscription Router\Launcher'
$launcherBuild = Join-Path $buildDir 'CodexSubscriptionRouter.exe'
$compiler = Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
$icon = Join-Path $source 'app\resources\chatgpt-app-dark.ico'
Invoke-Checked $compiler /nologo /target:winexe /reference:System.Windows.Forms.dll ('/out:' + $launcherBuild) ('/win32icon:' + $icon) (Join-Path $ProjectRoot 'windows\Launcher.cs')
$launcherExe = Join-Path $launcherRoot 'CodexSubscriptionRouter.exe'
$candidate = $Destination
$Destination = $FinalDestination
Assert-StagedRouterIsClosed
$backup = $null
if (Test-Path -LiteralPath $Destination) {
  $backup = Move-StagedDestinationToBackup -Path $Destination -Reason 'Creating a recoverable backup before replacing the staged app.'
}
try { Move-Item -LiteralPath $candidate -Destination $Destination } catch {
  if ($backup -and -not (Test-Path -LiteralPath $Destination)) { Move-Item -LiteralPath $backup -Destination $Destination }
  throw
}
if (-not $SkipShortcut) {
  New-Item -ItemType Directory -Path $launcherRoot -Force | Out-Null
  if (-not (Test-Path -LiteralPath $launcherExe)) { Copy-Item -LiteralPath $launcherBuild -Destination $launcherExe }
  Copy-Item -LiteralPath (Join-Path $ProjectRoot 'windows\Start.ps1') -Destination $launcherRoot -Force
  @{ destination = $Destination; goExe = $go } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $launcherRoot 'install.json') -Encoding UTF8
  $shell = New-Object -ComObject WScript.Shell
  $shortcut = $shell.CreateShortcut((Join-Path ([Environment]::GetFolderPath('Programs')) 'Codex Subscription Router.lnk'))
  $shortcut.TargetPath = $launcherExe
  $shortcut.WorkingDirectory = $launcherRoot
  $shortcut.Description = 'Codex with subscription routing and wallpapers'
  $shortcut.IconLocation = $launcherExe + ',0'
  $shortcut.Save()
}
$launcher = Join-Path $Destination 'Launch-CodexSubscriptionRouter.ps1'

Write-Host ''
Write-Host "Staged Windows build: $Destination"
Write-Host "Launcher: $launcher"
if (-not $SkipShortcut) { Write-Host 'Open Codex Subscription Router from Start; pin that shortcut to the taskbar for everyday use.' }
Write-Host 'The official ChatGPT/Codex installation was not modified or restarted.'
Write-Host 'This installer does not launch the staged copy automatically.'
if ($Force) { Write-Verbose '-Force is no longer required; it remains accepted for compatibility with older commands.' }
