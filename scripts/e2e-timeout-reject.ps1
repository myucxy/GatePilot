$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path "$PSScriptRoot\.."
$tenantId = "00000000-0000-0000-0000-000000000100"
$serverURL = "http://127.0.0.1:18083"

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
$agent = $null
$serverExe = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-server-timeout-{0}.exe" -f [guid]::NewGuid().ToString("N"))
$agentConfig = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-agent-timeout-{0}.json" -f [guid]::NewGuid().ToString("N"))
$agentOutput = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-agent-timeout-{0}.out" -f [guid]::NewGuid().ToString("N"))
$agentError = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-agent-timeout-{0}.err" -f [guid]::NewGuid().ToString("N"))
$previousAddr = $env:GATEPILOT_SERVER_ADDR
$previousURL = $env:GATEPILOT_SERVER_URL
$previousAgentConfig = $env:GATEPILOT_AGENT_CONFIG
$previousWorkerInterval = $env:GATEPILOT_WORKER_INTERVAL_SECONDS

$env:GATEPILOT_SERVER_ADDR = "127.0.0.1:18083"
$env:GATEPILOT_WORKER_INTERVAL_SECONDS = "1"
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
    if (-not $ready) { throw "server not ready" }

    $activation = Invoke-RestMethod `
        -Uri "$serverURL/api/v1/tenants/$tenantId/device-activation-codes" `
        -Method Post `
        -ContentType "application/json" `
        -Headers @{ "Idempotency-Key" = [guid]::NewGuid().ToString() } `
        -Body (@{ name = "Timeout Test Device"; expires_in_seconds = 600 } | ConvertTo-Json)

    $env:GATEPILOT_SERVER_URL = $serverURL
    $registeredOutput = & $go run "$repoRoot\agent\cmd\agent" register --activation-code $activation.data.activation_code
    if ($LASTEXITCODE -ne 0) { throw "agent register failed" }
    $registered = $registeredOutput | ConvertFrom-Json
    $auth = @{ Authorization = "Bearer $($registered.data.device_token)" }

    $session = Invoke-RestMethod `
        -Uri "$serverURL/api/v1/agent/sessions" `
        -Method Post `
        -ContentType "application/json" `
        -Headers $auth `
        -Body (@{
            device_id = $registered.data.device_id
            cli_type = "custom"
            command_line_redacted = "gatepilot fake"
            working_dir_hash = "sha256:timeout"
            last_output_summary = "fake CLI session started"
        } | ConvertTo-Json)

    $approval = Invoke-RestMethod `
        -Uri "$serverURL/api/v1/agent/approvals" `
        -Method Post `
        -ContentType "application/json" `
        -Headers $auth `
        -Body (@{
            device_id = $registered.data.device_id
            session_id = $session.data.session_id
            cli_type = "custom"
            event_type = "permission_request"
            risk_level = "high"
            prompt_text = "permission_request: allow command execution?"
            context_before = "GatePilot fake AI CLI"
            idempotency_key = [guid]::NewGuid().ToString()
            suggested_actions = @("approve", "reject", "reply")
            expires_in_seconds = 1
        } | ConvertTo-Json)

    Start-Sleep -Seconds 3

    $agent = Start-Process `
        -FilePath $go `
        -ArgumentList "run", "$repoRoot\agent\cmd\agent", "connect", "--device-id", $registered.data.device_id, "--wait-delivery" `
        -WorkingDirectory "$repoRoot" `
        -WindowStyle Hidden `
        -RedirectStandardOutput $agentOutput `
        -RedirectStandardError $agentError `
        -PassThru

    if (-not $agent.WaitForExit(30000)) {
        Stop-Process -Id $agent.Id -Force -ErrorAction SilentlyContinue
        throw "agent did not receive timeout delivery"
    }
    $out = if (Test-Path $agentOutput) { Get-Content $agentOutput -Raw } else { "" }
    $err = if (Test-Path $agentError) { Get-Content $agentError -Raw } else { "" }
    if ($out -notmatch '"ack_result"\s*:\s*"written"') {
        throw "agent did not ACK timeout delivery: stdout=$out stderr=$err"
    }

    $approvals = Invoke-RestMethod -Uri "$serverURL/api/v1/tenants/$tenantId/approvals"
    $finalApproval = $approvals.data.items | Where-Object { $_.approval_id -eq $approval.data.approval_id } | Select-Object -First 1
    if ($finalApproval.decision_type -ne "reject" -or $finalApproval.status -ne "delivered") {
        throw "timeout approval final state invalid: $($finalApproval | ConvertTo-Json -Compress)"
    }

    [pscustomobject]@{
        device_id = $registered.data.device_id
        approval_id = $approval.data.approval_id
        final_status = $finalApproval.status
        decision_type = $finalApproval.decision_type
        delivery_status = $finalApproval.delivery_status
        ack_written = $true
    } | ConvertTo-Json
} finally {
    if ($agent -and -not $agent.HasExited) {
        Stop-Process -Id $agent.Id -Force -ErrorAction SilentlyContinue
    }
    if ($server -and -not $server.HasExited) {
        Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
    }
    Remove-Item -LiteralPath $serverExe -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $agentConfig -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $agentOutput -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $agentError -Force -ErrorAction SilentlyContinue
    $env:GATEPILOT_SERVER_ADDR = $previousAddr
    $env:GATEPILOT_SERVER_URL = $previousURL
    $env:GATEPILOT_AGENT_CONFIG = $previousAgentConfig
    $env:GATEPILOT_WORKER_INTERVAL_SECONDS = $previousWorkerInterval
}
