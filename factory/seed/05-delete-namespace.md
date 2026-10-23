# Add DELETE /v1/namespaces/{name}

Teams cannot clean up namespaces they no longer need.

## Acceptance criteria

- `DELETE /v1/namespaces/{name}` returns `204` when the namespace existed and was removed.
- It returns `404` with error code `not_found` when the namespace does not exist.
- A deleted namespace no longer appears in `GET /v1/namespaces`.
- The store stays safe under concurrent creates and deletes (a test with goroutines; run it with -race locally).

Area: internal/naas, internal/api
