# Add request IDs and structured access logs

On-call cannot follow one request through the logs. Every request needs an ID, and every request needs one structured access log line.

## Acceptance criteria

- Middleware reads `X-Request-ID` from the request. It generates a new ID when the header is missing or longer than 64 characters.
- Every response carries the `X-Request-ID` header.
- One JSON log line per request (log/slog) with `request_id`, `method`, `path`, `status` and `duration_ms`.
- JSON error bodies include the request ID as `request_id`.
- Tests cover: header passed through, header generated, and the log line fields.

Area: internal/api, cmd/naas-lite
