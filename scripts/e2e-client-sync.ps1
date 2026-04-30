$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path "$PSScriptRoot\.."
$tenantId = "00000000-0000-0000-0000-000000000100"
$serverURL = "http://127.0.0.1:18081"
$serverWSURL = "ws://127.0.0.1:18081"

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

function Receive-WSJson {
    param([System.Net.WebSockets.ClientWebSocket]$Socket)

    $buffer = New-Object byte[] 8192
    $segment = [ArraySegment[byte]]::new($buffer)
    $builder = [System.Text.StringBuilder]::new()
    do {
        $result = $Socket.ReceiveAsync($segment, [Threading.CancellationToken]::None).GetAwaiter().GetResult()
        if ($result.MessageType -eq [System.Net.WebSockets.WebSocketMessageType]::Close) {
            throw "websocket closed before message was received"
        }
        [void]$builder.Append([System.Text.Encoding]::UTF8.GetString($buffer, 0, $result.Count))
    } while (-not $result.EndOfMessage)

    return ($builder.ToString() | ConvertFrom-Json)
}

function Receive-WSJsonType {
    param(
        [System.Net.WebSockets.ClientWebSocket]$Socket,
        [string]$Type
    )

    for ($i = 0; $i -lt 10; $i++) {
        $message = Receive-WSJson -Socket $Socket
        if ($message.type -eq $Type) {
            return $message
        }
    }
    throw "websocket message type $Type was not received"
}

$go = Resolve-Go
$server = $null
$socket = $null
$serverExe = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-server-client-sync-{0}.exe" -f [guid]::NewGuid().ToString("N"))
$previousAddr = $env:GATEPILOT_SERVER_ADDR
$env:GATEPILOT_SERVER_ADDR = "127.0.0.1:18081"

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

    $client = Invoke-RestMethod `
        -Uri "$serverURL/api/v1/client-instances" `
        -Method Post `
        -ContentType "application/json" `
        -Headers @{ "Idempotency-Key" = [guid]::NewGuid().ToString() } `
        -Body (@{
            tenant_id = $tenantId
            client_type = "web"
            display_name = "E2E Browser"
            app_version = "0.1.0"
            platform = "browser"
        } | ConvertTo-Json)

    $clientInstanceId = $client.data.client_instance_id
    $socket = [System.Net.WebSockets.ClientWebSocket]::new()
    [void]$socket.ConnectAsync([Uri]"$serverWSURL/ws/client?tenant_id=$tenantId&client_instance_id=$clientInstanceId", [Threading.CancellationToken]::None).GetAwaiter().GetResult()
    $connected = Receive-WSJson -Socket $socket
    if ($connected.type -ne "client.connected") {
        throw "unexpected client websocket first message: $($connected | ConvertTo-Json -Compress)"
    }

    $activation = Invoke-RestMethod `
        -Uri "$serverURL/api/v1/tenants/$tenantId/device-activation-codes" `
        -Method Post `
        -ContentType "application/json" `
        -Headers @{ "Idempotency-Key" = [guid]::NewGuid().ToString() } `
        -Body (@{ name = "Client Sync Test Device"; expires_in_seconds = 600 } | ConvertTo-Json)

    $registered = Invoke-RestMethod `
        -Uri "$serverURL/api/v1/agent/register" `
        -Method Post `
        -ContentType "application/json" `
        -Body (@{
            activation_code = $activation.data.activation_code
            device_name = "client-sync-agent"
            platform = "windows"
            arch = "amd64"
            agent_version = "0.1.0-dev"
            protocol_version = "2026-04-01"
            capabilities = @{ conpty = $true }
        } | ConvertTo-Json)

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
            working_dir_hash = "sha256:e2e"
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
            expires_in_seconds = 300
        } | ConvertTo-Json)

    $created = Receive-WSJsonType -Socket $socket -Type "approval.created"
    if ($created.type -ne "approval.created" -or $created.payload.approval_id -ne $approval.data.approval_id) {
        throw "unexpected approval.created message: $($created | ConvertTo-Json -Compress)"
    }

    $decision = Invoke-RestMethod `
        -Uri "$serverURL/api/v1/approvals/$($approval.data.approval_id)/decision" `
        -Method Post `
        -ContentType "application/json" `
        -Headers @{
            "Idempotency-Key" = [guid]::NewGuid().ToString()
            "X-Client-Instance-Id" = $clientInstanceId
        } `
        -Body (@{ decision_type = "approve"; payload = "" } | ConvertTo-Json)

    $updated = Receive-WSJsonType -Socket $socket -Type "approval.updated"
    if ($updated.type -ne "approval.updated" -or $updated.payload.approval_id -ne $approval.data.approval_id -or $updated.payload.status -ne "delivering") {
        throw "unexpected approval.updated message: $($updated | ConvertTo-Json -Compress)"
    }

    [pscustomobject]@{
        client_instance_id = $clientInstanceId
        approval_id = $approval.data.approval_id
        created_event = $created.type
        updated_event = $updated.type
        decision_status = $decision.data.status
        websocket_status = $connected.type
    } | ConvertTo-Json
} finally {
    if ($socket) {
        $socket.Dispose()
    }
    if ($server -and -not $server.HasExited) {
        Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
    }
    Remove-Item -LiteralPath $serverExe -Force -ErrorAction SilentlyContinue
    $env:GATEPILOT_SERVER_ADDR = $previousAddr
}
