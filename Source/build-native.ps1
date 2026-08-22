$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$BuildRoot = Join-Path $Root 'build'
$CMakeBuild = Join-Path $BuildRoot 'native-build'
$Out = Join-Path $BuildRoot 'rml_runtime.dll'
$Version = (Get-Content (Join-Path $Root 'VERSION') -TotalCount 1).Trim()
New-Item -ItemType Directory -Force $BuildRoot | Out-Null

cmake -S $Root -B $CMakeBuild -A x64
cmake --build $CMakeBuild --config Release

$Dll = Get-ChildItem -Path $CMakeBuild -Filter 'rml_runtime.dll' -File -Recurse |
    Sort-Object LastWriteTime -Descending |
    Select-Object -First 1
if (-not $Dll) {
    throw 'CMake completed but rml_runtime.dll was not found.'
}
Copy-Item -Force $Dll.FullName $Out
python (Join-Path $Root 'tools\pe_resources.py') $Out `
    --version $Version `
    --file-description 'Republic Mod Loader Runtime' `
    --original-filename 'rml_runtime.dll' `
    --dll
python (Join-Path $Root 'tools\pe_finalize.py') $Out
Write-Host "Built Republic Mod Loader Runtime v$Version at $Out"
