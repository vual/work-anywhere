param(
    [string]$FlutterRevision = 'd3b14c87698d761d2d8c608040306aa2de76bf31'
)

$ErrorActionPreference = 'Stop'
$mobileRoot = Split-Path -Parent $PSScriptRoot
$repoRoot = Split-Path -Parent $mobileRoot
$flutterDir = Join-Path $mobileRoot '.tools\flutter'
$toolBin = Join-Path $mobileRoot '.tools\bin'
$goExe = Join-Path $repoRoot 'desktop\.tools\go-sdk\go\bin\go.exe'

if (-not (Test-Path -LiteralPath (Join-Path $flutterDir 'bin\flutter.bat'))) {
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $flutterDir) | Out-Null
    git clone https://github.com/flutter/flutter.git $flutterDir
}

git -C $flutterDir fetch --depth 1 origin $FlutterRevision
git -C $flutterDir checkout --detach $FlutterRevision
New-Item -ItemType Directory -Force -Path $toolBin | Out-Null
$env:GOBIN = $toolBin
& $goExe install golang.org/x/mobile/cmd/gomobile@v0.0.0-20260908204917-8b95e45f8d3e
& $goExe install golang.org/x/mobile/cmd/gobind@v0.0.0-20260908204917-8b95e45f8d3e
Push-Location (Join-Path $mobileRoot 'core')
try {
    & $goExe get -tool golang.org/x/mobile/cmd/gobind@v0.0.0-20260908204917-8b95e45f8d3e
    if ($LASTEXITCODE -ne 0) { throw 'Failed to register the gomobile tool dependency' }
} finally {
    Pop-Location
}
$env:PATH = "$(Split-Path -Parent $goExe);$toolBin;$env:PATH"
& (Join-Path $toolBin 'gomobile.exe') init
& (Join-Path $flutterDir 'bin\flutter.bat') --version
