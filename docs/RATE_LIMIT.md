# Rate limiting

Token-bucket throttling for the public auth surface and for authenticated
traffic. One limiter serves REST and gRPC on both editions; only the bucket
store differs.

## Stores

`internal/ratelimit.Store` is the single seam (`Take(ctx, key, rule, n)`,
atomic per key, plus `Close`).

| Store | Where | Scope |
|---|---|---|
| `memory` | `internal/ratelimit.Memory` (sharded map, idle eviction, entry cap) | per process; the only choice on the free `nipad` |
| `redis` | `ee/ratelimit.Redis` (go-redis v9, one Lua script using Redis `TIME`) | shared by every `nipad-ee` instance (blue/green colors, pods) |

`RATE_LIMIT_STORE` picks one. On `nipad-ee` an unset value resolves to `redis`
when `RATE_LIMIT_REDIS_ADDR` is set and `memory` otherwise; an explicit
`memory` always wins. `redis` on the free server, `redis` without an address,
an unreachable Redis at startup and unknown values are startup errors.
`RATE_LIMIT_ENABLED=false` disables the limiter entirely.

The limiter **fails open**: if the store errors (Redis down) the request is
allowed, a warning is logged and `nipa_ratelimit_total{result="error"}` is
incremented. The auth scopes (`auth_ip`, `auth_id`, `auth_email`,
`register_email`) are the exception: on a store error they are counted in a
per-process `Memory` store (created on first failure, closed with the
limiter) instead, so a Redis outage degrades them to per-instance limits but
never to no limit. The warning and the `error` metric are still emitted.

## Scopes

| Scope | Applies to | Key | Default (per minute / burst) |
|---|---|---|---|
| `auth_ip` | `/api/v1/auth/*` (login, refresh, register, resend, verify, OAuth) and the public login RPCs | client IP | 10 / 10 |
| `auth_id` | password login, SSH begin/complete login with an email; password change | SHA-256 of the lower-cased email + client IP (email alone when no IP is known); `user:<id>` for password changes | 0.33 / 5 |
| `auth_email` | password login, SSH begin/complete login with an email, after `auth_id` allowed it | SHA-256 of the lower-cased email | 1 / 20 |
| `register_email` | `nipad-ee` SaaS `register` and `register/resend` | SHA-256 of the lower-cased email | 0.1 / 3 |
| `api_user` | authenticated REST (`/api/v1`) and gRPC | user id | 3000 / 200 |
| `chunk_ip` | signed `/api/chunks/*` transfers | client IP | 60000 / 5000 |
| `global_ip` | every other HTTP request | client IP | 6000 / 300 |

Exempt: `/healthz`, `/readyz`, `/metrics`, `/version`, pprof, gRPC health and
`GetServerVersion` (`/metrics` and pprof stay exempt as 404s on the main port
when `METRICS_ADDRESS` moves them; that listener has no limiter). gRPC requests skip the HTTP middleware; the interceptor
applies `auth_ip` to the public login RPCs and `api_user` to the rest.

Configuration lives in `config.yaml` / environment (`RATE_LIMIT_<SCOPE>_PER_MINUTE`
and `_BURST`; see `config.yaml.sample`). Redis settings:
`RATE_LIMIT_REDIS_ADDR`, `_PASSWORD`, `_DB`, `_TLS`, `_PREFIX` (default `nipa:rl:`).

## Client IP

Behind Caddy the peer address is always loopback, so `X-Forwarded-For` is read,
but only when the direct peer is in `RATE_LIMIT_TRUSTED_PROXIES` (default
`127.0.0.1,::1`). The header is walked right to left and the first untrusted
hop is the client, so a client-supplied left-most value cannot spoof the key.
Add the proxy's address (or CIDR) when it is not on loopback.

## Responses

- REST: `429` with `{"error": "rate limit exceeded, retry in Ns"}`,
  `Retry-After` and `X-RateLimit-Limit` / `X-RateLimit-Remaining`.
- gRPC: `ResourceExhausted` with the same message. The client maps it to a 429
  domain error (quota failures keep mapping to 402).
- `auth_id` / `auth_email` rejections surface as the same 429 from the usecase.
- A throttled `register` or `register/resend` answers exactly like an allowed
  one (200, the usual message) and sends nothing, so the limit reveals nothing
  about the address.

## Client behaviour

The `nipa` client treats throttling as backpressure instead of a failure:

- Chunk PUTs and GETs that answer 429 wait for `Retry-After` (capped at 30 s,
  plus jitter) and retry up to 10 times. That budget is separate from the 3
  attempts allowed for network errors, 5xx and 408. Each throttled transfer also
  halves the adaptive transfer concurrency (minimum 4). A pinned
  `NIPA_UPLOAD_WORKERS` is not tuned.
- Unary gRPC calls rejected with `rate limit exceeded, retry in Ns` are repeated
  after the requested wait under the same budget. The server interceptor rejects
  before any handler runs, so this is safe for every method, `Push` included.
  Quota errors (`ResourceExhausted` without the prefix) are not retried.
  Token refresh (`LoginWithRefreshToken`) is retried too, so a long push or
  clone survives an `auth_ip` rejection; password and SSH logins fail fast,
  because their `auth_id` check runs inside the usecase and is a deliberate
  slowdown.

## Metrics

`nipa_ratelimit_total{scope,result}` with `result` in `allowed|limited|error`.
Labels never carry IPs or users.

## Operations notes

- The memory store is per process: two colors during a blue/green flip each keep
  their own buckets, so a client can briefly get up to twice the budget. Use
  Redis to share them.
- Counting is per attempt (failed or not); there is no refund on success.
- `auth_id` is keyed per email and client IP, so one remote IP cannot lock a
  victim out cheaply; `auth_email` is the higher, IP-independent ceiling that
  still bounds a distributed attack on one account (it is only spent once
  `auth_id` allowed the attempt). The usecase reads the client IP from
  `domain.RequestMeta`, attached for the public login RPCs by the JWT
  interceptor and for `/api/v1/auth` by `RequestMetaFilter`.
- Behind a proxy that is not listed in `RATE_LIMIT_TRUSTED_PROXIES` every
  request carries the proxy's address, so email + IP collapses to email-only.
- During a blue/green flip the old color keys `auth_id` on the email alone and
  the new one on email + IP, so the two colors briefly count separately.

## Testing

- Unit: `internal/ratelimit` (fake clock, concurrency), REST filters in
  `internal/http/api`, gRPC interceptor in `internal/grpc/server`.
- Redis: `ee/ratelimit` runs against testcontainers (`redis:7`); override with
  `NIPA_TEST_REDIS_ADDR`.
- Black-box: `tests/suite/ratelimit_test.go` through `tests/run.sh` (the ee run
  uses the Redis service from `tests/docker-compose.ee.yml`).
