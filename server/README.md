# GatePilot Server

Minimal M0 server skeleton.

```powershell
D:\Dev\Env\Go\bin\go.exe run .\cmd\server
```

Persistence status:

- HTTP handlers now depend on a Store boundary instead of directly mutating maps.
- The default runtime Store is still in-memory for local M1-M4 E2E.
- PostgreSQL baseline migrations live in `server/migrations/`.
- `go run ./cmd/migrate up` applies SQL migrations to `DATABASE_URL` or `GATEPILOT_DATABASE_URL`.
- Set `GATEPILOT_STORE=postgres` and `DATABASE_URL=postgres://...` to run the server against PostgreSQL.
- PostgreSQL Store implementations must preserve the existing handler contract and E2E behavior.

Endpoints:

- `GET /api/v1/healthz`
- `GET /health/live`
- `GET /health/ready`
- `GET /metrics`
- `GET /api/v1/me`
- `POST /api/v1/client-instances`
- `POST /api/v1/tenants/{tenant_id}/device-activation-codes`
- `GET /api/v1/tenants/{tenant_id}/devices`
- `GET /api/v1/devices/{device_id}/grants`
- `POST /api/v1/devices/{device_id}/grants`
- `DELETE /api/v1/devices/{device_id}/grants/{grant_id}`
- `POST /api/v1/agent/register`
- `POST /api/v1/agent/sessions`
- `POST /api/v1/agent/session-updates`
- `GET /api/v1/devices/{device_id}/sessions`
- `GET /api/v1/sessions/{session_id}`
- `GET /api/v1/sessions/{session_id}/output-chunks`
- `POST /api/v1/agent/approvals`
- `POST /api/v1/agent/approval-acks`
- `POST /api/v1/agent/output-chunks`
- `GET /ws/agent`
- `GET /ws/client`
- `GET /api/v1/tenants/{tenant_id}/approvals`
- `POST /api/v1/approvals/{approval_id}/decision`
