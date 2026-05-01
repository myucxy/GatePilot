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
& $go build -trimpath -ldflags "-s -w" -o (Join-Path $distRoot "gp.exe") "$repoRoot\agent\cmd\gp"
if ($LASTEXITCODE -ne 0) {
    throw "gp build failed"
}
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
# GatePilot Windows AMD64

本包只包含桌面版和 gp 命令：

- `gatepilot-agent-desktop.exe`：桌面客户端，同时提供本地提醒、确认和历史接口。
- `gp.exe`：AI CLI 托管入口，用于启动 Codex 或 Claude，并把确认请求交给桌面版处理。

启动桌面版：

```powershell
.\gatepilot-agent-desktop.exe
```

桌面版启动后会在本机提供 `127.0.0.1:18731` 本地接口。日常配置、登录、离线模式、提醒开关、AI 工具历史来源、GP 子进程和会话历史都在桌面客户端里完成，不需要打开网页，也不再需要单独的 `gatepilot-agent.exe`。

托管 Codex/Claude：

```powershell
.\gp.exe codex
.\gp.exe claude
```

如果桌面版没有运行，`gp.exe` 会自动启动同目录的 `gatepilot-agent-desktop.exe`。确认请求会通过桌面版弹窗处理，最终写回当前 CLI。

需要全局使用 `gp` 时，把当前目录加入用户 PATH，之后新打开的终端可以使用：

```powershell
gp codex
gp claude
```
'@
$readme | Set-Content -Path (Join-Path $distRoot "README.md") -Encoding UTF8

if (Test-Path $packagePath) {
    Remove-Item -LiteralPath $packagePath -Force
}
Compress-Archive -Path (Join-Path $distRoot "*") -DestinationPath $packagePath

[pscustomobject]@{
    gp = Join-Path $distRoot "gp.exe"
    desktop = Join-Path $distRoot "gatepilot-agent-desktop.exe"
    package = $packagePath
} | ConvertTo-Json
