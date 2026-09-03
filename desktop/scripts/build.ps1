[CmdletBinding()]
param(
    [string]$Version = "0.1.0",
    [string]$Commit = "unknown",
    [string]$BuildDate = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$goExe = Join-Path $repoRoot ".tools\go-sdk\go\bin\go.exe"
if (-not (Test-Path -LiteralPath $goExe)) {
    throw "Pinned Go 1.26.5 toolchain is missing. Install it at .tools\go-sdk\go first."
}
$actualVersion = & $goExe version
if ($actualVersion -notmatch "go1\.26\.5 windows/amd64") {
    throw "Expected Go 1.26.5 windows/amd64, found: $actualVersion"
}

$outputDir = Join-Path $repoRoot "dist\windows-amd64"
New-Item -ItemType Directory -Path $outputDir -Force | Out-Null
$output = Join-Path $outputDir "work-anywhere.exe"
$ldflags = "-s -w -X main.version=$Version -X main.commit=$Commit -X main.date=$BuildDate"

Push-Location $repoRoot
try {
    $env:CGO_ENABLED = "0"
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    $versionParts = [regex]::Matches($Version, '\d+') | ForEach-Object { $_.Value }
    while ($versionParts.Count -lt 4) { $versionParts += '0' }
    $resourceVersion = ($versionParts | Select-Object -First 4) -join '.'
    & $goExe run github.com/tc-hib/go-winres@v0.3.3 simply --arch amd64 --out cmd/work-anywhere/rsrc --product-version $resourceVersion --file-version $resourceVersion --manifest cli --file-description "Work Anywhere Codex Bridge and Daemon" --product-name "Work Anywhere" --original-filename "work-anywhere.exe"
    if ($LASTEXITCODE -ne 0) { throw "Windows resource generation failed." }
    & $goExe test ./...
    if ($LASTEXITCODE -ne 0) { throw "Tests failed." }
    & $goExe build -trimpath -buildvcs=false -ldflags $ldflags -o $output ./cmd/work-anywhere
    if ($LASTEXITCODE -ne 0) { throw "Build failed." }
    $hash = (Get-FileHash -LiteralPath $output -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-Host "Built $output"
    Write-Host "SHA256 $hash"
} finally {
    Pop-Location
}
