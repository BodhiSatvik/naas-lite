# Add liveness and readiness endpoints and wire Kubernetes probes

naas-lite has no health endpoints, so Kubernetes cannot tell a hung pod from a healthy one. Rollouts go green even when the server does not serve.

## What we need

- `GET /healthz` returns `200` with `{"status":"ok"}` while the process is up.
- `GET /readyz` returns `200` with `{"status":"ready"}` when the server can serve requests, and `503` with `{"status":"not ready"}` while it shuts down.
- `deploy/k8s/deployment.yaml` gets a `livenessProbe` on `/healthz` and a `readinessProbe` on `/readyz` (httpGet on the `http` port).

## Acceptance criteria

- Handler tests cover `200` for both endpoints and `503` for `/readyz` during shutdown.
- Both probes appear in the Deployment with sensible timings (period 5-10s, failure threshold 3).
- Health checks are not logged at info level, so they do not flood the logs.

Area: internal/api, cmd/naas-lite, deploy/k8s
