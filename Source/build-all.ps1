$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$Version = (Get-Content (Join-Path $Root 'VERSION') -TotalCount 1).Trim()
& (Join-Path $Root 'build-native.ps1')
& (Join-Path $Root 'build-launcher.ps1')
Write-Host "Republic Mod Loader v$Version binaries are in $(Join-Path $Root 'build')"
