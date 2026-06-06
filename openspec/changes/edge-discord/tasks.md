# Tasks: Edge Discord Bot Adapter

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | 650-750 (~430 code + ~300 tests) |
| 400-line budget risk | Medium |
| Chained PRs recommended | Yes |
| Suggested split | PR 1: Foundation (ratelimit, audit, client, listener, Dockerfile, go.mod) → PR 2: Behavior + Wiring (commands, handler, main, compose) |
| Delivery strategy | ask-on-risk |
| Chain strategy | feature-branch-chain |

Decision needed before apply: Yes
Chained PRs recommended: Yes
Chain strategy: feature-branch-chain
400-line budget risk: Medium

### Suggested Work Units

| Unit | Goal | Likely PR | Notes |
|------|------|-----------|-------|
| 1 | deps + ratelimit + audit + client + listener + Dockerfile | PR 1 | Base: `feature/edge-discord`; ~373 lines incl. tests |
| 2 | commands + handler + main + compose | PR 2 | Base: PR 1 branch; ~340 lines incl. tests |

## Phase 1: Foundation (deps + core packages) — PR 1

- [x] 1.1 Update `go.mod`: add `github.com/disgoorg/disgo` (latest), promote `golang.org/x/time` to direct; run `go mod tidy`
- [x] 1.2 Create `edge/discord/ratelimit/ratelimit.go`: `Limiter` interface + `tokenBucket` (45 req/s, burst 10, 2s queue via `x/time/rate`); log `event=rate_limit_drop` on overflow
- [x] 1.3 Create `edge/discord/audit/audit.go`: `Emitter` interface + `AuditEvent` struct + `SlogEmitter` writing JSON lines to stdout with required fields per spec §discord-moderation
- [x] 1.4 Create `edge/discord/client/client.go`: `AnalysisClient` interface + ConnectRPC wrapper with circuit breaker (5 failures/60s, 30s open); log `event=circuit_breaker_open` on BLOCK state
- [x] 1.5 Create `edge/discord/listener/listener.go`: `MessageCreate` handler filtering attachments/URLs (skip plain text, skip >10MB with warning); `GuildCreate` handler registering slash commands
- [x] 1.6 Create `deployments/Dockerfile.edge-discord`: multi-stage (golang:1.26-alpine → distroless/static), CGO_ENABLED=0, wget HEALTHCHECK against `/healthz`

## Phase 2: Core behavior + wiring — PR 2

- [x] 2.1 Create `edge/discord/commands/commands.go`: register `/moderate <url>`, `/status`, `/config workspace_id <id> enforce <on|off>`; `/moderate` calls Analyze + replies with decision; `/status` ephemeral with uptime, engine health, breaker state, tokens
- [x] 2.2 Create `edge/discord/handler/handler.go`: `EnforceDecision(ctx, msg, decision)` — BLOCK→delete ≤500ms + DM author with reason, ALLOW→noop, QUEUED→default BLOCK `block_reason=pending_analysis`; gate via `ENFORCE_MODERATION` env (default true)
- [x] 2.3 Create `cmd/edge-discord/main.go`: load env (DISCORD_TOKEN, ENGINE_URL, REQUIRED_GUILD_ID), slog JSON, signal.NotifyContext, wire all packages, disgo start, drain in-flight RPCs on SIGTERM, exit 0 ≤10s
- [x] 2.4 Modify `compose.yml`: add `edge-discord` service with build context, DISCORD_TOKEN/ENGINE_URL env vars, depends_on engine

## Phase 3: Testing — both PRs

- [x] 3.1 Test `ratelimit`: table-driven token exhaustion + queue overflow → `rate_limit_drop` log (spec §discord-moderation scenarios 1-2)
- [x] 3.2 Test `audit`: SlogEmitter emits valid JSON with all required fields on BLOCK event
- [x] 3.3 Test `client`: circuit breaker open/close transitions via `synctest` + mock failing RPC (spec §discord-bot scenarios 1-2)
- [x] 3.4 Test `listener`: plain text skipped, >10MB skipped with warning, image ≤10MB triggers Analyze (spec §discord-bot scenarios 1-3)
- [x] 3.5 Test `handler`: mock disgo REST; verify delete+DM for BLOCK, noop for ALLOW, fallback for QUEUED, gate off skips (spec §discord-moderation scenarios 1-3)
- [x] 3.6 Test `commands`: mock interaction responses for /moderate, /status, /config; verify ephemeral /status fields (spec §discord-commands scenarios 1-2)
- [x] 3.7 Test `main`: integration smoke — bot starts, connects, accepts SIGTERM, drains RPCs before disgo.Close (spec §discord-bot scenario 2)
