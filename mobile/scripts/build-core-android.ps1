param(
    [string]$AndroidSdk = $env:ANDROID_HOME
)

$ErrorActionPreference = 'Stop'
$mobileRoot = Split-Path -Parent $PSScriptRoot
$repoRoot = Split-Path -Parent $mobileRoot
$goBin = Join-Path $repoRoot 'desktop\.tools\go-sdk\go\bin'
$toolBin = Join-Path $mobileRoot '.tools\bin'
$outputDir = Join-Path $mobileRoot 'app\android\app\libs'

if (-not $AndroidSdk -or -not (Test-Path -LiteralPath $AndroidSdk)) {
    throw 'Android SDK not found. Set ANDROID_HOME or pass -AndroidSdk.'
}

$env:ANDROID_HOME = (Resolve-Path -LiteralPath $AndroidSdk).Path
$env:ANDROID_SDK_ROOT = $env:ANDROID_HOME
$env:PATH = "$goBin;$toolBin;$env:PATH"
New-Item -ItemType Directory -Force -Path $outputDir | Out-Null

Push-Location (Join-Path $mobileRoot 'core')
try {
    & (Join-Path $toolBin 'gomobile.exe') bind -target=android -androidapi 29 -o (Join-Path $outputDir 'mobilecore.aar') ./mobilecore
    if ($LASTEXITCODE -ne 0) { throw "gomobile bind failed with exit code $LASTEXITCODE" }
} finally {
    Pop-Location
}
