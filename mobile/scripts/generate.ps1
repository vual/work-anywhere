param(
    [string]$AndroidSdk = $env:ANDROID_HOME
)

$ErrorActionPreference = 'Stop'
$mobileRoot = Split-Path -Parent $PSScriptRoot
$flutter = Join-Path $mobileRoot '.tools\flutter\bin\flutter.bat'

& (Join-Path $PSScriptRoot 'build-core-android.ps1') -AndroidSdk $AndroidSdk
Push-Location (Join-Path $mobileRoot 'app')
try {
    & $flutter pub get
    & (Join-Path (Split-Path -Parent $flutter) 'dart.bat') run pigeon --input pigeons/mobile_core.dart --dart_out lib/core/mobile_core.g.dart --kotlin_out android/app/src/main/kotlin/io/workanywhere/work_anywhere/MobileCore.g.kt --kotlin_package io.workanywhere.work_anywhere --swift_out ios/Runner/MobileCore.g.swift
    if ($LASTEXITCODE -ne 0) { throw "Pigeon generation failed with exit code $LASTEXITCODE" }
} finally {
    Pop-Location
}
