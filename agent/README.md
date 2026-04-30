# GatePilot Agent

Minimal M0 Agent skeleton.

Built-in adapter coverage now includes `custom`, `codex`, `claude_code`, `opencode`, `copilot`, and `gemini` prompt detection plus decision input mapping tests.

```powershell
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent version
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent register --activation-code <code>
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent connect --device-id <device_id> --once
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent create-session --device-id <device_id>
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent detect-approval --device-id <device_id> --session-id <session_id>
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent ack-decision --approval-id <approval_id> --delivery-id <delivery_id> --session-id <session_id>
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent run -- fake-ai-cli
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent run-fake
```
