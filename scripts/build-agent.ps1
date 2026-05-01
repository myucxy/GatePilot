$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path "$PSScriptRoot\.."
$distRoot = Join-Path $repoRoot "dist\gatepilot-agent-windows-amd64"
$packagePath = Join-Path $repoRoot "dist\gatepilot-agent-windows-amd64.zip"

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
if (Test-Path $distRoot) {
    Remove-Item -Path (Join-Path $distRoot "*") -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $distRoot | Out-Null
& $go build -trimpath -ldflags "-s -w" -o (Join-Path $distRoot "gatepilot-agent.exe") "$repoRoot\agent\cmd\agent"
if ($LASTEXITCODE -ne 0) {
    throw "agent build failed"
}

$readme = @'
# GatePilot Agent Windows AMD64

Offline local confirmation:

```powershell
.\gatepilot-agent.exe tray
.\gatepilot-agent.exe run --local-only -- fake-ai-cli
.\gatepilot-agent.exe run --local-only --decision approve -- fake-ai-cli
.\gatepilot-agent.exe run --local-only --popup -- fake-ai-cli
.\gatepilot-agent.exe status
.\gatepilot-agent.exe settings --notification-enabled true --notification-style mini_window
.\gatepilot-agent.exe settings --start-on-login true
.\gatepilot-agent.exe history
.\gatepilot-agent.exe history --cli-type codex --status running --limit 20
.\gatepilot-agent.exe reply --session-id <session_id> --text "continue"
.\gatepilot-agent.exe login --server-url <url> --tenant-id <tenant_id> --device-id <device_id>
.\gatepilot-agent.exe offline
.\gatepilot-agent.exe logout
```

Server-backed local UI:

```powershell
.\gatepilot-agent.exe register --activation-code <code>
.\gatepilot-agent.exe local-ui --tenant-id <tenant_id> --device-id <device_id>
.\gatepilot-agent.exe run -- fake-ai-cli
```
'@
$readme | Set-Content -Path (Join-Path $distRoot "README.md") -Encoding UTF8

if (Test-Path $packagePath) {
    Remove-Item -LiteralPath $packagePath -Force
}
Compress-Archive -Path (Join-Path $distRoot "*") -DestinationPath $packagePath

[pscustomobject]@{
    executable = Join-Path $distRoot "gatepilot-agent.exe"
    package = $packagePath
} | ConvertTo-Json
