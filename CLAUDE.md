# naas-lite: agent guide

naas-lite is a small Namespace-as-a-Service API. Teams request a Kubernetes namespace with a CPU and memory quota, and the service returns the ResourceQuota it would apply. The repo also hosts the AI software factory that builds it.

## Code map

- `cmd/naas-lite`: main. HTTP server, JSON logs (log/slog), graceful shutdown.
- `internal/api`: HTTP handlers on Go 1.22 `net/http` route patterns. JSON in, JSON out.
- `internal/naas`: the domain. Request validation, the in-memory store, ResourceQuota rendering.
- `deploy/k8s`: Deployment and Service. Keep them in sync with ports, endpoints and probes.
- `specs/<issue>/SPEC.md`: approved specs, written by the spec station.
- `factory/`: the factory controller. Stations must not edit it.

## Conventions

- Standard library only. Do not add dependencies.
- Errors: use `writeError(w, status, code, message)` in `internal/api`. The body is `{"error":{"code":"...","message":"..."}}`.
- Domain rules live in `internal/naas`, never in HTTP handlers.
- Tests: table-driven, in the same package, `httptest` for handlers. Every acceptance criterion gets a test.
- Run `make verify` before you finish: gofmt, go vet, go test, and a manifest dry-run when the local `kind-factory` cluster exists.
- Commits: conventional commits that reference the issue, for example `feat(api): add readiness endpoint (#41)`.
- Kubernetes: probes use `httpGet` on the named port `http`. Keep resource requests and limits, and keep `runAsNonRoot`.

## Team memory (ai-memory)

- Before you plan, search memory (`memory_query`) for decisions and gotchas about the area you touch.
- Treat memory as hints. Check each hint against the current code.
- Record only durable, non-obvious lessons, under `gotchas/` or `decisions/`.

## Factory stations

The factory runs one station per headless Claude Code session: `/factory-triage`, `/factory-spec`, `/factory-build`, `/factory-review` and `/factory-ship`. Verify is plain `make verify`. Inputs are in `.factory/in/`. Outputs go to `.factory/out/`. Both folders are git-ignored.

Never push, never call `gh`, never use `kubectl`, and never edit `factory/`, `.claude/` or the `Makefile`. The controller does every GitHub side effect, and `.claude/settings.json` denies these actions.
