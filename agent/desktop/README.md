# GatePilot Agent Desktop

Wails + React + TypeScript desktop shell for GatePilot Agent.

The desktop app starts or connects to the local `gatepilot-agent.exe tray` process, then manages settings, login state, local history, and running-session replies through the local control API.

## Development

```powershell
cd agent\desktop
D:\Dev\Env\Go\bin\wails.exe dev
```

## Build

Use the repository packaging script so the desktop shell and core agent executable are placed together:

```powershell
.\scripts\build-agent.ps1
```
