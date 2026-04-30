# GatePilot Server

Minimal M0 server skeleton.

```powershell
D:\Dev\Env\Go\bin\go.exe run .\cmd\server
```

Endpoints:

- `GET /api/v1/healthz`
- `GET /api/v1/me`
- `POST /api/v1/tenants/{tenant_id}/device-activation-codes`
- `GET /api/v1/tenants/{tenant_id}/devices`
- `POST /api/v1/agent/register`
- `POST /api/v1/agent/sessions`
- `GET /api/v1/devices/{device_id}/sessions`
- `POST /api/v1/agent/approvals`
- `GET /api/v1/tenants/{tenant_id}/approvals`
- `POST /api/v1/approvals/{approval_id}/decision`
