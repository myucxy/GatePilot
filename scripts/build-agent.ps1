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

function Resolve-Wails {
    if ($env:GATEPILOT_WAILS -and (Test-Path $env:GATEPILOT_WAILS)) {
        return $env:GATEPILOT_WAILS
    }

    $defaultWails = "D:\Dev\Env\Go\bin\wails.exe"
    if (Test-Path $defaultWails) {
        return $defaultWails
    }

    $pathWails = Get-Command wails -ErrorAction SilentlyContinue
    if ($pathWails) {
        return $pathWails.Source
    }

    throw "wails executable not found. Install it with: `$env:GOBIN='D:\Dev\Env\Go\bin'; D:\Dev\Env\Go\bin\go.exe install github.com/wailsapp/wails/v2/cmd/wails@latest"
}

$go = Resolve-Go
$wails = Resolve-Wails
if (Test-Path $distRoot) {
    Remove-Item -Path (Join-Path $distRoot "*") -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $distRoot | Out-Null
& $go build -trimpath -ldflags "-s -w" -o (Join-Path $distRoot "gatepilot-agent.exe") "$repoRoot\agent\cmd\agent"
if ($LASTEXITCODE -ne 0) {
    throw "agent build failed"
}
Copy-Item -LiteralPath (Join-Path $distRoot "gatepilot-agent.exe") -Destination (Join-Path $distRoot "gp.exe") -Force
Push-Location (Join-Path $repoRoot "agent\desktop")
try {
    $env:PATH = "D:\Dev\Env\nvm4w\nodejs;D:\Dev\Env\Go\bin;$env:PATH"
    & $wails build
    if ($LASTEXITCODE -ne 0) {
        throw "desktop build failed"
    }
}
finally {
    Pop-Location
}
Copy-Item -LiteralPath (Join-Path $repoRoot "agent\desktop\build\bin\gatepilot-agent-desktop.exe") -Destination (Join-Path $distRoot "gatepilot-agent-desktop.exe") -Force

$readme = @'
# GatePilot Agent Windows AMD64

桌面客户端：

```powershell
.\gatepilot-agent-desktop.exe
```

桌面客户端会自动启动或连接 `gatepilot-agent.exe tray`。日常配置、登录、离线模式、提醒开关、AI 工具历史来源和会话历史都在桌面客户端里完成，不需要打开网页。

双击 `gatepilot-agent.exe` 会启动托盘 Agent，并打开桌面客户端设置页。托盘菜单里的设置、登录和会话历史也都会打开 `gatepilot-agent-desktop.exe`，不会再弹出浏览器网页。

本地托管 Codex/Claude：

```powershell
.\gp.exe codex
.\gp.exe claude
.\gatepilot-agent.exe codex
.\gatepilot-agent.exe claude
.\gatepilot-agent.exe install-gp
```

运行 `install-gp` 后，新打开的终端可以使用：

```powershell
gp codex
gp claude
```

离线本地确认：

```powershell
.\gatepilot-agent.exe
.\gatepilot-agent.exe tray
.\gatepilot-agent.exe run --local-only -- fake-ai-cli
.\gatepilot-agent.exe run --local-only --decision approve -- fake-ai-cli
.\gatepilot-agent.exe run --local-only --popup -- fake-ai-cli
.\gatepilot-agent.exe run --local-only --cli-type codex -- codex
.\gatepilot-agent.exe run --local-only --cli-type claude_code -- claude
.\gatepilot-agent.exe status
.\gatepilot-agent.exe settings --notification-enabled true --notification-style mini_window
.\gatepilot-agent.exe settings --start-on-login true
.\gatepilot-agent.exe open-settings
.\gatepilot-agent.exe open-history
.\gatepilot-agent.exe history
.\gatepilot-agent.exe history --cli-type codex --status running --limit 20
.\gatepilot-agent.exe reply --session-id <session_id> --text "continue"
.\gatepilot-agent.exe login --server-url <url> --tenant-id <tenant_id> --device-id <device_id>
.\gatepilot-agent.exe offline
.\gatepilot-agent.exe logout
```

服务端联动模式：

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
    gp = Join-Path $distRoot "gp.exe"
    desktop = Join-Path $distRoot "gatepilot-agent-desktop.exe"
    package = $packagePath
} | ConvertTo-Json
