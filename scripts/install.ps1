# Installs zx on Windows for the current user. No admin rights needed.
#
#   irm https://raw.githubusercontent.com/jameymcelveen/zxcli/main/scripts/install.ps1 | iex
#
# Options (environment variables):
#   ZX_VERSION      a release tag such as v0.1.0 (default: the latest release)
#   ZX_INSTALL_DIR  where to put zx.exe (default: %LOCALAPPDATA%\Programs\zx)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$repo = 'jameymcelveen/zxcli'
$version = if ($env:ZX_VERSION) { $env:ZX_VERSION } else { 'latest' }
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$asset = "zx_windows_$arch.zip"
$base = if ($version -eq 'latest') {
  "https://github.com/$repo/releases/latest/download"
} else {
  "https://github.com/$repo/releases/download/$version"
}
$dir = if ($env:ZX_INSTALL_DIR) { $env:ZX_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\zx' }

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("zx-" + [Guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Write-Host "downloading $asset ($version)"
  Invoke-WebRequest "$base/$asset" -OutFile (Join-Path $tmp $asset) -UseBasicParsing
  Invoke-WebRequest "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt') -UseBasicParsing

  $want = $null
  foreach ($line in Get-Content (Join-Path $tmp 'checksums.txt')) {
    $parts = $line -split '\s+'
    if ($parts.Count -ge 2 -and $parts[1] -eq $asset) { $want = $parts[0] }
  }
  $got = (Get-FileHash (Join-Path $tmp $asset) -Algorithm SHA256).Hash.ToLower()
  if (-not $want -or $want -ne $got) { throw "checksum mismatch for $asset, so nothing was installed" }

  Expand-Archive (Join-Path $tmp $asset) -DestinationPath $tmp -Force
  New-Item -ItemType Directory -Path $dir -Force | Out-Null
  Copy-Item (Join-Path $tmp 'zx.exe') (Join-Path $dir 'zx.exe') -Force

  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  if (-not $userPath) { $userPath = '' }
  if (($userPath -split ';') -notcontains $dir) {
    [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ';' + $dir).TrimStart(';'), 'User')
    Write-Host "added $dir to your PATH. Open a new terminal to pick it up."
  }
  Write-Host ("installed " + (& (Join-Path $dir 'zx.exe') version))
  Write-Host "run: zx"
} finally {
  Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
