$ErrorActionPreference = 'Stop'
$mobileRoot = Split-Path -Parent $PSScriptRoot
$repoRoot = Split-Path -Parent $mobileRoot
$goExe = Join-Path $repoRoot 'desktop\.tools\go-sdk\go\bin\go.exe'
$flutter = Join-Path $mobileRoot '.tools\flutter\bin\flutter.bat'

Push-Location (Join-Path $mobileRoot 'core')
try {
    & $goExe test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Mobile Core tests failed' }
} finally {
    Pop-Location
}

Push-Location (Join-Path $mobileRoot 'app')
try {
    & $flutter analyze
    if ($LASTEXITCODE -ne 0) { throw 'Flutter analyze failed' }
    & $flutter test
    if ($LASTEXITCODE -ne 0) { throw 'Flutter tests failed' }
} finally {
    Pop-Location
}
