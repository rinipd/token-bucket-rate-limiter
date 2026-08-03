# Distributed deployment (Redis-backed, load-balanced)

This spins up three identical app instances, all in `redis` backend mode
sharing one Redis, behind an nginx load balancer — to prove multiple
instances enforce **one shared** rate limit, not one limit per instance.

## Build and run

```
docker compose -f deploy/docker-compose.yml up --build
```

This builds the app image once (from `deploy/Dockerfile`, with build context
at the repo root) and starts:

- `redis` — the shared rate-limit state
- `app1`, `app2`, `app3` — three instances, each `BACKEND=redis` pointed at
  the same `redis:6379`, none reachable directly from the host
- `nginx` — listening on host port `8080`, round-robining across the three
  app instances (see `deploy/nginx.conf`)

## Configure a client

Configure the client **once**, through nginx. Because all three instances
share the same Redis, whichever instance happens to handle this particular
request writes the config to Redis, and all three instances see it on their
very next lookup — there's no need to configure each instance separately:

```
curl -X PUT http://localhost:8080/admin/clients/loadtest \
  -d '{"RPS":1,"Burst":100,"Algorithm":"token_bucket"}'
```

## Run the distributed correctness test

Point the existing `loadtest` tool at nginx (port 8080) exactly as you would
against a single instance — it doesn't need to know it's hitting a load
balancer fronting three separate backends:

```
go run ./loadtest -url http://localhost:8080 -key loadtest \
  -mode correctness -n 5000 -c 50 -expect 100
```

With a shared Redis-backed limit, this should report (close to) exactly 100
ALLOWed, even though nginx spread the 5000 requests round-robin across three
separate processes. A naive per-process in-memory limiter under the same
setup would instead let roughly 300 through (up to ~100 per instance, since
each process would enforce its own independent burst) — that gap is exactly
what this deployment is built to demonstrate.

## Tear down

```
docker compose -f deploy/docker-compose.yml down
```
