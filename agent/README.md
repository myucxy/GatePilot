# GatePilot Agent

Minimal M0 Agent skeleton.

Built-in adapter coverage now includes `custom`, `codex`, `claude_code`, `opencode`, `copilot`, and `gemini` prompt detection plus decision input mapping tests.

```powershell
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent version
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent register --activation-code <code>
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent connect --device-id <device_id> --once
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent local-ui --tenant-id <tenant_id> --device-id <device_id>
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent local-ui --tenant-id <tenant_id> --device-id <device_id> --decision approve --once
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent run --local-only --decision approve -- fake-ai-cli
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent create-session --device-id <device_id>
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent detect-approval --device-id <device_id> --session-id <session_id>
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent ack-decision --approval-id <approval_id> --delivery-id <delivery_id> --session-id <session_id>
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent flush-queue
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent run -- fake-ai-cli
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent run-fake
```

`local-ui` registers an `agent_desktop` client instance, prints a local approval notification, and submits the user's approve/reject/reply decision through the normal server decision API. The managed `run` command still performs the device-side delivery ACK after writing the decision back to the CLI.

`run --local-only` does not require registration or a server. It detects a local CLI approval prompt, prints a local notification, asks for approve/reject/reply unless `--decision` is provided, and writes the decision directly back to the CLI.

Queued approval detections are stored in `%APPDATA%\GatePilot\approval-queue.jsonl` by default. Set `GATEPILOT_AGENT_QUEUE` to override the file path for tests or isolated runs.
