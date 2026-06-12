# Production Readiness Audit Report — AurelioMod Infrastructure

**Date:** 2026-06-11
**Scope:** Dockerfiles, Compose (dev + prod), Caddy, CI/CD, Schema, Observability, Resilience, KrakenD
**Target:** Fase 1 — Single Hetzner VPS

---

## Overall Score: 62/100

Deducted 38 points across 2 critical blockers and 8 high-priority issues. The core architecture is sound but security hardening, restart resilience, and version pinning need attention before production.

---

## Critical Issues (BLOCKERS)

### C1. Internal service ports exposed to host in production
**File:** `deployments/production/compose.prod.yml`  
**Evidence:** The `ytdlp-sidecar` service is not overridden in `compose.prod.yml`. It retains its dev mapping `ports: - "8081:8080"` from `compose.yml`, exposing the sidecar directly on the VPS host.
- The sidecar wraps yt-dlp CLI → direct host exposure is a sandbox escape risk.
- Also applies to any future service added to `compose.yml` but not explicitly blocked in production.
**Fix:** Add `ytdlp-sidecar` override with `ports: []` and resource limits in `compose.prod.yml`.

### C2. No restart policies on foundational services
**File:** `compose.yml`  
**Evidence:** Only 4 of 14 services have `restart: unless-stopped`: krakend (L167), control (L228), edge-discord (L282), ytdlp-sidecar (L301).  
**Missing:** engine, dragonfly, nats, weaviate, minio, victoriametrics, tempo, grafana, centrifugo, caddy (prod only — already has `restart: unless-stopped`).  
**Impact:** Engine crash = production down until manual intervention. Dragonfly crash = cache cold restart.
**Fix:** Add `restart: unless-stopped` to all services in `compose.yml`. In `compose.prod.yml`, ensure all overrides inherit or add restart policies.

---

## High Priority Issues

### H1. `:latest` tags on infrastructure images
**File:** `compose.yml`  
**Evidence:** Three services use unpinned versions:
- L100: `minio/minio:latest`
- L129: `grafana/tempo:latest`
- L142: `grafana/grafana:latest`
**Impact:** Non-deterministic deploys. A `docker compose pull` can silently break production on upstream image changes.
**Fix:** Pin to explicit versions: `minio/minio:RELEASE.2025-01-20T14-15-55Z`, `grafana/tempo:2.7.1`, `grafana/grafana:11.3.1`.

### H2. `alpine:latest` in Dockerfile.centrifugo and Dockerfile.weaviate
**File:** `deployments/Dockerfile.centrifugo` L1, `deployments/Dockerfile.weaviate` L1  
**Evidence:** Both use `FROM alpine:latest` — non-deterministic base image.
**Impact:** Different builds on different days produce different images. CVEs can appear without code changes.
**Fix:** Pin to `alpine:3.21`.

### H3. Missing health checks on 5 services
**Evidence:**
| Service | Has Health Check? |
|---------|-------------------|
| centrifugo | ❌ |
| victoriametrics | ❌ |
| tempo | ❌ |
| grafana | ❌ |
| minio | ✅ (curl) |
| dragonfly | ✅ (redis-cli) |
| nats | ✅ (wget) |
| weaviate | ✅ (wget) |
| krakend | ✅ (wget) |
| engine | ✅ (Dockerfile wget) |
| control | ✅ (wget) |
| edge-discord | ✅ (Dockerfile wget) |
| ytdlp-sidecar | ✅ (Dockerfile wget) |

**Impact:** Docker cannot detect when these services are unhealthy. Orchestrator cannot restart them automatically.
**Fix:** Add HEALTHCHECK to all. Example for victoriametrics: `wget --spider http://localhost:8428/health`. For tempo: `wget --spider http://localhost:3200/ready`.

### H4. No resource limits on stateful services in production
**File:** `deployments/production/compose.prod.yml`  
**Evidence:** Only engine, control, edge-discord, and krakend have `deploy.resources.limits`. Missing: dragonfly, weaviate, nats, victoriametrics, tempo, minio.
**Impact:** A memory leak in Weaviate or VictoriaMetrics can OOM the entire VPS with no containment.
**Fix:** Add `deploy.resources.limits` to all stateful services. Dragonfly (caching) needs generous limits; Weaviate (vector DB) needs at least 1g.

### H5. Engine and ytdlp-sidecar not using Distroless runtime
**File:** `deployments/Dockerfile.engine` L48, `deployments/Dockerfile.ytdlp-sidecar` L32  
**Evidence:** Both use `jrottenberg/ffmpeg:7.1-ubuntu` as runtime base — a full Ubuntu image with shell, apt, and hundreds of packages.  
- Engine Dockerfile L49 comment acknowledges: _"Distroless deferred to later phase once a static FFmpeg build is available."_
- ytdlp-sidecar needs yt-dlp + FFmpeg, both dynamic binaries.
**Impact:** Larger attack surface. ubuntu:24.04 has ~200+ packages. Distroless has zero.
**Mitigation:** Both images are pinned (`:7.1-ubuntu`) and minimal packages are added. The nsjail sandbox isolates FFmpeg execution. Acceptable for Fase 1 with documented tech debt.
**Recommendation:** Track static FFmpeg build task. Until then, ensure `apt-get clean` in final stage and remove unused packages.

### H6. No circuit breaker for external APIs in Engine
**Evidence:** The edge-discord service has a failsafe-go circuit breaker for ConnectRPC calls to Engine (`edge/discord/client/client.go` L78). However, the Engine itself has no circuit breaker for:
- WaveSpeed API calls (external AI service)
- Weaviate queries (external vector DB — can timeout)
- ytdlp-sidecar calls (external HTTP — can hang)
**Impact:** A WaveSpeed outage will cause cascading request pileup in Engine instead of fast-failing with QUEUED decisions.
**Fix:** Add failsafe-go circuit breakers for WaveSpeed and ytdlp-sidecar calls. Weaviate timeout configured at client level.

### H7. Control API: missing OCI labels and depends_on
**File:** `deployments/Dockerfile.control`  
**Evidence:**
- No `org.opencontainers.image.*` labels (engine and edge-discord have them).
- In `compose.yml`, control has no `depends_on` block — it starts before its dependencies.
**Fix:** Add OCI labels matching engine/edge-discord pattern. Add `depends_on` with at minimum the database (Neon is external, but any local dependencies should be listed).

### H8. KrakenD rate limiting only on GET /v1/workspaces
**File:** `deployments/krakend.json` L70-L80  
**Evidence:** Only the `GET /v1/workspaces` endpoint has `qos/ratelimit/router` configured (100 req/s global, 20 req/s per IP). All other endpoints — including POST login, POST consume, POST webhook — have no rate limiting.
**Impact:** Brute-force on `/v1/auth/login`, abuse of `/v1/workspaces/{id}/consume`.
**Fix:** Add rate limiting to POST /v1/auth/login (10 req/min per IP), POST /v1/webhooks/stripe (unlimited — Stripe controls rate), and at least a global default rate limit.

---

## Medium Priority Issues

### M1. deploy.sh health check uses wrong port
**File:** `deployments/production/deploy.sh` L23  
**Evidence:** `curl -sf https://localhost:8080/healthz` — KrakenD listens on 8080 inside Docker, but in production `ports: []` blocks the mapping. The health check endpoint is only reachable via Caddy on 443.
**Fix:** Change to `curl -sfk https://localhost/healthz` or `curl -sf http://localhost/healthz` (Caddy serves HTTP on 80 with redirect, but direct health check should use HTTPS or internal Docker networking).

### M2. Caddy HEALTHCHECK uses config validation, not HTTP probe
**File:** `deployments/production/compose.prod.yml` L20-L23  
**Evidence:** `test: ["CMD", "caddy", "validate", "--config", "/etc/caddy/Caddyfile"]` — validates Caddyfile syntax, not that the server is serving traffic.
**Fix:** Add `wget --spider http://localhost:2019/metrics` or use `curl -f http://localhost/health`.

### M3. deploy.sh has no rollback capability
**File:** `deployments/production/deploy.sh`  
**Evidence:** No mechanism to revert to previous image version or compose configuration. If a bad deploy goes through, manual SSH intervention is required.
**Fix:** Tag images with git SHA before deploy. Store previous compose checksum. Add `--rollback` flag that restores previous state.

### M4. No backup strategy
**Evidence:** No backup script, no cron job, no S3 backup for:
- Weaviate vector data
- Dragonfly cache (optional — cache can be rebuilt)
- VictoriaMetrics time-series data
**Impact:** Data loss on disk failure.
**Fix:** Add backup script for Weaviate (snapshot API) and VictoriaMetrics (vmbackup). Schedule via cron.

### M5. Centrifugo: hardcoded dev secrets + no volume
**File:** `deployments/centrifugo.json`, `compose.yml`  
**Evidence:**
- `hmac_secret_key: "dev-secret-change-in-production"` — L3
- `admin.password: "dev-admin-change-in-production"` — L10
- No persistent volume for Centrifugo state (token revocation, history).
**Impact:** In production with dev secrets, WebSocket connections are trivially hijackable. State lost on restart.
**Fix:** Use `${CENTRIFUGO_HMAC_KEY}` env var. Add volume mount. Enable TLS in production.

### M6. Missing index on workspaces.api_key
**File:** `deployments/schema/002_workspaces.sql`  
**Evidence:** `api_key TEXT NOT NULL UNIQUE` creates a unique constraint (which implies an index in PostgreSQL), but in Neon/PostgreSQL the behavior is implementation-dependent. The UNIQUE constraint should be sufficient but an explicit index is safer for query patterns where api_key is used for lookup during auth.
**Verdict:** UNIQUE constraint generates an implicit index in PostgreSQL. Acceptable. No action needed.

### M7. CI/CD only builds engine binary
**File:** `.woodpecker.yml` L56-L61  
**Evidence:** Build stage only verifies `go build ./cmd/engine`. Does not build: control, edge-discord, ytdlp-sidecar.
**Impact:** A compilation error in control or edge-discord will not be caught until Docker build time.
**Fix:** Add all `./cmd/*` binaries to the build stage.

### M8. ytdlp-sidecar port mismatch in compose.yml vs code default
**Evidence:** `compose.yml` exposes ytdlp-sidecar on host port 8081 → container 8080. The code default in `cmd/ytdlp-sidecar/main.go` L32 is port 8080 (overridden by YTDLP_SIDECAR_PORT). This is consistent but the port mapping (8081:8080) is a potential confusion point.

### M9. Grafana admin credentials hardcoded
**File:** `compose.yml` L145-L147  
**Evidence:** `GF_SECURITY_ADMIN_USER: admin`, `GF_SECURITY_ADMIN_PASSWORD: admin`.
**Fix:** Use `${GRAFANA_ADMIN_USER}` and `${GRAFANA_ADMIN_PASSWORD}` env vars.

---

## Low Priority / Recommendations

### L1. Dockerfile.control HEALTHCHECK uses custom flag instead of HTTP
**File:** `deployments/Dockerfile.control` L24-L25  
**Evidence:** `CMD ["/control", "-healthcheck"]` — invokes the binary with a flag. Works for Distroless (no wget needed) but is non-standard. Consider adding wget like edge-discord for consistency.

### L2. engine Dockerfile: USER directive missing
**File:** `deployments/Dockerfile.engine`  
**Evidence:** No `USER nonroot` directive. The ffmpeg-based runtime image runs as root. This is acceptable for Fase 1 given the nsjail sandbox, but should be fixed when migrating to Distroless.

### L3. ytdlp-sidecar Dockerfile: USER directive missing
**File:** `deployments/Dockerfile.ytdlp-sidecar`  
**Evidence:** Same as engine — Ubuntu runtime runs as root.
**Fix:** Add non-root user when migrating to Distroless.

### L4. Caddy HTTP→HTTPS redirect not explicit
**File:** `deployments/production/Caddyfile`  
**Evidence:** No explicit `redir` directive for HTTP→HTTPS. Caddy defaults to auto-redirect when TLS is configured, and since port 80 is exposed, this works. However, an explicit redirect is more readable and portable.
**Recommendation:** Add explicit `redir https://{host}{uri}` to port 80 handling (or document that Caddy auto-redirect is relied upon).

### L5. setup.sh creates .env with placeholder values — ok for provisioning
**File:** `deployments/production/setup.sh` L73-L102  
**Evidence:** The script creates a `.env` file with commented-out production values. This is good for the provisioning step. No issue.

### L6. deploy.sh health check: `|| echo "FAIL"` can mask errors
**File:** `deployments/production/deploy.sh` L27  
**Evidence:** `curl -sf https://localhost:8080/healthz 2>/dev/null || curl -sf http://localhost:8080/healthz 2>/dev/null || echo "FAIL"` — if the first curl fails silently, the second is attempted, but the output is assigned to HEALTH which can show confusing output.
**Fix:** Separate the checks and use explicit error messages.

### L7. VictoriaMetrics alert rules reference metrics that may not exist yet
**File:** `deployments/vmalert.yml`  
**Evidence:** Alerts reference `up{job="engine"}`, `workspace_analysis_remaining`, `neon_db_errors_total`. These depend on:
- Prometheus scraping config (not defined in repo — needs `prometheus.yml` or VM scrape config)
- `workspace_analysis_remaining` metric (not confirmed exported by control service)
- `neon_db_errors_total` (not confirmed exported)
**Fix:** Verify all referenced metrics exist in the codebase. Add a Prometheus scrape configuration file.

### L8. centrifugo.json uses `allowed_origins: ["*"]` and `insecure: true`
**File:** `deployments/centrifugo.json`  
**Evidence:** Cross-origin wide open. Insecure mode enabled (no TLS check). Acceptable for dev; must be restricted for production.
**Fix:** Set `allowed_origins` to the production domain. Set `insecure: false` and configure TLS.

---

## Dockerfile Matrix

| Service | Multi-stage | Distroless | Pinned Base | CGO=0 | `-trimpath -s -w` | HEALTHCHECK | Non-root | OCI Labels | Layer Cache |
|---------|------------|------------|-------------|-------|-------------------|-------------|----------|------------|-------------|
| engine | ✅ | ❌ (ffmpeg ubuntu) | ✅ jrottenberg/ffmpeg:7.1-ubuntu | ✅ | ✅ | ✅ wget | ❌ | ✅ | ✅ |
| control | ✅ | ✅ distroless/static-debian12:nonroot | ✅ | ✅ | ✅ | ✅ binary flag | ✅ nonroot | ❌ | ✅ |
| edge-discord | ✅ | ✅ distroless/static-debian12:nonroot | ✅ | ✅ | ✅ | ✅ wget | ✅ nonroot | ✅ | ✅ |
| centrifugo | ❌ single | ❌ alpine:latest | ❌ **alpine:latest** | N/A | N/A | ❌ | ❌ | ❌ | N/A |
| weaviate | ❌ single | ❌ alpine:latest | ❌ **alpine:latest** | N/A | N/A | ❌ | ❌ | ❌ | N/A |
| ytdlp-sidecar | ✅ | ❌ (ffmpeg ubuntu) | ✅ jrottenberg/ffmpeg:7.1-ubuntu | ✅ | ✅ | ✅ wget | ❌ | ✅ | ✅ |

**Summary:** 3/6 services use multi-stage builds. 2/6 use Distroless. 2/6 have unpinned `alpine:latest` bases.

---

## Service Exposure Matrix

| Service | Dev Port (host:container) | Prod Port | Risk |
|---------|--------------------------|-----------|------|
| caddy | — | **80, 443** | ✅ Only public entry point |
| krakend | 8080:8080 | `[]` | ✅ Blocked |
| engine | 9090:8080 | `[]` | ✅ Blocked |
| control | 8082:8080 | `[]` | ✅ Blocked |
| edge-discord | — | `[]` | ✅ Blocked |
| **ytdlp-sidecar** | 8081:8080 | **8081:8080** | 🔴 **EXPOSED** — not blocked in production override |
| dragonfly | 6380:6379 | `[]` | ✅ Blocked |
| nats | 4222:4222, 8222:8222 | `[]` | ✅ Blocked |
| weaviate | 8090:8080, 50051:50051 | `[]` | ✅ Blocked |
| centrifugo | 8000:8000 | `[]` | ✅ Blocked |
| minio | 9000:9000, 9001:9001 | `[]` | ✅ Blocked |
| victoriametrics | 8428:8428 | `[]` | ✅ Blocked |
| tempo | 3200:3200, 4317:4317, 4318:4318 | `[]` | ✅ Blocked |
| grafana | 3000:3000 | `[]` | ✅ Blocked |

---

## Checklist

| # | Item | Status | Evidence |
|---|------|--------|----------|
| **Dockerfiles** | | | |
| 1a | Multi-stage builds | ⚠️ 3/6 | engine, control, edge-discord, ytdlp-sidecar ✅; centrifugo, weaviate ❌ |
| 1b | Pinned base images | ⚠️ 4/6 | centrifugo/weaviate use `alpine:latest` |
| 1c | Distroless runtime | ⚠️ 2/6 | control + edge-discord ✅; engine + ytdlp deferred |
| 1d | CGO_ENABLED=0 | ✅ | All Go Dockerfiles set CGO_ENABLED=0 |
| 1e | `-trimpath -s -w` | ✅ | All Go Dockerfiles use correct ldflags |
| 1f | HEALTHCHECK | ⚠️ 4/6 | engine, control, edge-discord, ytdlp ✅; centrifugo, weaviate ❌ |
| 1g | Non-root USER | ⚠️ 2/6 | control + edge-discord ✅; engine, ytdlp, centrifugo, weaviate ❌ |
| 1h | ca-certificates only | ⚠️ | engine installs extra protobuf libs for nsjail (justified) |
| 1i | OCI labels | ⚠️ 3/6 | engine, edge-discord, ytdlp ✅; control, centrifugo, weaviate ❌ |
| 1j | Layer caching (go mod before COPY) | ✅ | All Go Dockerfiles ✅ |
| 1k | FFmpeg version pinned | ✅ | jrottenberg/ffmpeg:7.1-ubuntu ✅ |
| 1l | nsjail built and included | ✅ | Built from source in engine Dockerfile ✅ |
| 1m | wget for HEALTHCHECK | ✅ | engine, edge-discord, ytdlp ✅; control uses binary flag |
| **Compose Dev** | | | |
| 2a | All infra services | ⚠️ | All present ✅; missing: none documented |
| 2b | Health checks | ⚠️ 8/14 | Missing: centrifugo, victoriametrics, tempo, grafana |
| 2c | depends_on service_healthy | ⚠️ | engine ✅; krakend ✅; edge-discord ✅; centrifugo ✅; control ❌; ytdlp ❌ |
| 2d | Restart unless-stopped | ❌ 4/14 | Only krakend, control, edge-discord, ytdlp |
| 2e | Volume persistence | ✅ | All stateful services have volumes |
| 2f | Port mapping | ⚠️ | ytdlp-sidecar 8081 exposed unnecessarily |
| 2g | Environment variables | ✅ | All mapped via `${VAR:-default}` |
| 2h | Tag versions pinned | ❌ 3/14 | minio, tempo, grafana use `:latest` |
| **Compose Prod** | | | |
| 3a | Public ports blocked | ❌ | ytdlp-sidecar port 8081 not blocked |
| 3b | Resource limits | ⚠️ 4/11 | Missing on dragonfly, nats, weaviate, minio, vmetrics, tempo, grafana |
| 3c | Caddy only public ports | ⚠️ | Caddy + ytdlp-sidecar exposed |
| 3d | Extends compose.yml | ✅ | Proper override via `-f` merge |
| **Caddy** | | | |
| 4a | Auto Let's Encrypt | ✅ | Caddy default behavior |
| 4b | HTTP→HTTPS redirect | ✅ | Caddy auto-redirect with port 80 exposed |
| 4c | HSTS max-age=63072000 | ✅ | Explicit header set |
| 4d | Security headers | ✅ | X-Content-Type-Options, X-Frame-Options |
| 4e | Rate limiting | ✅ | 200 req/s burst 50 |
| 4f | Health check exempt | ✅ | Separate `/healthz` block |
| 4g | Real IP headers | ✅ | X-Real-IP, X-Forwarded-For, X-Forwarded-Proto |
| 4h | Admin API disabled | ✅ | `admin off` |
| 4i | Email set | ✅ | aurelio@aureliomod.com |
| **CI/CD** | | | |
| 5a | Stages: lint, test, security, sbom, build, deploy | ✅ | All 6 stages present |
| 5b | Race detector | ✅ | `go test -race` |
| 5c | govulncheck | ✅ | Security stage |
| 5d | syft SBOM | ✅ | SPDX JSON output |
| 5e | Deploy only main | ✅ | `when: branch: main` |
| 5f | Secrets handling | ✅ | Woodpecker secrets (prod_vps_ip, prod_ssh_key) |
| 5g | Docker TLS for deploy | ✅ | DOCKER_TLS_VERIFY, DOCKER_CERT_PATH |
| 5h | Build all cmd/ binaries | ❌ | Only `./cmd/engine` verified |
| **Database** | | | |
| 6a | Numbered .sql files | ✅ | 001–004 sequential |
| 6b | audit_log columns | ✅ | UUID PK, audit_id UNIQUE, all required fields |
| 6c | workspaces fields | ✅ | id, name, api_key, plan, created_at, updated_at |
| 6d | Stripe integration | ✅ | customer_id, subscription_id |
| 6e | Billing counters | ✅ | monthly_analysis_count, monthly_analysis_limit |
| 6f | Indexes | ✅ | idx_audit_workspace_time; UNIQUE on api_key (implicit index) |
| **Observability** | | | |
| 7a | VictoriaMetrics retention | ✅ | `--retentionPeriod=30d` |
| 7b | Tempo OTLP endpoints | ✅ | gRPC 4317, HTTP 4318 |
| 7c | Grafana dashboards | ✅ | Provisioned via volume mount |
| 7d | Engine OTLP metrics | ✅ | Traces + metrics via gRPC to Tempo |
| 7e | Trace context propagation | ✅ | W3C TraceContext + Baggage propagators |
| 7f | JSON structured logs | ✅ | slog JSON handlers in all services |
| 7g | pprof enabled + protected | ✅ | Port 6060, PASETO auth, gated by PPROF_ADMIN_KEY |
| **Resilience** | | | |
| 8a | Restart policies | ❌ 10/14 missing | Only 4 services have `restart: unless-stopped` |
| 8b | Health checks | ⚠️ 9/14 | 5 services missing health checks |
| 8c | depends_on health | ⚠️ 4/14 | engine, krakend, edge-discord, centrifugo ✅ |
| 8d | Circuit breaker ext APIs | ⚠️ | edge-discord ✅; engine ❌ (WaveSpeed, Weaviate) |
| 8e | Graceful shutdown | ✅ | All 4 Go services: signal.NotifyContext + drain |
| 8f | Volume persistence | ✅ | All stateful services |
| **Deployment** | | | |
| 9a | deploy.sh idempotent | ✅ | set -euo pipefail |
| 9b | Health check after deploy | ⚠️ | Basic ps check ✅; curl check uses wrong port |
| 9c | Rollback capability | ❌ | No rollback mechanism |
| 9d | Backup strategy | ❌ | No backup scripts or cron jobs |
| 9e | .env.example template | ⚠️ | setup.sh creates .env; .env.example not accessible for review |
| **KrakenD** | | | |
| 10a | All endpoints | ✅ | Auth, workspaces, billing, webhooks, consume |
| 10b | Rate limiting per endpoint | ❌ | Only GET /v1/workspaces has rate limits |
| 10c | Auth headers forwarded | ✅ | Authorization passthrough on all endpoints |
| 10d | Timeout configured | ✅ | `"timeout": "10s"` |
| 10e | JSON logging | ✅ | `telemetry/logging: format: json` |
| 10f | Stripe-Signature passthrough | ✅ | Explicitly listed in input_headers |

---

## Summary of Required Actions Before Production

### Must Fix (Blockers)
1. Add `ytdlp-sidecar` service override to `compose.prod.yml` with `ports: []` and resource limits.
2. Add `restart: unless-stopped` to all services missing it: engine, dragonfly, nats, weaviate, minio, victoriametrics, tempo, grafana, centrifugo.

### Should Fix (High Priority)
3. Pin `minio/minio:latest`, `grafana/tempo:latest`, `grafana/grafana:latest` to explicit versions.
4. Pin `alpine:latest` to `alpine:3.21` in Dockerfile.centrifugo and Dockerfile.weaviate.
5. Add HEALTHCHECK to centrifugo, victoriametrics, tempo, grafana.
6. Add `deploy.resources.limits` to dragonfly, weaviate, nats, victoriametrics, tempo, minio.
7. Add circuit breaker for WaveSpeed API and ytdlp-sidecar calls in Engine.
8. Add OCI labels and depends_on to control Dockerfile/compose.
9. Add rate limiting to remaining KrakenD endpoints.
10. Add all `./cmd/*` to CI build verification.

### Nice to Have (Medium/Low)
11. Fix deploy.sh health check port.
12. Add Caddy HTTP health check (not just config validate).
13. Implement rollback capability.
14. Create backup scripts for Weaviate and VictoriaMetrics.
15. Replace Centrifugo dev secrets with env vars.
16. Use env vars for Grafana admin credentials.
17. Add explicit HTTP→HTTPS redirect in Caddyfile.
