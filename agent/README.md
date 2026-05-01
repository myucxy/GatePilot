# GatePilot Agent

Minimal M0 Agent skeleton.

Built-in adapter coverage now includes `custom`, `codex`, `claude_code`, `opencode`, `copilot`, and `gemini` prompt detection plus decision input mapping tests.

```powershell
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent version
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent register --activation-code <code>
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent connect --device-id <device_id> --once
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent local-ui --tenant-id <tenant_id> --device-id <device_id>
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent local-ui --tenant-id <tenant_id> --device-id <device_id> --decision approve --once
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent run --local-only --decision approve -- fake-ai-cli
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent run --local-only --popup -- fake-ai-cli
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent tray
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent status
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent settings --notification-enabled true --notification-style mini_window
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent settings --start-on-login true
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent open-settings
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent open-history
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent history
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent history --cli-type codex --status running --limit 20
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent reply --session-id <session_id> --text "continue"
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent login --server-url <url> --tenant-id <tenant_id> --device-id <device_id>
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent offline
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent logout
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent create-session --device-id <device_id>
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent detect-approval --device-id <device_id> --session-id <session_id>
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent ack-decision --approval-id <approval_id> --delivery-id <delivery_id> --session-id <session_id>
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent flush-queue
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent run -- fake-ai-cli
D:\Dev\Env\Go\bin\go.exe run .\cmd\agent run-fake
```

`local-ui` registers an `agent_desktop` client instance, prints a local approval notification, and submits the user's approve/reject/reply decision through the normal server decision API. The managed `run` command still performs the device-side delivery ACK after writing the decision back to the CLI.

`run --local-only` does not require registration or a server. It detects a local CLI approval prompt, prints a local notification, asks for approve/reject/reply unless `--decision` is provided, and writes the decision directly back to the CLI. On Windows, add `--popup` to show a native Yes/No confirmation dialog; Yes maps to approve and No maps to reject.

Running the agent with no arguments starts the Windows tray process and opens the settings page. `tray` starts the same tray control process without opening a page. When it is running, `run --local-only` sends detected approvals to the tray first; the tray applies the local notification settings and returns approve/reject/reply to the session host.

`settings` reads or updates local desktop settings. `--start-on-login true` registers the packaged executable under the current user's Windows Run key; set it to `false` to remove that entry.

`open-settings` and `open-history` open the tray-hosted settings and history pages. Start the agent or `tray` first.

`history` reads the local offline session history. Use `history --session-id <session_id>` for output chunks, approvals, and decisions for one session. Use `--cli-type`, `--status`, and `--limit` to filter the session list.

`reply` sends text to a still-running local session that was started by `run --local-only`; ended sessions reject replies.

`login` stores desktop online settings after registering an `agent_desktop` client instance. `offline` keeps the login identity but switches back to local-only mode. `logout` clears the desktop login identity and leaves the agent offline.

Queued approval detections are stored in `%APPDATA%\GatePilot\approval-queue.jsonl` by default. Set `GATEPILOT_AGENT_QUEUE` to override the file path for tests or isolated runs.
