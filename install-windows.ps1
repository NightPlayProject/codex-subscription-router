[CmdletBinding()]
param(
  [string]$SourceDir = (Join-Path $env:LOCALAPPDATA 'Codex Subscription Router\Source'),
  [string]$Destination = (Join-Path $env:LOCALAPPDATA 'Programs\Codex Subscription Router'),
  [string]$GoExe = '',
  [switch]$Launch
)

$ErrorActionPreference = 'Stop'
$RepositoryUrl = 'https://github.com/NightPlayProject/codex-subscription-router.git'

function Invoke-Checked {
  param(
    [Parameter(Mandatory = $true)][string]$FilePath,
    [Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments
  )
  # Send child-process output directly to the console. If this function is
  # called inside another function whose return value is assigned, leaving the
  # output on PowerShell's success stream would accidentally become part of
  # that return value (for example Git's "Already up to date." text).
  & $FilePath @Arguments | Out-Host
  if ($LASTEXITCODE -ne 0) {
    throw "$FilePath failed with exit code $LASTEXITCODE"
  }
}

function Resolve-ProjectDirectory {
  $localInstaller = if ($PSScriptRoot) { Join-Path $PSScriptRoot 'install.ps1' } else { '' }
  $localPatcher = if ($PSScriptRoot) { Join-Path $PSScriptRoot 'scripts\patch_app_windows.py' } else { '' }
  if ($localInstaller -and (Test-Path -LiteralPath $localInstaller -PathType Leaf) -and (Test-Path -LiteralPath $localPatcher -PathType Leaf)) {
    Write-Host "Using source checkout: $PSScriptRoot"
    return [System.IO.Path]::GetFullPath($PSScriptRoot)
  }

  $git = Get-Command git.exe -ErrorAction SilentlyContinue
  if (-not $git) { throw 'Git is required for the one-command Windows installer.' }

  $resolvedSource = [System.IO.Path]::GetFullPath($SourceDir)
  $gitDir = Join-Path $resolvedSource '.git'
  if (Test-Path -LiteralPath $gitDir -PathType Container) {
    $origin = (& $git.Source -C $resolvedSource remote get-url origin).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Could not inspect the managed source remote.' }
    if ($origin -ne $RepositoryUrl) {
      throw "Managed source points at an unexpected origin: $origin"
    }
    $dirty = & $git.Source -C $resolvedSource status --porcelain
    if ($LASTEXITCODE -ne 0) { throw 'Could not inspect the managed source checkout.' }
    if ($dirty) {
      throw "Managed source has local changes: $resolvedSource. Preserve or remove them, then rerun the same command."
    }
    $branch = (& $git.Source -C $resolvedSource branch --show-current).Trim()
    if ($branch -ne 'main') {
      throw "Managed source is on '$branch' instead of 'main': $resolvedSource"
    }
    Write-Host 'Updating Codex Subscription Router source...'
    Invoke-Checked $git.Source -C $resolvedSource pull --ff-only origin main
  } elseif (Test-Path -LiteralPath $resolvedSource) {
    throw "Source path exists but is not a Git checkout: $resolvedSource"
  } else {
    $parent = Split-Path $resolvedSource -Parent
    New-Item -ItemType Directory -Path $parent -Force | Out-Null
    Write-Host 'Downloading Codex Subscription Router source...'
    Invoke-Checked $git.Source clone --depth 1 --branch main $RepositoryUrl $resolvedSource
  }
  return $resolvedSource
}

$project = Resolve-ProjectDirectory
$installer = Join-Path $project 'install.ps1'
$arguments = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $installer, '-Destination', $Destination)
if ($GoExe) { $arguments += @('-GoExe', $GoExe) }

Write-Host 'Installing or upgrading Codex Subscription Router...'
Invoke-Checked 'powershell.exe' @arguments

$launcher = Join-Path $env:LOCALAPPDATA 'Codex Subscription Router\Launcher\CodexSubscriptionRouter.exe'
if ($Launch) {
  Write-Host 'Launching Codex Subscription Router...'
  Start-Process -FilePath $launcher -WindowStyle Hidden
} else {
  Write-Host ''
  Write-Host 'Install/update complete. Open Codex Subscription Router from Start.'
  Write-Host 'Pin that shortcut to your taskbar. Future updates can be queued in the Subscriptions panel.'
}
