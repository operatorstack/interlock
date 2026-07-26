# interlock installer (Windows) — rendered by distribution/render.mjs, do not edit by hand.
#
#   irm https://get.operatorstack.systems/interlock/install.ps1 | iex
#
# Toolchain-free: downloads a prebuilt, checksum-verified binary from OperatorStack's
# GCP Artifact Registry, fronted by get.operatorstack.systems. All-GCP.
#
# Env overrides: INTERLOCK_VERSION (pin), INTERLOCK_INSTALL_DIR (location).
$ErrorActionPreference = "Stop"

$binary  = "interlock"
$getHost = "get.operatorstack.systems"
$version = [Environment]::GetEnvironmentVariable("INTERLOCK_VERSION")
$dir     = [Environment]::GetEnvironmentVariable("INTERLOCK_INSTALL_DIR")

$arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { throw "unsupported architecture" }

if (-not $version) {
  $latest = Invoke-RestMethod -Uri "https://$getHost/$binary/latest"
  $version = $latest.version
  if (-not $version) { throw "could not resolve latest version" }
}
Write-Host "  installing $binary $version (windows/$arch)"

$tmp = New-Item -ItemType Directory -Path (Join-Path $env:TEMP ([guid]::NewGuid()))
try {
  $archive = "${binary}_${version}_windows_${arch}.zip"
  $base = "https://$getHost/$binary/dl/$version"
  Invoke-WebRequest -Uri "$base/$archive" -OutFile (Join-Path $tmp $archive)
  Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile (Join-Path $tmp "checksums.txt")

  $want = (Select-String -Path (Join-Path $tmp "checksums.txt") -Pattern ([regex]::Escape($archive)) |
           ForEach-Object { ($_ -split '\s+')[0] } | Select-Object -First 1)
  if (-not $want) { throw "no checksum listed for $archive" }
  $got = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $tmp $archive)).Hash.ToLower()
  if ($want.ToLower() -ne $got) { throw "checksum mismatch — refusing to install" }
  Write-Host "  checksum verified"

  Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath $tmp -Force
  if (-not $dir) { $dir = Join-Path $env:LOCALAPPDATA "$binary\bin" }
  New-Item -ItemType Directory -Force -Path $dir | Out-Null
  Move-Item -Force -Path (Join-Path $tmp "$binary.exe") -Destination (Join-Path $dir "$binary.exe")
  Write-Host "  installed to $dir\$binary.exe"

  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if ($userPath -notlike "*$dir*") {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$dir", "User")
    Write-Host "  added $dir to your PATH (restart your shell)"
  }
  & (Join-Path $dir "$binary.exe") doctor
  Write-Host "  done. run '$binary --help' to get started."
}
finally { Remove-Item -Recurse -Force $tmp }
