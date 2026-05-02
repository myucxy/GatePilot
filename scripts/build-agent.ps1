$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path "$PSScriptRoot\.."
$distRoot = Join-Path $repoRoot "dist\gatepilot-client-windows-amd64"
$packagePath = Join-Path $repoRoot "dist\gatepilot-client-windows-amd64.zip"
$legacyDistRoot = Join-Path $repoRoot "dist\gatepilot-agent-windows-amd64"
$legacyPackagePath = Join-Path $repoRoot "dist\gatepilot-agent-windows-amd64.zip"
$clientExeName = "GataPilot" + [string][char]0x5BA2 + [string][char]0x6237 + [string][char]0x7AEF + ".exe"

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

if (Test-Path $legacyDistRoot) {
    Remove-Item -LiteralPath (Join-Path $legacyDistRoot "*") -Recurse -Force -ErrorAction SilentlyContinue
    try {
        Remove-Item -LiteralPath $legacyDistRoot -Recurse -Force -ErrorAction Stop
    }
    catch {
        Write-Warning "Legacy dist directory is locked by another process or terminal: $legacyDistRoot"
    }
}
if (Test-Path $legacyPackagePath) {
    Remove-Item -LiteralPath $legacyPackagePath -Force
}
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
Copy-Item -LiteralPath (Join-Path $repoRoot "agent\desktop\build\bin\$clientExeName") -Destination (Join-Path $distRoot $clientExeName) -Force

$readmeBase64 = "IyBHYXRhUGlsb3TlrqLmiLfnq68gV2luZG93cyBBTUQ2NAoK5pys5YyF5Y+q5YyF5ZCr5qGM6Z2i54mI5ZKMIGdwIOWRveS7pO+8mgoKLSBgR2F0YVBpbG905a6i5oi356uvLmV4ZWDvvJrmoYzpnaLlrqLmiLfnq6/vvIzlkIzml7bmj5DkvpvmnKzlnLDmj5DphpLjgIHnoa7orqTlkozljoblj7LmjqXlj6PjgIIKLSBgZ3AuZXhlYO+8mkFJIENMSSDmiZjnrqHlhaXlj6PvvIznlKjkuo7lkK/liqggQ29kZXgg5oiWIENsYXVkZe+8jOW5tuaKiuehruiupOivt+axguS6pOe7meahjOmdoueJiOWkhOeQhuOAggoK5ZCv5Yqo5qGM6Z2i54mI77yaCgpgYGBwb3dlcnNoZWxsCi5cR2F0YVBpbG905a6i5oi356uvLmV4ZQpgYGAKCuahjOmdoueJiOWQr+WKqOWQjuS8muWcqOacrOacuuaPkOS+myBgMTI3LjAuMC4xOjE4NzMxYCDmnKzlnLDmjqXlj6PjgILml6XluLjphY3nva7jgIHnmbvlvZXjgIHnprvnur/mqKHlvI/jgIHmj5DphpLlvIDlhbPjgIFBSSDlt6Xlhbfljoblj7LmnaXmupDjgIFHUCDlrZDov5vnqIvlkozkvJror53ljoblj7Lpg73lnKjmoYzpnaLlrqLmiLfnq6/ph4zlrozmiJDvvIzkuI3pnIDopoHmiZPlvIDnvZHpobXvvIzkuZ/kuI3lho3pnIDopoHljZXni6znmoQgYGdhdGVwaWxvdC1hZ2VudC5leGVg44CCCgrmiZjnrqEgQ29kZXgvQ2xhdWRl77yaCgpgYGBwb3dlcnNoZWxsCi5cZ3AuZXhlIGNvZGV4Ci5cZ3AuZXhlIGNsYXVkZQpgYGAKCuWmguaenOahjOmdoueJiOayoeaciei/kOihjO+8jGBncC5leGVgIOS8muiHquWKqOWQr+WKqOWQjOebruW9leeahCBgR2F0YVBpbG905a6i5oi356uvLmV4ZWDjgILnoa7orqTor7fmsYLkvJrpgJrov4fmoYzpnaLniYjlvLnnqpflpITnkIbvvIzmnIDnu4jlhpnlm57lvZPliY0gQ0xJ44CCCgrpnIDopoHlhajlsYDkvb/nlKggYGdwYCDml7bvvIzmiorlvZPliY3nm67lvZXliqDlhaXnlKjmiLcgUEFUSO+8jOS5i+WQjuaWsOaJk+W8gOeahOe7iOerr+WPr+S7peS9v+eUqO+8mgoKYGBgcG93ZXJzaGVsbApncCBjb2RleApncCBjbGF1ZGUKYGBg"
$readme = [System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($readmeBase64))
$readme | Set-Content -Path (Join-Path $distRoot "README.md") -Encoding UTF8

if (Test-Path $packagePath) {
    Remove-Item -LiteralPath $packagePath -Force
}
Compress-Archive -Path (Join-Path $distRoot "*") -DestinationPath $packagePath

[pscustomobject]@{
    gp = Join-Path $distRoot "gp.exe"
    desktop = Join-Path $distRoot $clientExeName
    package = $packagePath
} | ConvertTo-Json
