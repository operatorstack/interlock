<#
.SYNOPSIS
  Interlock installer (Windows) — no Go toolchain required.

.DESCRIPTION
  Downloads a pinned prebuilt release, verifies its SHA-256 checksum, installs
  interlock.exe, adds the install directory to the user PATH, then runs
  `interlock doctor` and the repository-policy demo. Fails closed on a checksum
  mismatch.

    irm https://raw.githubusercontent.com/operatorstack/interlock/main/install.ps1 | iex

.PARAMETER Version
  Release tag to install. Defaults to $env:INTERLOCK_VERSION, else latest.
.PARAMETER InstallDir
  Install directory. Defaults to $env:INTERLOCK_INSTALL_DIR, else
  %LOCALAPPDATA%\interlock\bin.
#>
[CmdletBinding()]
param(
  [string]$Version = $env:INTERLOCK_VERSION,
  [string]$InstallDir = $env:INTERLOCK_INSTALL_DIR
)

$ErrorActionPreference = 'Stop'
$Repo = 'operatorstack/interlock'
$Binary = 'interlock'

function Info($m) { Write-Host "interlock-install: $m" }

# --- detect arch (Windows amd64 only) ------------------------------------
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { 'amd64' }
  default { throw "unsupported architecture: $($env:PROCESSOR_ARCHITECTURE) (only windows/amd64 is published)" }
}
$os = 'windows'

# --- resolve version ------------------------------------------------------
if ([string]::IsNullOrEmpty($Version)) {
  Info 'resolving latest release'
  $latest = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers @{ 'User-Agent' = 'interlock-install' }
  $Version = $latest.tag_name
  if ([string]::IsNullOrEmpty($Version)) { throw 'could not resolve latest release tag' }
}
Info "installing $Binary $Version ($os/$arch)"

$archive = "${Binary}_${Version}_${os}_${arch}.zip"
$base = "https://github.com/$Repo/releases/download/$Version"

# --- download + verify ----------------------------------------------------
$tmp = Join-Path $env:TEMP ("interlock-" + [System.Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp -Force | Out-Null
try {
  $archivePath = Join-Path $tmp $archive
  $sumsPath = Join-Path $tmp 'checksums.txt'
  Invoke-WebRequest -Uri "$base/$archive" -OutFile $archivePath -UseBasicParsing
  Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile $sumsPath -UseBasicParsing

  $want = (Select-String -Path $sumsPath -Pattern ([regex]::Escape($archive)) |
    Select-Object -First 1).Line -split '\s+' | Select-Object -First 1
  if ([string]::IsNullOrEmpty($want)) { throw "no checksum for $archive in checksums.txt" }
  $got = (Get-FileHash -Path $archivePath -Algorithm SHA256).Hash.ToLower()
  if ($want.ToLower() -ne $got) { throw "checksum mismatch for $archive (want $want, got $got)" }
  Info 'checksum verified'

  # --- install ------------------------------------------------------------
  if ([string]::IsNullOrEmpty($InstallDir)) {
    $InstallDir = Join-Path $env:LOCALAPPDATA 'interlock\bin'
  }
  New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
  Expand-Archive -Path $archivePath -DestinationPath $tmp -Force
  $exe = Join-Path $tmp "$Binary.exe"
  if (-not (Test-Path $exe)) { throw "archive did not contain $Binary.exe" }
  Copy-Item -Path $exe -Destination (Join-Path $InstallDir "$Binary.exe") -Force
  Info "installed $InstallDir\$Binary.exe"

  # add to user PATH if missing
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  if (($userPath -split ';') -notcontains $InstallDir) {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$InstallDir", 'User')
    $env:Path = "$env:Path;$InstallDir"
    Info "added $InstallDir to your user PATH (restart your shell to pick it up)"
  }

  # --- prove it works -----------------------------------------------------
  $bin = Join-Path $InstallDir "$Binary.exe"
  Write-Host ''
  & $bin doctor
  Write-Host ''
  & $bin demo repository-policy
  Write-Host ''
  Info "done — author your own policy with '$Binary init'"
}
finally {
  Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
