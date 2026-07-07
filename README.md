# API Gateway — consultprompts.com

The single entry point for all client traffic across the consultprompts.com platform.
Sits in front of all microservices, handling JWT verification, rate limiting, request
routing, and trusted header injection. Has no database, no business logic, and no
direct knowledge of users, products, or courses.

---

## Architecture

```
[Client: browser / mobile app]
        ↓ all requests hit one URL
[API Gateway :8080]  ← this service
        ↓ strips any incoming X-User-ID / X-User-Roles (anti-spoofing)
        ↓ verifies JWT, re-sets X-User-ID / X-User-Roles headers
        ↓ routes by URL path
[Internal microservices]
    auth-service      :8081
    agency-service    :8082
    products-service  :8083  (not yet built)
    academy-service   :8084  (not yet built)
    orders-service    :8085  (not yet built)
```

The Gateway is the **only** publicly reachable service. All internal services are
on a private network, unreachable directly from outside in production.

---

## What the Gateway does

- **JWT verification** — verifies RS256-signed access tokens using auth-service's
  public key, fetched from `http://auth-service:8081/.well-known/jwks.json` on
  startup and refreshed every hour
- **Trusted header injection** — strips any incoming `X-User-ID` / `X-User-Roles`
  headers (prevents spoofing), then sets them from the verified JWT payload
  before forwarding downstream
- **Reverse proxy** — forwards requests to the correct internal service based on
  URL path, streams the response back to the client
- **CORS** — `Access-Control-Allow-Origin` locked to `FRONTEND_URL`, credentials allowed
- **Global rate limiting** — 10 requests/second per IP, burst of 20
- **Structured JSON logging** — every request logged with method, path, status, IP

## What the Gateway does NOT do

- No database
- No business logic
- No JWT signing (only verification)
- No direct knowledge of users, products, or courses
- No session management

---

## Tech Stack

- Go + Gin (HTTP routing)
- `golang-jwt/jwt/v5` — RS256 token verification
- `httputil.ReverseProxy` — standard library reverse proxy
- `golang.org/x/time/rate` — in-memory rate limiting
- Docker + Docker Compose

---

## Project Structure

```
api-gateway/
  main.go                      # entry point, routing, dependency wiring
  internal/
    middleware/
      auth.go                  # JWT verification, trusted header injection
      cors.go                  # CORS headers, locked to FRONTEND_URL
      rate_limit.go            # global IP-based rate limiting
    proxy/
      proxy.go                 # reverse proxy factory
  pkg/
    jwks/
      jwks.go                  # JWKS fetcher, public key cache, hourly refresh
    logger/
      logger.go                # structured JSON slog setup
  .env                         # local secrets (gitignored)
  .env.example                 # template for required environment variables
  Dockerfile
  docker-compose.yml
```

---

## Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| PORT | Port the Gateway listens on | 8080 |
| AUTH_SERVICE_URL | Internal URL of auth-service | http://auth-service:8081 |
| AGENCY_SERVICE_URL | Internal URL of agency-service | http://agency-service:8082 |
| FRONTEND_URL | Allowed CORS origin for browser clients | http://localhost:3000/ |
| DB_PASSWORD | auth-service Postgres password (used by docker-compose only) | yourpassword |
| AGENCY_DB_PASSWORD | agency-service Postgres password (used by docker-compose only) | yourpassword |

---

## Routes

### Public (no JWT required)

| Method | Path | Forwards to |
|--------|------|-------------|
| GET | /healthz | Gateway (local) |
| POST | /auth/register | auth-service |
| POST | /auth/login | auth-service |
| POST | /auth/refresh | auth-service |
| POST | /auth/logout | auth-service |
| POST | /auth/verify-email | auth-service |
| POST | /auth/verify-email/resend | auth-service |
| POST | /auth/password/reset-request | auth-service |
| POST | /auth/password/reset | auth-service |
| GET | /auth/google/login | auth-service |
| GET | /auth/google/callback | auth-service |
| GET | /.well-known/jwks.json | auth-service |

### Protected (JWT required)

| Method | Path | Forwards to |
|--------|------|-------------|
| GET | /auth/me | auth-service |
| POST | /auth/roles/assign | auth-service |
| POST | /auth/roles/remove | auth-service |
| GET | /auth/users/:id | auth-service |
| POST | /agency/leads | agency-service |
| GET | /agency/leads/mine | agency-service |
| GET | /agency/leads | agency-service |
| PATCH | /agency/leads/:id/milestone | agency-service |
| PATCH | /agency/leads/:id/mockup | agency-service |
| PATCH | /agency/leads/:id/complete | agency-service |
| POST | /agency/leads/:id/review | agency-service |
| PATCH | /agency/leads/:id/maintenance | agency-service |
| POST | /agency/leads/:id/pay | agency-service |
| PATCH | /agency/leads/:id/launch | agency-service |

Protected routes verify the JWT at the Gateway level. Downstream services trust
the `X-User-ID` and `X-User-Roles` headers set by the Gateway — they never
re-verify the JWT themselves. Role-gating for admin-only agency routes
(e.g. `GET /agency/leads`) happens inside agency-service itself, not at the Gateway.

---

## Setup (Local Development)

**Prerequisites**: Go 1.26+, `air`, auth-service running on `:8081`

**1. Create `.env` from template:**
```bash
cp .env.example .env
# fill in DB_PASSWORD and other values
```

**2. Run with live reload:**
```bash
air
```

The Gateway fetches auth-service's public key on startup — make sure auth-service
is running on `:8081` before starting the Gateway, or it will fail to start.

---

## Running with Docker

The `docker-compose.yml` in this repo runs the **entire stack**:
- `postgres` (database for auth-service)
- `agency-postgres` (separate database for agency-service)
- `auth-service` (built from `../auth-service`)
- `agency-service` (built from `../agency-service`, no host port — reachable only via the Gateway)
- `api-gateway` (this service)

Both Postgres containers build from `../postgres-jsonlog`, a thin wrapper around
the stock `postgres` image that tails Postgres's JSON log file to stdout so
`docker compose logs` shows structured query/connection logs.

**Prerequisites**: all repos cloned side by side in the same parent folder:
```
consultprompts/
  auth-service/
  agency-service/
  postgres-jsonlog/
  api-gateway/     ← run docker compose from here
```

**Start the full stack:**
```bash
docker compose up --build
```

**Stop (keeps database data):**
```bash
docker compose down
```

**Full reset (wipes database):**
```bash
docker compose down -v
```

> **Note**: never run `auth-service/docker-compose.yml` and `api-gateway/docker-compose.yml`
> at the same time — they both bind port 5433 for Postgres, which causes a conflict.
> Use `api-gateway/docker-compose.yml` as the master stack going forward.

---

## Multi-database Plan (as more services are added)

Each microservice owns its own database — `auth-service` and `agency-service`
already run on fully separate Postgres instances (`postgres` / `agency-postgres`
in `docker-compose.yml`), each with its own credentials in its own `.env`.
The longer-term plan is to move to a single root-level `.env` file containing
credentials for all services:

```
consultprompts/
  .env                  ← single source of truth
  auth-service/
  api-gateway/
  agency-service/
  academy-service/
  products-service/
  orders-service/
```

This avoids duplicating credentials across multiple `.env` files and makes rotating
passwords a one-file change.

---

## Adding a New Microservice

When a new service is built (e.g. `academy-service` on `:8084`):

**1. Add the service URL to `.env`:**
```
ACADEMY_SERVICE_URL=http://academy-service:8084
```

**2. Add route groups in `main.go`:**
```go
academyURL := os.Getenv("ACADEMY_SERVICE_URL")

// public academy routes
router.GET("/academy/courses", proxy.NewReverseProxy(academyURL))

// protected academy routes
authorized.GET("/academy/my-courses", proxy.NewReverseProxy(academyURL))
```

**3. Add the service to `docker-compose.yml`:**
```yaml
academy-service:
  build:
    context: ../academy-service
    dockerfile: Dockerfile
  ports:
    - "8084:8084"
  env_file:
    - ../academy-service/.env
  depends_on:
    postgres:
      condition: service_healthy
```

No changes needed to JWT verification or middleware — the Gateway's auth layer
applies automatically to any route registered under the `authorized` group.

---

## TODO

### v2.0
- [ ] Redis-backed rate limiting (multi-instance support)
- [ ] Request ID header (`X-Request-ID`) for distributed tracing
- [ ] Circuit breaker (stop forwarding to a repeatedly failing service)

### Routes to add as services are built
- [x] `/agency/*` → agency-service :8082
- [ ] `/products/*` → products-service :8083
- [ ] `/academy/*` → academy-service :8084
- [ ] `/orders/*` → orders-service :8085
