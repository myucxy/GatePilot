# GataPilot客户端

Wails + React + TypeScript desktop client for GatePilot.

The desktop client provides the local control API directly, then manages settings, login state, local history, approvals, and running-session replies.

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
