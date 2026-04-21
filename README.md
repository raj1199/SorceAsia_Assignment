# Rate-Limited API Service

A production-considerate HTTP service written in Go that enforces per-user rate limits.

## Quick Start

```bash
# Run directly
go run .

# Or build then run
make build && ./bin/server

# Run with a custom port
PORT=9000 go run .
```

## Run Tests

```bash
make test          # runs all tests with -race detector
go test -v ./...   # verbose output
```

## API Reference

### POST /request

Accepts a JSON body and processes it subject to per-user rate limiting.

**Request**
```json
{ "user_id": "alice", "payload": "any value" }
```

**201 Created** – request accepted
```json
{
  "message": "request accepted",
  "user_id": "alice",
  "remaining_requests": 4,
  "timestamp": "2024-01-01T12:00:00Z"
}
```

**429 Too Many Requests** – limit exceeded (includes `Retry-After: 60` header)
```json
{
  "error": "rate limit exceeded: max 5 requests per minute",
  "retry_after_seconds": 60
}
```

**400 Bad Request** – missing or invalid body

---

### GET /stats

Returns per-user request statistics.

- `GET /stats` — all users
- `GET /stats?user_id=alice` — single user

**200 OK**
```json
{
  "users": [
    {
      "user_id": "alice",
      "total_requests": 6,
      "allowed_requests": 5,
      "rejected_requests": 1,
      "last_request_at": "2024-01-01T12:00:05Z"
    }
  ]
}
```

---

### GET /health

Returns `{"status": "ok"}` — useful for load-balancer probes.

---

## Docker

```bash
make docker-build
make docker-run
```

---

## Design Decisions

### Rate Limiting: Sliding Window Log

Each user maintains a slice of request timestamps. On every call `Allow()`:

1. Acquires the per-user mutex (never a global lock).
2. Prunes timestamps older than the 60-second window in-place (O(n), zero alloc).
3. Checks count against the limit atomically — the check and the write happen inside the same lock hold, so there is no TOCTOU race.
4. Records the new timestamp if allowed.

**Why sliding window over fixed window?**  
A fixed window allows a burst of 2× the limit at the boundary (last request of window N + first of window N+1). The sliding window eliminates that by always looking at the last 60 seconds regardless of clock alignment.

**Why sliding window over token bucket?**  
Token bucket allows short bursts beyond the steady-state rate. A strict "5 requests per 60 seconds" requirement is better served by a sliding window that enforces an exact count over the exact rolling interval.

### Concurrency Model

- **Per-user mutex** (`userBucket.mu`): goroutines for different users never contend with each other.
- **Top-level RWMutex** (`SlidingWindowLimiter.mu`): only contended on first encounter of a new user ID; uses double-checked locking to avoid write-lock overhead for existing users.
- **`store.Store`** uses a single `sync.RWMutex`: reads (stats queries) never block each other; writes (recording a request outcome) are brief.
- The `-race` test flag is used in CI to catch any data races.

### Graceful Shutdown

The server listens for `SIGINT`/`SIGTERM` and calls `http.Server.Shutdown` with a 30-second context, allowing in-flight requests to complete before the process exits.

---

## Limitations & What I'd Improve with More Time

| Area | Current | With More Time |
|---|---|---|
| **Persistence** | In-memory; data lost on restart | Redis with `ZADD`/`ZCOUNT` for distributed sliding windows |
| **Multi-instance** | Each replica has independent rate-limit state | Centralise in Redis; use Lua scripts for atomic check-and-increment |
| **Memory growth** | User buckets accumulate forever | Background goroutine evicts buckets idle for >N minutes |
| **Observability** | Structured JSON logs | Prometheus metrics (`/metrics`): request count, reject rate, latency histograms |
| **Auth** | None | API key or JWT middleware; tie rate limit to authenticated identity |
| **Retry / queuing** | Client receives 429 and must retry manually | Optional request queue per user with exponential back-off and webhooks |
| **Configuration** | `PORT` env var only | Viper config: limit, window, log level, timeouts all configurable |
| **Deployment** | Dockerfile only | Helm chart + GitHub Actions CI/CD to AKS (Azure Kubernetes Service) |
| **Tests** | Unit + integration | Load test with `k6`; chaos test with random delays to verify window accuracy |
