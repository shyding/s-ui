# Project-local environment; does not alter system Python, Git or proxy settings.
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path -Parent $PSScriptRoot
$taskVenv = Join-Path $taskRoot 'work/.venv-pool'
$taskPython = Join-Path $taskVenv 'Scripts/python.exe'
$taskTemp = Join-Path $taskRoot 'work/.pool-tmp'
$taskOldTemp = $env:TEMP
$taskOldTmp = $env:TMP
try {
    New-Item -ItemType Directory -Force $taskTemp | Out-Null
    $env:TEMP = $taskTemp
    $env:TMP = $taskTemp
    if (-not (Test-Path -LiteralPath $taskPython)) {
        & py -3.12 -m venv $taskVenv
        if ($LASTEXITCODE -ne 0) { throw 'Unable to create Python 3.12 virtual environment.' }
    }
    & $taskPython -m pip install --disable-pip-version-check --no-cache-dir --no-input --retries 0 --timeout 10 -r (Join-Path $PSScriptRoot 'pool-test-requirements.txt')
    if ($LASTEXITCODE -ne 0) { throw 'Dependency installation failed.' }
    & $taskPython -m pip check
    if ($LASTEXITCODE -ne 0) { throw 'Dependency consistency check failed.' }
    & $taskPython -c 'import sys,paramiko,cryptography,bcrypt,nacl; print(sys.executable); print("Paramiko",paramiko.__version__); print("SSH dependencies: OK")'
    if ($LASTEXITCODE -ne 0) { throw 'SSH dependency import failed.' }
    Write-Host "Ready. Run tests with: & '$taskPython' <script>"
} finally {
    $env:TEMP = $taskOldTemp
    $env:TMP = $taskOldTmp
}
