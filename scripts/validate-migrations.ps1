$ErrorActionPreference = "Stop"

$migrationDir = Resolve-Path "$PSScriptRoot\..\server\migrations"
$upFiles = Get-ChildItem -Path $migrationDir -Filter "*.up.sql" | Sort-Object Name

if ($upFiles.Count -eq 0) {
    throw "no migration up files found"
}

foreach ($up in $upFiles) {
    $downName = $up.Name -replace "\.up\.sql$", ".down.sql"
    $downPath = Join-Path $migrationDir $downName
    if (-not (Test-Path $downPath)) {
        throw "missing down migration for $($up.Name)"
    }
    Write-Output "migration pair ok: $($up.Name) / $downName"
}

$core = Get-Content -Raw (Join-Path $migrationDir "0001_core_schema.up.sql")
$requiredTables = @(
    "tenants",
    "device_activation_codes",
    "devices",
    "client_instances",
    "device_tokens",
    "sessions",
    "approval_requests",
    "approval_deliveries",
    "approval_notifications",
    "approval_actions",
    "audit_logs",
    "http_idempotency_keys"
)

foreach ($table in $requiredTables) {
    if ($core -notmatch "CREATE TABLE IF NOT EXISTS $table") {
        throw "missing required table in 0001 migration: $table"
    }
    Write-Output "table ok: $table"
}

