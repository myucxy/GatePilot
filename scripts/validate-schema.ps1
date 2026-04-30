$ErrorActionPreference = "Stop"

$jsonFiles = Get-ChildItem -Path "$PSScriptRoot\..\schema\ws" -Filter "*.json" -File
foreach ($file in $jsonFiles) {
    Get-Content $file.FullName -Raw | ConvertFrom-Json | Out-Null
    Write-Output "json ok: $($file.Name)"
}

$yamlFiles = @(
    "$PSScriptRoot\..\schema\openapi.yaml",
    "$PSScriptRoot\..\schema\enums.yaml",
    "$PSScriptRoot\..\schema\errors.yaml"
)

foreach ($file in $yamlFiles) {
    if (-not (Test-Path $file)) {
        throw "missing schema file: $file"
    }
    Write-Output "yaml present: $(Split-Path $file -Leaf)"
}
