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
$agent = $null
$serverExe = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-server-agent-run-{0}.exe" -f [guid]::NewGuid().ToString("N"))
$agentConfig = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-agent-run-{0}.json" -f [guid]::NewGuid().ToString("N"))
$agentOutput = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-agent-run-{0}.out" -f [guid]::NewGuid().ToString("N"))
$agentError = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-agent-run-{0}.err" -f [guid]::NewGuid().ToString("N"))
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
    if (-not $ready) { throw "server not ready" }

    $activation = Invoke-RestMethod `
        -Uri "$serverURL/api/v1/tenants/$tenantId/device-activation-codes" `
        -Method Post `
        -ContentType "application/json" `
        -Headers @{ "Idempotency-Key" = [guid]::NewGuid().ToString() } `
        -Body (@{ name = "Agent Run Test Device"; expires_in_seconds = 600 } | ConvertTo-Json)

    $env:GATEPILOT_SERVER_URL = $serverURL
    $registeredOutput = & $go run "$repoRoot\agent\cmd\agent" register --activation-code $activation.data.activation_code
    if ($LASTEXITCODE -ne 0) { throw "agent register failed" }
    $registered = $registeredOutput | ConvertFrom-Json

    $agent = Start-Process `
        -FilePath $go `
        -ArgumentList "run", "$repoRoot\agent\cmd\agent", "run", "--", "fake-ai-cli" `
        -WorkingDirectory "$repoRoot" `
        -WindowStyle Hidden `
        -RedirectStandardOutput $agentOutput `
        -RedirectStandardError $agentError `
        -PassThru

    $approval = $null
    for ($i = 0; $i -lt 30; $i++) {
        if ($agent.HasExited) {
            $err = if (Test-Path $agentError) { Get-Content $agentError -Raw } else { "" }
            $out = if (Test-Path $agentOutput) { Get-Content $agentOutput -Raw } else { "" }
            throw "agent run exited before creating approval: stdout=$out stderr=$err"
        }
        Start-Sleep -Seconds 1
        $approvals = Invoke-RestMethod -Uri "$serverURL/api/v1/tenants/$tenantId/approvals?status=waiting_decision"
        $approval = $approvals.data.items | Where-Object { $_.device_id -eq $registered.data.device_id } | Select-Object -First 1
        if ($approval) { break }
    }
    if (-not $approval) { throw "agent run did not create an approval" }

    $decision = Invoke-RestMethod `
        -Uri "$serverURL/api/v1/approvals/$($approval.approval_id)/decision" `
        -Method Post `
        -ContentType "application/json" `
        -Headers @{
            "Idempotency-Key" = [guid]::NewGuid().ToString()
            "X-Client-Instance-Id" = "00000000-0000-0000-0000-000000000200"
        } `
        -Body (@{ decision_type = "approve"; payload = "" } | ConvertTo-Json)

    if (-not $agent.WaitForExit(30000)) {
        Stop-Process -Id $agent.Id -Force -ErrorAction SilentlyContinue
        throw "agent run timed out waiting for websocket decision"
    }
    $agent.Refresh()
    $out = if (Test-Path $agentOutput) { Get-Content $agentOutput -Raw } else { "" }
    $err = if (Test-Path $agentError) { Get-Content $agentError -Raw } else { "" }
    if ($null -ne $agent.ExitCode -and $agent.ExitCode -ne 0) {
        throw "agent run failed with code $($agent.ExitCode): stdout=$out stderr=$err"
    }
    if ($out -notmatch '"ack_result"\s*:\s*"written"') {
        throw "agent run did not ACK decision: stdout=$out stderr=$err"
    }

    $final = Invoke-RestMethod -Uri "$serverURL/api/v1/tenants/$tenantId/approvals"
    $finalApproval = $final.data.items | Where-Object { $_.approval_id -eq $approval.approval_id } | Select-Object -First 1
    $sessionId = $approval.session_id
    $outputChunks = Invoke-RestMethod -Uri "$serverURL/api/v1/sessions/$sessionId/output-chunks"
    if ($outputChunks.data.items.Count -lt 1) {
        throw "agent run did not append output chunks"
    }

    [pscustomobject]@{
        device_id = $registered.data.device_id
        approval_id = $approval.approval_id
        session_id = $sessionId
        delivery_id = $decision.data.delivery_id
        final_status = $finalApproval.status
        delivery_status = $finalApproval.delivery_status
        output_chunk_count = $outputChunks.data.items.Count
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
}
