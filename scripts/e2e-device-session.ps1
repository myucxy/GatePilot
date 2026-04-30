$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path "$PSScriptRoot\.."
$tenantId = "00000000-0000-0000-0000-000000000100"
$serverURL = "http://127.0.0.1:18080"

function Resolve-Go {
    if ($env:GATEPILOT_GO -and (Test-Path $env:GATEPILOT_GO)) {
        return $env:GATEPILOT_GO
    }

    $defaultGo = "D:\Dev\Env\Go\bin\go.exe"
    if (Test-Path $defaultGo) {
        return $defaultGo
    }

    $pathGo = Get-Command go -ErrorAction SilentlyContinue
    if ($pathGo) {
        return $pathGo.Source
    }

    throw "go executable not found. Set GATEPILOT_GO to the full go.exe path."
}

$go = Resolve-Go
$server = $null
$serverExe = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-server-e2e-{0}.exe" -f [guid]::NewGuid().ToString("N"))
$agentConfig = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-agent-e2e-{0}.json" -f [guid]::NewGuid().ToString("N"))
$agentDeliveryOutput = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-agent-delivery-{0}.json" -f [guid]::NewGuid().ToString("N"))
$agentDeliveryError = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-agent-delivery-{0}.err" -f [guid]::NewGuid().ToString("N"))
$agentReadyFile = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-agent-ready-{0}.txt" -f [guid]::NewGuid().ToString("N"))
$previousAddr = $env:GATEPILOT_SERVER_ADDR
$previousURL = $env:GATEPILOT_SERVER_URL
$previousAgentConfig = $env:GATEPILOT_AGENT_CONFIG

$env:GATEPILOT_SERVER_ADDR = "127.0.0.1:18080"
$env:GATEPILOT_AGENT_CONFIG = $agentConfig
& $go build -o $serverExe "$repoRoot\server\cmd\server"
if ($LASTEXITCODE -ne 0) { throw "server build failed" }
$server = Start-Process -FilePath $serverExe -WorkingDirectory "$repoRoot\server" -WindowStyle Hidden -PassThru

try {
    $ready = $false
    for ($i = 0; $i -lt 30; $i++) {
        if ($server.HasExited) {
            throw "server exited before becoming ready with code $($server.ExitCode)"
        }

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

    $connectOutput = & $go run "$repoRoot\agent\cmd\agent" connect --device-id $registered.data.device_id --once
    if ($LASTEXITCODE -ne 0) { throw "agent websocket connect failed" }
    $connected = $connectOutput | ConvertFrom-Json

    $sessionOutput = & $go run "$repoRoot\agent\cmd\agent" create-session --device-id $registered.data.device_id
    if ($LASTEXITCODE -ne 0) { throw "agent create-session failed" }
    $session = $sessionOutput | ConvertFrom-Json

    $approvalOutput = & $go run "$repoRoot\agent\cmd\agent" detect-approval --device-id $registered.data.device_id --session-id $session.data.session_id
    if ($LASTEXITCODE -ne 0) { throw "agent detect-approval failed" }
    $approval = $approvalOutput | ConvertFrom-Json

    $agentDelivery = Start-Process `
        -FilePath $go `
        -ArgumentList "run", "$repoRoot\agent\cmd\agent", "connect", "--device-id", $registered.data.device_id, "--wait-delivery", "--ready-file", $agentReadyFile `
        -WorkingDirectory "$repoRoot" `
        -WindowStyle Hidden `
        -RedirectStandardOutput $agentDeliveryOutput `
        -RedirectStandardError $agentDeliveryError `
        -PassThru

    $agentReady = $false
    for ($i = 0; $i -lt 30; $i++) {
        if ($agentDelivery.HasExited) {
            $err = if (Test-Path $agentDeliveryError) { Get-Content $agentDeliveryError -Raw } else { "" }
            throw "agent websocket delivery listener exited early: $err"
        }
        if (Test-Path $agentReadyFile) {
            $agentReady = $true
            break
        }
        Start-Sleep -Seconds 1
    }
    if (-not $agentReady) {
        Stop-Process -Id $agentDelivery.Id -Force -ErrorAction SilentlyContinue
        throw "agent websocket delivery listener not ready"
    }

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

    if (-not $agentDelivery.WaitForExit(30000)) {
        Stop-Process -Id $agentDelivery.Id -Force -ErrorAction SilentlyContinue
        throw "agent websocket delivery ack timed out"
    }
    $agentDelivery.Refresh()
    $ack = Get-Content $agentDeliveryOutput -Raw | ConvertFrom-Json
    if ($agentDelivery.ExitCode -ne 0) {
        $err = if (Test-Path $agentDeliveryError) { Get-Content $agentDeliveryError -Raw } else { "" }
        $out = if (Test-Path $agentDeliveryOutput) { Get-Content $agentDeliveryOutput -Raw } else { "" }
        if (-not $ack -or $ack.ack_result -ne "written") {
            throw "agent websocket delivery ack failed: stdout=$out stderr=$err"
        }
    }
    $sessions = Invoke-RestMethod -Uri "$serverURL/api/v1/devices/$($registered.data.device_id)/sessions"
    $approvals = Invoke-RestMethod -Uri "$serverURL/api/v1/tenants/$tenantId/approvals"
    $finalApproval = $approvals.data.items | Where-Object { $_.approval_id -eq $approval.data.approval_id } | Select-Object -First 1

    [pscustomobject]@{
        activation_code = $activation.data.activation_code
        device_id = $registered.data.device_id
        session_id = $session.data.session_id
        approval_id = $approval.data.approval_id
        delivery_id = $decision.data.delivery_id
        approval_status = $finalApproval.status
        delivery_status = $finalApproval.delivery_status
        ws_status = $connected.type
        ws_ack_result = $ack.ack_result
        session_count = $sessions.data.items.Count
    } | ConvertTo-Json
} finally {
    if ($server -and -not $server.HasExited) {
        Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
    }
    Remove-Item -LiteralPath $serverExe -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $agentConfig -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $agentDeliveryOutput -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $agentDeliveryError -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $agentReadyFile -Force -ErrorAction SilentlyContinue
    $env:GATEPILOT_SERVER_ADDR = $previousAddr
    $env:GATEPILOT_SERVER_URL = $previousURL
    $env:GATEPILOT_AGENT_CONFIG = $previousAgentConfig
}
