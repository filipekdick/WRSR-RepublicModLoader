$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$Build = Join-Path $Root 'build'
$Out = Join-Path $Build 'RepublicModLoader.exe'
$Version = (Get-Content (Join-Path $Root 'VERSION') -TotalCount 1).Trim()
New-Item -ItemType Directory -Force $Build | Out-Null

Push-Location (Join-Path $Root 'launcher')
try {
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $LdFlags = "-H=windowsgui -s -w -buildid= -X main.launcherVersion=$Version"
    go build -trimpath -ldflags $LdFlags -o $Out .
} finally {
    Pop-Location
}

python (Join-Path $Root 'tools\pe_resources.py') $Out `
    --icon (Join-Path $Root 'assets\RepublicModLoader.ico') `
    --version $Version `
    --file-description 'Republic Mod Loader' `
    --original-filename 'RepublicModLoader.exe'
python (Join-Path $Root 'tools\pe_finalize.py') $Out
Write-Host "Built Republic Mod Loader v$Version at $Out"
