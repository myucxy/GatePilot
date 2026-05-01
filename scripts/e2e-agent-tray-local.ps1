$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path "$PSScriptRoot\.."

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
$tray = $null
$readyFile = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-tray-ready-{0}.txt" -f [guid]::NewGuid().ToString("N"))
$settingsFile = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-tray-settings-{0}.json" -f [guid]::NewGuid().ToString("N"))
$historyFile = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-tray-history-{0}.json" -f [guid]::NewGuid().ToString("N"))
$trayOutput = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-tray-{0}.out" -f [guid]::NewGuid().ToString("N"))
$trayError = Join-Path ([System.IO.Path]::GetTempPath()) ("gatepilot-tray-{0}.err" -f [guid]::NewGuid().ToString("N"))
$previousPopupDecision = $env:GATEPILOT_AGENT_POPUP_DECISION
$previousTrayAddr = $env:GATEPILOT_AGENT_TRAY_ADDR
$previousSettings = $env:GATEPILOT_AGENT_SETTINGS
$previousHistory = $env:GATEPILOT_AGENT_HISTORY
$trayPort = Get-Random -Minimum 20000 -Maximum 25000

$env:GATEPILOT_AGENT_POPUP_DECISION = "approve"
$env:GATEPILOT_AGENT_TRAY_ADDR = "127.0.0.1:$trayPort"
$env:GATEPILOT_AGENT_SETTINGS = $settingsFile
$env:GATEPILOT_AGENT_HISTORY = $historyFile

try {
    $tray = Start-Process `
        -FilePath $go `
        -ArgumentList "run", "$repoRoot\agent\cmd\agent", "tray", "--no-ui", "--ready-file", $readyFile `
        -WorkingDirectory "$repoRoot" `
        -WindowStyle Hidden `
        -RedirectStandardOutput $trayOutput `
        -RedirectStandardError $trayError `
        -PassThru

    $ready = $false
    for ($i = 0; $i -lt 30; $i++) {
        if ($tray.HasExited) {
            $out = if (Test-Path $trayOutput) { Get-Content $trayOutput -Raw } else { "" }
            $err = if (Test-Path $trayError) { Get-Content $trayError -Raw } else { "" }
            throw "tray exited before ready: stdout=$out stderr=$err"
        }
        if (Test-Path $readyFile) {
            $ready = $true
            break
        }
        Start-Sleep -Milliseconds 500
    }
    if (-not $ready) {
        throw "tray did not become ready"
    }

    $output = & $go run "$repoRoot\agent\cmd\agent" run --local-only -- fake-ai-cli
    if ($LASTEXITCODE -ne 0) {
        throw "agent local-only via tray failed: $output"
    }
    $text = ($output -join "`n")
    if ($text -notmatch "tray.decision_received" -or $text -notmatch "local_ui.tray_decision") {
        throw "local-only run did not use tray decision: $text"
    }
    if ($text -notmatch "received_decision: approve") {
        throw "fake CLI did not receive approve from tray: $text"
    }
    $history = & $go run "$repoRoot\agent\cmd\agent" history | ConvertFrom-Json
    $session = $history.data.items | Select-Object -First 1
    if (-not $session -or $session.status -ne "completed" -or $session.pending_approval_count -ne 0) {
        throw "local history did not record completed session: $($history | ConvertTo-Json -Compress)"
    }
    $filteredHistory = & $go run "$repoRoot\agent\cmd\agent" history --cli-type custom --status completed --limit 1 | ConvertFrom-Json
    if ($filteredHistory.data.items.Count -ne 1 -or $filteredHistory.data.items[0].session_id -ne $session.session_id) {
        throw "local history filter did not return completed custom session: $($filteredHistory | ConvertTo-Json -Compress)"
    }
    $detail = & $go run "$repoRoot\agent\cmd\agent" history --session-id $session.session_id | ConvertFrom-Json
    if ($detail.data.approvals.Count -lt 1 -or $detail.data.decisions.Count -lt 1 -or $detail.data.output.Count -lt 1) {
        throw "local history detail missing output, approval, or decision: $($detail | ConvertTo-Json -Compress)"
    }

    [pscustomobject]@{
        mode = "tray_local"
        decision = "approve"
        session_id = $session.session_id
        tray_addr = $env:GATEPILOT_AGENT_TRAY_ADDR
        completed = $true
    } | ConvertTo-Json
} finally {
    if ($tray -and -not $tray.HasExited) {
        Stop-Process -Id $tray.Id -Force -ErrorAction SilentlyContinue
    }
    $listener = Get-NetTCPConnection -LocalAddress 127.0.0.1 -LocalPort $trayPort -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($listener) {
        Stop-Process -Id $listener.OwningProcess -Force -ErrorAction SilentlyContinue
    }
    Remove-Item -LiteralPath $readyFile -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $settingsFile -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $historyFile -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $trayOutput -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $trayError -Force -ErrorAction SilentlyContinue
    $env:GATEPILOT_AGENT_POPUP_DECISION = $previousPopupDecision
    $env:GATEPILOT_AGENT_TRAY_ADDR = $previousTrayAddr
    $env:GATEPILOT_AGENT_SETTINGS = $previousSettings
    $env:GATEPILOT_AGENT_HISTORY = $previousHistory
}
