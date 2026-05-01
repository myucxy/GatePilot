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
$output = & $go run "$repoRoot\agent\cmd\agent" run --local-only --decision approve -- fake-ai-cli
if ($LASTEXITCODE -ne 0) {
    throw "agent local-only run failed: $output"
}
$text = ($output -join "`n")
if ($text -notmatch "local_ui.approval_notification") {
    throw "local-only run did not print local notification: $text"
}
if ($text -notmatch "local_only.decision_written") {
    throw "local-only run did not write local decision: $text"
}
if ($text -notmatch "local_only.completed") {
    throw "local-only run did not complete: $text"
}
if ($text -notmatch "received_decision: approve") {
    throw "fake CLI did not receive approve: $text"
}

[pscustomobject]@{
    mode = "local_only"
    decision = "approve"
    notification = $true
    decision_written = $true
    completed = $true
} | ConvertTo-Json
