[CmdletBinding()]
param(
    [string]$Version = "0.1.0",
    [string]$Commit = "unknown",
    [string]$BuildDate = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ"),
    [string]$IsccPath = "",
    [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$buildScript = Join-Path $PSScriptRoot "build.ps1"
$binary = Join-Path $repoRoot "dist\windows-amd64\work-anywhere.exe"
$installerScript = Join-Path $repoRoot "packaging\windows\installer.iss"

if (-not $SkipBuild) {
    & $buildScript -Version $Version -Commit $Commit -BuildDate $BuildDate
    if ($LASTEXITCODE -ne 0) {
        throw "Desktop executable build failed."
    }
}

if (-not (Test-Path -LiteralPath $binary)) {
    throw "Desktop executable not found: $binary"
}

if ([string]::IsNullOrWhiteSpace($IsccPath)) {
    $command = Get-Command iscc.exe -ErrorAction SilentlyContinue
    if ($command) {
        $IsccPath = $command.Source
    } else {
        $candidates = @(
            (Join-Path ${env:ProgramFiles(x86)} "Inno Setup 6\ISCC.exe"),
            (Join-Path $env:ProgramFiles "Inno Setup 6\ISCC.exe")
        )
        $IsccPath = $candidates | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
    }
}

if ([string]::IsNullOrWhiteSpace($IsccPath) -or -not (Test-Path -LiteralPath $IsccPath)) {
    throw "Inno Setup 6 ISCC.exe was not found. Install Inno Setup 6 or pass -IsccPath explicitly."
}

$versionParts = [regex]::Matches($Version, '\d+') | ForEach-Object { $_.Value }
while ($versionParts.Count -lt 4) { $versionParts += '0' }
$fileVersion = ($versionParts | Select-Object -First 4) -join '.'

& $IsccPath "/DAppVersion=$Version" "/DAppFileVersion=$fileVersion" "/DAppCommit=$Commit" "/DAppBuildDate=$BuildDate" $installerScript
if ($LASTEXITCODE -ne 0) {
    throw "Inno Setup installer build failed."
}

$output = Join-Path $repoRoot "dist\windows-amd64\work-anywhere-setup-$Version.exe"
if (-not (Test-Path -LiteralPath $output)) {
    throw "Installer output not found: $output"
}

$hash = (Get-FileHash -LiteralPath $output -Algorithm SHA256).Hash.ToLowerInvariant()
Write-Host "Built $output"
Write-Host "SHA256 $hash"
