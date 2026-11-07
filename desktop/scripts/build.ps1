[CmdletBinding()]
param(
    [string]$Version = "0.1.0",
    [string]$Commit = "unknown",
    [string]$OutputDirectory = "",
    [string]$BuildDate = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$workspaceRoot = Split-Path -Parent $repoRoot
$goExe = Join-Path $repoRoot ".tools\go-sdk\go\bin\go.exe"
if (-not (Test-Path -LiteralPath $goExe)) {
    # CI (actions/setup-go) puts the same pinned version on PATH instead of .tools.
    $goCommand = Get-Command go.exe -ErrorAction SilentlyContinue
    if (-not $goCommand) {
        throw "Pinned Go 1.27.3 toolchain is missing. Install it at .tools\go-sdk\go first."
    }
    $goExe = $goCommand.Source
}
$actualVersion = & $goExe version
if ($actualVersion -notmatch "go1\.27\.3 windows/amd64") {
    throw "Expected Go 1.27.3 windows/amd64, found: $actualVersion"
}

$outputDir = Join-Path $repoRoot "dist\windows-amd64"
if (-not [string]::IsNullOrWhiteSpace($OutputDirectory)) {
    if (-not [System.IO.Path]::IsPathRooted($OutputDirectory)) {
        throw "OutputDirectory must be an absolute path."
    }
    $outputDir = [System.IO.Path]::GetFullPath($OutputDirectory)
}
New-Item -ItemType Directory -Path $outputDir -Force | Out-Null
$output = Join-Path $outputDir "work-anywhere.exe"
$iconSource = Join-Path $workspaceRoot "waw-app-icon.svg"
$iconFallback = Join-Path $repoRoot "packaging\windows\icon.png"
$iconOutputDir = Join-Path $repoRoot "winres"
$iconOutput = Join-Path $iconOutputDir "icon.png"
$webIcon = Join-Path $repoRoot "internal\daemon\web\waw-app-icon.svg"
$manifestSource = Join-Path $repoRoot "packaging\windows\work-anywhere.manifest"
if (-not (Test-Path -LiteralPath $manifestSource)) {
    throw "Windows application manifest is missing: $manifestSource"
}
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
    Copy-Item -LiteralPath $manifestSource -Destination (Join-Path $iconOutputDir "work-anywhere.manifest") -Force
    $resourceDefinition = [ordered]@{
        RT_GROUP_ICON = @{ "#1" = @{ "0000" = "icon.png" } }
        RT_MANIFEST   = @{ "#1" = @{ "0409" = "work-anywhere.manifest" } }
        RT_VERSION    = @{ "#1" = @{ "0000" = [ordered]@{
            fixed = [ordered]@{ file_version = $resourceVersion; product_version = $resourceVersion }
            info  = @{ "0409" = [ordered]@{
                FileDescription  = "work-anywhere.exe"
                FileVersion      = $resourceVersion
                InternalName     = "work-anywhere"
                OriginalFilename = "work-anywhere.exe"
                ProductName      = "Work Anywhere"
                ProductVersion   = $Version
            } }
        } } }
    }
    $resourceJSON = Join-Path $iconOutputDir "winres.json"
    [System.IO.File]::WriteAllText($resourceJSON, ($resourceDefinition | ConvertTo-Json -Depth 8), [System.Text.UTF8Encoding]::new($false))
    & $goExe run github.com/tc-hib/go-winres@v0.3.3 make --arch amd64 --in $resourceJSON --out cmd/work-anywhere/rsrc
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
