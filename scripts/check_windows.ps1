[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$ProjectRoot = Split-Path $PSScriptRoot -Parent

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

Push-Location $ProjectRoot
try {
  $go = (Get-Command go.exe -ErrorAction Stop).Source
  $node = (Get-Command node.exe -ErrorAction Stop).Source
  $python = (Get-Command python.exe -ErrorAction Stop).Source

  Invoke-Checked $go test ./...
  Invoke-Checked $go vet ./...
  Invoke-Checked $node --check ui/windows-account-panel.js
  Invoke-Checked $python -m py_compile scripts/patch_app_windows.py tests/test_patch_app_windows.py
  Invoke-Checked $python -m unittest tests/test_patch_app_windows.py

  $tokens = $null
  $errors = $null
  [System.Management.Automation.Language.Parser]::ParseFile(
    (Join-Path $ProjectRoot 'install.ps1'),
    [ref]$tokens,
    [ref]$errors
  ) | Out-Null
  if ($errors.Count -ne 0) {
    $messages = $errors | ForEach-Object { $_.Message }
    throw "install.ps1 parse errors: $($messages -join '; ')"
  }
} finally {
  Pop-Location
}

Write-Host 'Windows checks passed.'
