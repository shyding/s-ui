<#
.SYNOPSIS
  Build an Ubuntu/Linux amd64 s-ui binary from Windows.
.DESCRIPTION
  Uses Ubuntu/WSL when a Linux Go toolchain is available. Otherwise it uses
  the installed Windows Go toolchain with CGO disabled (s-ui uses pure-Go
  SQLite), then validates the output as an ELF binary.
#>
[CmdletBinding()]
param(
  [switch]$SkipFrontend,
  [string]$OutputDirectory = ""
)

$ErrorActionPreference = "Stop"
$Root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
  $OutputDirectory = Join-Path $Root "dist\linux-amd64"
}
New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
$Binary = Join-Path $OutputDirectory "sui"
$Gzip = "$Binary.gz"
$Tags = "with_quic,with_grpc,with_utls,with_acme,with_gvisor"

function Assert-Elf([string]$Path) {
  $bytes = [System.IO.File]::ReadAllBytes($Path)
  if ($bytes.Length -lt 4 -or $bytes[0] -ne 0x7f -or $bytes[1] -ne 0x45 -or $bytes[2] -ne 0x4c -or $bytes[3] -ne 0x46) {
    throw "Output is not an ELF binary: $Path"
  }
}

# Preferred path: a real Ubuntu toolchain in WSL, matching CI/server builds.
$wsl = Get-Command wsl.exe -ErrorAction SilentlyContinue
if ($wsl) {
  $linuxGo = (& wsl.exe bash -lc "command -v go >/dev/null 2>&1 && go version" 2>$null | Out-String).Trim()
  if ($linuxGo -match "go1\.26") {
    $linuxRoot = (& wsl.exe wslpath -a "$Root").Trim()
    $skip = if ($SkipFrontend) { "SKIP_FRONTEND=1 " } else { "" }
    & wsl.exe bash -lc "cd '$linuxRoot' && ${skip}./scripts/build_linux.sh '$linuxRoot/dist/linux-amd64'"
    if ($LASTEXITCODE -ne 0) { throw "WSL Linux build failed ($LASTEXITCODE)" }
    Assert-Elf $Binary
    Write-Host "Built with Ubuntu/WSL: $Binary"
    exit 0
  }
}

# Fallback: native Windows Go cross-compilation. CGO must remain disabled.
$go = Get-Command go.exe -ErrorAction SilentlyContinue
if (-not $go) { throw "Go was not found. Install Go 1.26.x or install it inside WSL." }
$version = (& go.exe version).Trim()
if ($version -notmatch "go1\.26") { throw "Go 1.26.x required, found: $version" }
if (-not $SkipFrontend) {
  if (-not (Get-Command npm.exe -ErrorAction SilentlyContinue)) { throw "npm not found; use -SkipFrontend or install Node.js." }
  Push-Location (Join-Path $Root "frontend")
  npm ci
  npm run build
  Pop-Location
  $web = Join-Path $Root "web\html"
  New-Item -ItemType Directory -Force -Path $web | Out-Null
  Get-ChildItem $web -Force | Remove-Item -Recurse -Force
  Copy-Item (Join-Path $Root "frontend\dist\*") $web -Recurse -Force
}
$oldGoOS=$env:GOOS; $oldGoArch=$env:GOARCH; $oldCgo=$env:CGO_ENABLED
try {
  $env:GOOS="linux"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"
  Push-Location $Root
  go.exe build -trimpath -ldflags="-w -s" -tags $Tags -o $Binary ./main.go
  if ($LASTEXITCODE -ne 0) { throw "Windows cross-build failed ($LASTEXITCODE)" }
} finally {
  Pop-Location
  $env:GOOS=$oldGoOS; $env:GOARCH=$oldGoArch; $env:CGO_ENABLED=$oldCgo
}
Assert-Elf $Binary
if ($wsl) { & wsl.exe gzip -f -9 ((& wsl.exe wslpath -a $Binary).Trim()) }
if (-not (Test-Path $Gzip)) { throw "gzip output was not created: $Gzip" }
Get-FileHash $Binary -Algorithm SHA256
Get-FileHash $Gzip -Algorithm SHA256
Write-Host "Built Linux amd64 ELF: $Binary"
