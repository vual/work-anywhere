[CmdletBinding()]
param(
    [string]$Version = "0.1.0",
    [string]$Commit = "unknown",
    [string]$BuildDate = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$workspaceRoot = Split-Path -Parent $repoRoot
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
$iconSource = Join-Path $workspaceRoot "waw-app-icon.svg"
$iconFallback = Join-Path $repoRoot "packaging\windows\icon.png"
$iconOutputDir = Join-Path $repoRoot "winres"
$iconOutput = Join-Path $iconOutputDir "icon.png"
$webIcon = Join-Path $repoRoot "internal\daemon\web\waw-app-icon.svg"
if (-not (Test-Path -LiteralPath $iconSource)) {
    throw "Source icon is missing: $iconSource"
}
if (-not (Test-Path -LiteralPath $iconFallback)) {
    throw "Windows icon fallback is missing: $iconFallback"
}
$webDir = Split-Path -Parent $webIcon
New-Item -ItemType Directory -Path $iconOutputDir -Force | Out-Null
New-Item -ItemType Directory -Path $webDir -Force | Out-Null
$ldflags = "-s -w -X main.version=$Version -X main.commit=$Commit -X main.date=$BuildDate"

Push-Location $repoRoot
try {
    $env:CGO_ENABLED = "0"
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    $chrome = Get-Command chrome.exe -ErrorAction SilentlyContinue
    if (-not $chrome) {
        $chromeCandidates = @(
            (Join-Path ${env:ProgramFiles} "Google\Chrome\Application\chrome.exe"),
            (Join-Path ${env:ProgramFiles(x86)} "Google\Chrome\Application\chrome.exe")
        )
        $chromePath = $chromeCandidates | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
    } else {
        $chromePath = $chrome.Source
    }
    $iconRendered = $false
    if (-not [string]::IsNullOrWhiteSpace($chromePath)) {
        $chromeProfile = Join-Path $env:TEMP "work-anywhere-icon-render"
        $iconURL = ([System.Uri]::new($iconSource)).AbsoluteUri
        & $chromePath --headless=new --disable-gpu --hide-scrollbars --no-sandbox --no-first-run --user-data-dir=$chromeProfile --allow-file-access-from-files --default-background-color=00000000 --screenshot=$iconOutput --window-size=1024,1024 $iconURL 2>$null
        $iconRendered = ($LASTEXITCODE -eq 0 -and (Test-Path -LiteralPath $iconOutput))
    }
    if (-not $iconRendered) {
        Copy-Item -LiteralPath $iconFallback -Destination $iconOutput -Force
    }
    Copy-Item -LiteralPath $iconSource -Destination $webIcon -Force
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
