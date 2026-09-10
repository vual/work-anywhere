param(
    [string]$AndroidSdk = $env:ANDROID_HOME,
    [ValidateSet('debug', 'release')]
    [string]$Mode = 'debug'
)

$ErrorActionPreference = 'Stop'
$mobileRoot = Split-Path -Parent $PSScriptRoot
$flutter = Join-Path $mobileRoot '.tools\flutter\bin\flutter.bat'

& (Join-Path $PSScriptRoot 'generate.ps1') -AndroidSdk $AndroidSdk
Push-Location (Join-Path $mobileRoot 'app')
try {
    & $flutter build apk "--$Mode"
    if ($LASTEXITCODE -ne 0) { throw "Android $Mode build failed with exit code $LASTEXITCODE" }
} finally {
    Pop-Location
}
