$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path "$PSScriptRoot\.."
$go = "D:\Dev\Env\Go\bin\go.exe"
$tenantId = "00000000-0000-0000-0000-000000000100"
$serverURL = "http://127.0.0.1:18080"

$env:GATEPILOT_SERVER_ADDR = "127.0.0.1:18080"
$server = Start-Process -FilePath $go -ArgumentList "run .\cmd\server" -WorkingDirectory "$repoRoot\server" -WindowStyle Hidden -PassThru

try {
    $ready = $false
    for ($i = 0; $i -lt 30; $i++) {
        Start-Sleep -Seconds 1
        try {
            Invoke-RestMethod -Uri "$serverURL/api/v1/healthz" -TimeoutSec 2 | Out-Null
            $ready = $true
            break
        } catch {
        }
    }
    if (-not $ready) {
        throw "server not ready"
    }

    $activationBody = @{
        name = "CLI Test Device"
        expires_in_seconds = 600
    } | ConvertTo-Json

    $activation = Invoke-RestMethod `
        -Uri "$serverURL/api/v1/tenants/$tenantId/device-activation-codes" `
        -Method Post `
        -ContentType "application/json" `
        -Headers @{ "Idempotency-Key" = [guid]::NewGuid().ToString() } `
        -Body $activationBody

    $env:GATEPILOT_SERVER_URL = $serverURL
    $registeredOutput = & $go run "$repoRoot\agent\cmd\agent" register --activation-code $activation.data.activation_code
    if ($LASTEXITCODE -ne 0) { throw "agent register failed" }
    $registered = $registeredOutput | ConvertFrom-Json
    $sessionOutput = & $go run "$repoRoot\agent\cmd\agent" create-session --device-id $registered.data.device_id
    if ($LASTEXITCODE -ne 0) { throw "agent create-session failed" }
    $session = $sessionOutput | ConvertFrom-Json
    $approvalOutput = & $go run "$repoRoot\agent\cmd\agent" detect-approval --device-id $registered.data.device_id --session-id $session.data.session_id
    if ($LASTEXITCODE -ne 0) { throw "agent detect-approval failed" }
    $approval = $approvalOutput | ConvertFrom-Json
    $decisionBody = @{
        decision_type = "approve"
        payload = ""
    } | ConvertTo-Json
    $decision = Invoke-RestMethod `
        -Uri "$serverURL/api/v1/approvals/$($approval.data.approval_id)/decision" `
        -Method Post `
        -ContentType "application/json" `
        -Headers @{
            "Idempotency-Key" = [guid]::NewGuid().ToString()
            "X-Client-Instance-Id" = "00000000-0000-0000-0000-000000000200"
        } `
        -Body $decisionBody
    $ackOutput = & $go run "$repoRoot\agent\cmd\agent" ack-decision --approval-id $approval.data.approval_id --delivery-id $decision.data.delivery_id --session-id $session.data.session_id
    if ($LASTEXITCODE -ne 0) { throw "agent ack-decision failed" }
    $ack = $ackOutput | ConvertFrom-Json
    $sessions = Invoke-RestMethod -Uri "$serverURL/api/v1/devices/$($registered.data.device_id)/sessions"

    [pscustomobject]@{
        activation_code = $activation.data.activation_code
        device_id = $registered.data.device_id
        session_id = $session.data.session_id
        approval_id = $approval.data.approval_id
        delivery_id = $decision.data.delivery_id
        approval_status = $ack.data.status
        delivery_status = $ack.data.delivery_status
        session_count = $sessions.data.items.Count
    } | ConvertTo-Json
} finally {
    Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
    $env:GATEPILOT_SERVER_ADDR = $null
}
