$ErrorActionPreference = 'Stop'

$relayRoot = Split-Path -Parent $PSScriptRoot
$toolBin = Join-Path $relayRoot '.tools\bin'
$goExe = Join-Path (Split-Path -Parent $relayRoot) 'desktop\.tools\go-sdk\go\bin\go.exe'

if (-not (Test-Path -LiteralPath $goExe)) {
    throw "Go 1.27.3 toolchain was not found at $goExe"
}

New-Item -ItemType Directory -Force -Path $toolBin | Out-Null
$bufExe = Join-Path $toolBin 'buf.exe'
if (-not (Test-Path -LiteralPath $bufExe)) {
    $oldGobin = $env:GOBIN
    try {
        $env:GOBIN = $toolBin
        & $goExe install github.com/bufbuild/buf/cmd/buf@v1.57.2
        if ($LASTEXITCODE -ne 0) { throw 'failed to install Buf' }
    }
    finally {
        $env:GOBIN = $oldGobin
    }
}

Push-Location $relayRoot
try {
    & $bufExe lint
    if ($LASTEXITCODE -ne 0) { throw 'buf lint failed' }
    & $bufExe generate
    if ($LASTEXITCODE -ne 0) { throw 'buf generate failed' }
    & $bufExe generate --template buf.gen.desktop.yaml
    if ($LASTEXITCODE -ne 0) { throw 'desktop buf generate failed' }
}
finally {
    Pop-Location
}
