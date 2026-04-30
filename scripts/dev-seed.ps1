param(
    [string]$ServerUrl = $(if ($env:GATEPILOT_SERVER_URL) { $env:GATEPILOT_SERVER_URL } else { "http://127.0.0.1:8080" }),
    [string]$TenantId = "00000000-0000-0000-0000-000000000100",
    [string]$IdempotencyKey = [guid]::NewGuid().ToString()
)

$ErrorActionPreference = "Stop"

function Invoke-GatePilotJson {
    param(
        [string]$Uri,
        [string]$Method = "Get",
        [object]$Body = $null,
        [hashtable]$Headers = @{}
    )

    $args = @{
        Uri = $Uri
        Method = $Method
        TimeoutSec = 10
        Headers = $Headers
    }
    if ($null -ne $Body) {
        $args.ContentType = "application/json"
        $args.Body = ($Body | ConvertTo-Json -Depth 8)
    }

    Invoke-RestMethod @args
}

Invoke-GatePilotJson -Uri "$ServerUrl/api/v1/healthz" | Out-Null

$activation = Invoke-GatePilotJson `
    -Uri "$ServerUrl/api/v1/tenants/$TenantId/device-activation-codes" `
    -Method Post `
    -Headers @{ "Idempotency-Key" = $IdempotencyKey } `
    -Body @{
        name = "Seeded Dev Device"
        expires_in_seconds = 3600
    }

[pscustomobject]@{
    tenant_id = $TenantId
    users = @(
        @{ email = "owner@example.local"; role = "owner" }
        @{ email = "admin@example.local"; role = "admin" }
        @{ email = "approver@example.local"; role = "approver" }
        @{ email = "viewer@example.local"; role = "viewer" }
    )
    activation_code = $activation.data.activation_code
    activation_expires_at = $activation.data.expires_at
} | ConvertTo-Json -Depth 8
