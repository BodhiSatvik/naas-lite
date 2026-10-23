# Enforce per-tier CPU and memory limits on namespace requests

Teams can request any quota today. A bronze team asked for 64 cores last week and got it. Requests must stay inside their tier.

## Limits

| Tier   | Max CPU (cores) | Max memory (Gi) |
|--------|-----------------|-----------------|
| bronze | 4               | 8               |
| silver | 8               | 32              |
| gold   | 16              | 64              |

## Acceptance criteria

- A request above its tier limit returns `422` with error code `quota_exceeded` and a message that names the resource, the tier and the limit, for example `cpu 12 exceeds the silver tier limit of 8`.
- A request exactly at the limit is accepted (the limit is inclusive).
- The limits live in one place in `internal/naas`, not in the HTTP handler.
- Table-driven tests cover every tier below, at and above each limit.

Area: internal/naas, internal/api
