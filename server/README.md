# GatePilot Server

Minimal M0 server skeleton.

```powershell
D:\Dev\Env\Go\bin\go.exe run .\cmd\server
```

Persistence status:

- HTTP handlers now depend on a Store boundary instead of directly mutating maps.
- The default runtime Store is still in-memory for local M1-M3 E2E.
- PostgreSQL baseline migrations live in `server/migrations/`.
- Future PostgreSQL Store implementations must preserve the existing handler contract and E2E behavior.

Endpoints:

- `GET /api/v1/healthz`
- `GET /api/v1/me`
- `POST /api/v1/tenants/{tenant_id}/device-activation-codes`
- `GET /api/v1/tenants/{tenant_id}/devices`
- `POST /api/v1/agent/register`
- `POST /api/v1/agent/sessions`
- `GET /api/v1/devices/{device_id}/sessions`
- `POST /api/v1/agent/approvals`
- `POST /api/v1/agent/approval-acks`
- `GET /api/v1/tenants/{tenant_id}/approvals`
- `POST /api/v1/approvals/{approval_id}/decision`
