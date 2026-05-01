$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path "$PSScriptRoot\.."
$tenantId = "00000000-0000-0000-0000-000000000100"
$serverURL = "http://127.0.0.1:18082"

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
$serverExe = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-server-agent-local-ui-{0}.exe" -f [guid]::NewGuid().ToString("N"))
$agentConfig = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-agent-local-ui-{0}.json" -f [guid]::NewGuid().ToString("N"))
$agentOutput = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-agent-local-ui-{0}.out" -f [guid]::NewGuid().ToString("N"))
$agentError = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-agent-local-ui-{0}.err" -f [guid]::NewGuid().ToString("N"))
$previousAddr = $env:GATEPILOT_SERVER_ADDR
$previousURL = $env:GATEPILOT_SERVER_URL
$previousAgentConfig = $env:GATEPILOT_AGENT_CONFIG

$env:GATEPILOT_SERVER_ADDR = "127.0.0.1:18082"
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
        -Body (@{ name = "Agent Local UI Test Device"; expires_in_seconds = 600 } | ConvertTo-Json)

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

    $localUIOutput = & $go run "$repoRoot\agent\cmd\agent" local-ui `
        --tenant-id $tenantId `
        --device-id $registered.data.device_id `
        --decision approve `
        --once `
        --timeout-seconds 30
    if ($LASTEXITCODE -ne 0) { throw "agent local-ui failed: $localUIOutput" }
    $localEvents = @($localUIOutput | ForEach-Object { $_ | ConvertFrom-Json })
    $notification = $localEvents | Where-Object { $_.type -eq "local_ui.approval_notification" } | Select-Object -First 1
    $submitted = $localEvents | Where-Object { $_.type -eq "local_ui.decision_submitted" } | Select-Object -First 1
    if (-not $notification -or $notification.approval_id -ne $approval.approval_id) {
        throw "local UI did not notify expected approval: $localUIOutput"
    }
    if (-not $submitted -or $submitted.client_type -ne "agent_desktop") {
        throw "local UI did not submit agent_desktop decision: $localUIOutput"
    }

    if (-not $agent.WaitForExit(30000)) {
        Stop-Process -Id $agent.Id -Force -ErrorAction SilentlyContinue
        throw "agent run timed out waiting for local UI decision"
    }
    $agent.Refresh()
    $out = if (Test-Path $agentOutput) { Get-Content $agentOutput -Raw } else { "" }
    $err = if (Test-Path $agentError) { Get-Content $agentError -Raw } else { "" }
    if ($null -ne $agent.ExitCode -and $agent.ExitCode -ne 0) {
        throw "agent run failed with code $($agent.ExitCode): stdout=$out stderr=$err"
    }
    if ($out -notmatch '"ack_result"\s*:\s*"written"') {
        throw "agent run did not ACK local UI decision: stdout=$out stderr=$err"
    }

    $final = Invoke-RestMethod -Uri "$serverURL/api/v1/tenants/$tenantId/approvals"
    $finalApproval = $final.data.items | Where-Object { $_.approval_id -eq $approval.approval_id } | Select-Object -First 1
    if ($finalApproval.status -ne "delivered" -or $finalApproval.delivery_status -ne "acked") {
        throw "approval was not delivered and acked: $($finalApproval | ConvertTo-Json -Compress)"
    }
    if ($finalApproval.decided_by.client_type -ne "agent_desktop" -or $finalApproval.decided_by.client_instance_id -ne $submitted.client_instance_id) {
        throw "approval was not decided by local agent desktop: $($finalApproval.decided_by | ConvertTo-Json -Compress)"
    }

    [pscustomobject]@{
        device_id = $registered.data.device_id
        approval_id = $approval.approval_id
        session_id = $approval.session_id
        client_instance_id = $submitted.client_instance_id
        decided_client_type = $finalApproval.decided_by.client_type
        final_status = $finalApproval.status
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
}
