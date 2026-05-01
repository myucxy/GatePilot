$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path "$PSScriptRoot\.."
$tenantId = "00000000-0000-0000-0000-000000000100"
$serverURL = "http://127.0.0.1:18081"

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
$previousAddr = $env:GATEPILOT_SERVER_ADDR
$previousURL = $env:GATEPILOT_SERVER_URL
$previousAgentConfig = $env:GATEPILOT_AGENT_CONFIG

$env:GATEPILOT_SERVER_ADDR = "127.0.0.1:18081"
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

    $activation = Invoke-RestMethod `
        -Uri "$serverURL/api/v1/tenants/$tenantId/device-activation-codes" `
        -Method Post `
        -ContentType "application/json" `
        -Headers @{ "Idempotency-Key" = [guid]::NewGuid().ToString() } `
        -Body (@{ name = "CLI Supersede Device"; expires_in_seconds = 600 } | ConvertTo-Json)

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

    $supersedeOutput = & $go run "$repoRoot\agent\cmd\agent" supersede-approval --approval-id $approval.data.approval_id --session-id $session.data.session_id --reason "operator typed locally"
    if ($LASTEXITCODE -ne 0) { throw "agent supersede failed" }
    $supersede = $supersedeOutput | ConvertFrom-Json

    $sessionDetail = Invoke-RestMethod -Uri "$serverURL/api/v1/sessions/$($session.data.session_id)"
    $audit = Invoke-RestMethod -Uri "$serverURL/api/v1/tenants/$tenantId/audit-logs?action=approval.superseded"

    if ($supersede.data.status -ne "cancelled_by_local_input") {
        throw "approval not superseded: $($supersedeOutput | Out-String)"
    }
    if ($sessionDetail.data.status -ne "running" -or $sessionDetail.data.pending_approval_count -ne 0) {
        throw "session not restored after supersede"
    }

    [pscustomobject]@{
        device_id = $registered.data.device_id
        session_id = $session.data.session_id
        approval_id = $approval.data.approval_id
        final_status = $supersede.data.status
        session_status = $sessionDetail.data.status
        audit_count = $audit.data.items.Count
    } | ConvertTo-Json
} finally {
    if ($server -and -not $server.HasExited) {
        Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
    }
    Remove-Item -LiteralPath $serverExe -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $agentConfig -Force -ErrorAction SilentlyContinue
    $env:GATEPILOT_SERVER_ADDR = $previousAddr
    $env:GATEPILOT_SERVER_URL = $previousURL
    $env:GATEPILOT_AGENT_CONFIG = $previousAgentConfig
}
