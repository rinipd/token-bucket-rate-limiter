# Rate Limiter Dashboard

A minimal Next.js (App Router) dashboard that polls the Go server's
`GET /stats` endpoint once a second and shows live per-client allow/deny
rates. This is a separate npm project from the Go module — it has its own
`package.json` and isn't part of the Go build or the `deploy/` container
setup.

## Run it

```
cd dashboard
npm install
npm run dev
```

Then open http://localhost:3000. Make sure the Go server (any backend) is
running and reachable; by default the dashboard polls
`http://localhost:8080/stats`.

To point it at a different address, copy `.env.local.example` to
`.env.local` and set `NEXT_PUBLIC_API_URL`.
