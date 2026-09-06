[CmdletBinding()]
param(
    [string]$Version = 'dev',
    [string]$Revision = 'unknown',
    [string]$BuildTime = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
)

$ErrorActionPreference = 'Stop'

$relayRoot = Split-Path -Parent $PSScriptRoot
$repositoryRoot = Split-Path -Parent $relayRoot
$goExe = Join-Path $repositoryRoot 'desktop\.tools\go-sdk\go\bin\go.exe'
$outputDir = Join-Path $relayRoot 'dist\linux-amd64'

if (-not (Test-Path -LiteralPath $goExe)) {
    throw "Go 1.26.5 toolchain was not found at $goExe"
}

Push-Location $relayRoot
try {
    & $goExe test ./...
    if ($LASTEXITCODE -ne 0) { throw 'go test failed' }
    & $goExe vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }
    New-Item -ItemType Directory -Force -Path $outputDir | Out-Null
    $oldCgo, $oldGoos, $oldGoarch = $env:CGO_ENABLED, $env:GOOS, $env:GOARCH
    try {
        $env:CGO_ENABLED = '0'
        $env:GOOS = 'linux'
        $env:GOARCH = 'amd64'
        $ldflags = "-s -w -X github.com/vual/work-anywhere/relay/internal/buildinfo.Version=$Version -X github.com/vual/work-anywhere/relay/internal/buildinfo.Revision=$Revision -X github.com/vual/work-anywhere/relay/internal/buildinfo.BuildTime=$BuildTime"
        & $goExe build -trimpath -ldflags $ldflags -o (Join-Path $outputDir 'relay') ./cmd/relay
        if ($LASTEXITCODE -ne 0) { throw 'relay build failed' }
    }
    finally {
        $env:CGO_ENABLED, $env:GOOS, $env:GOARCH = $oldCgo, $oldGoos, $oldGoarch
    }
}
finally {
    Pop-Location
}
