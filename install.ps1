[CmdletBinding()]
param(
  [switch]$Force,
  [string]$Destination = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router'),
  [string]$GoExe = ''
)

$ErrorActionPreference = 'Stop'
$ExpectedVersion = '26.908.4834.0'
$ExpectedAsarSha256 = '2BD5B96A48232F3CCF3DF6BE50965920699EA3A1B4512DCDD770E209FD1F009E'
$ProjectRoot = $PSScriptRoot
$Destination = [System.IO.Path]::GetFullPath($Destination)

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
}

$package = Get-OfficialCodexPackage
$source = [System.IO.Path]::GetFullPath($package.InstallLocation)
$sourceAsar = Join-Path $source 'app\resources\app.asar'
if (-not (Test-Path -LiteralPath $sourceAsar -PathType Leaf)) { throw "Official app.asar not found: $sourceAsar" }
$sourceHash = (Get-FileHash -LiteralPath $sourceAsar -Algorithm SHA256).Hash
if ($sourceHash -ne $ExpectedAsarSha256) { throw "Unsupported official app.asar hash $sourceHash; expected $ExpectedAsarSha256." }
if ($source.TrimEnd('\') -ieq $Destination.TrimEnd('\')) { throw 'Source and destination must be different.' }

if (Test-Path -LiteralPath $Destination) {
  $existingLauncher = Join-Path $Destination 'Launch-CodexSubscriptionRouter.ps1'
  $existingRealCli = Join-Path $Destination 'app\resources\codex.real.exe'
  $existingAsar = Join-Path $Destination 'app\resources\app.asar'
  $looksComplete =
    (Test-Path -LiteralPath $existingLauncher -PathType Leaf) -and
    (Test-Path -LiteralPath $existingRealCli -PathType Leaf) -and
    (Test-Path -LiteralPath $existingAsar -PathType Leaf)

  if ($looksComplete -and -not $Force) {
    throw "A complete staged router already exists at $Destination. Re-run with -Force to replace it and create a recoverable backup."
  }

  if ($looksComplete) {
    Move-StagedDestinationToBackup -Path $Destination
  } else {
    Move-StagedDestinationToBackup -Path $Destination -Reason 'Incomplete staged router detected; recovering automatically.'
  }
}

New-Item -ItemType Directory -Path $Destination -Force | Out-Null
$null = & robocopy.exe $source $Destination /E /COPY:DAT /DCOPY:DAT /R:1 /W:1 /NFL /NDL /NJH /NJS /NP
if ($LASTEXITCODE -ge 8) { throw "robocopy failed with exit code $LASTEXITCODE" }
Get-ChildItem -LiteralPath $Destination -Force -Recurse -File -ErrorAction SilentlyContinue | ForEach-Object { $_.IsReadOnly = $false }

$stagedAsar = Join-Path $Destination 'app\resources\app.asar'
if ((Get-FileHash -LiteralPath $stagedAsar -Algorithm SHA256).Hash -ne $ExpectedAsarSha256) {
  throw 'Staged app.asar does not byte-match the official package after copying.'
}

$go = Resolve-GoExecutable
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

$python = Get-Command python.exe -ErrorAction SilentlyContinue
if (-not $python) { throw 'Python 3 is required to patch the staged ASAR.' }
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

Write-Host ''
Write-Host "Staged Windows build: $Destination"
Write-Host "Launcher: $launcher"
Write-Host 'The official ChatGPT/Codex installation was not modified or restarted.'
Write-Host 'This installer does not launch the staged copy automatically.'
