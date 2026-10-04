<#
.SYNOPSIS
  Build an Ubuntu/Linux amd64 s-ui binary from Windows.
.DESCRIPTION
  Uses only the installed Windows Go toolchain with CGO disabled (s-ui uses
  pure-Go SQLite), then validates the output as an ELF binary. No WSL script
  or Linux shell is invoked.
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

function Compress-Gzip([string]$InputPath, [string]$OutputPath) {
  $input = [System.IO.File]::OpenRead($InputPath)
  $output = [System.IO.File]::Create($OutputPath)
  try {
    $gzip = [System.IO.Compression.GzipStream]::new($output, [System.IO.Compression.CompressionMode]::Compress, $false)
    try { $input.CopyTo($gzip) } finally { $gzip.Dispose() }
  } finally { $input.Dispose(); $output.Dispose() }
}

# Native Windows Go cross-compilation. CGO must remain disabled.
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
Compress-Gzip $Binary $Gzip
Get-FileHash $Binary -Algorithm SHA256
Get-FileHash $Gzip -Algorithm SHA256
Write-Host "Built Linux amd64 ELF: $Binary"
