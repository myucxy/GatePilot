# GatePilot Web Admin

```powershell
npm install
npm run dev
```

The Vite dev server proxies `/api` and `/ws` to `http://127.0.0.1:8080`.

The M1-M4 console registers a Web client instance, connects to `/ws/client`, refreshes approvals on `approval.created` and `approval.updated`, creates device activation codes, shows device sessions, and submits approval decisions.
