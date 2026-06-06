# Design: Edge Service — Discord Bot Adapter

## Technical Approach

Single-binary bot using `disgo` for Discord Gateway + ConnectRPC client for Engine `Analyze` RPC. Intercepts `MessageCreate` events with attachments/URLs, forwards content to Engine, enforces BLOCK decisions via message deletion + DM. Follows Engine patterns: env-var config, slog JSON logging, `signal.NotifyContext` graceful shutdown, multi-stage Dockerfile.

## Architecture Decisions

| Decision | Option | Tradeoff | Chosen |
|----------|--------|----------|--------|
| Circuit breaker | **Reuse `internal/circuitbreaker/`** (failsafe-go) | Already in go.mod, tested. Thresholds: 5 failures/60s, 30s open, half-open probe every 60s. | ✅ |
| | Custom goroutine breaker | More control, more code, untested | |
| Rate limiter | **`golang.org/x/time/rate`** token bucket | Already indirect dep in go.mod. 45 req/s, burst 10, queue 2s deadline. | ✅ |
| | DragonflyDB distributed limiter | Overkill for single-shard MVP | |
| Attachment download | **`http.Client` GET Discord CDN URL** | In-memory only, no disk I/O. CDN URLs expire fast — must download synchronously before analysis. | ✅ |
| | Proxy through Engine | Extra hop, Engine doesn't parse CDN URLs | |
| Audit emitter | **Simplified `SlogEmitter` (stdout only)** | Matches Engine's `AuditEmitter` interface shape but no Neon/R2 (edge has no DB config). Spec §discord-moderation requires same emitter pattern. | ✅ |
| | Full `MultiEmitter` reuse | Unnecessary deps (Neon, R2, S3) for edge service | |
| Package granularity | **6 packages as proposed** (client, listener, commands, handler, ratelimit, audit) | Clear separation. Client/ratelimit reusable by future adapters. Each ≤200 lines. | ✅ |
| | Monolithic `edge/discord/` | Smaller but Telegram/Twitch can't reuse | |
| Docker base | **`distroless/static`** | CGO_ENABLED=0 binary. Minimal attack surface. Follows Engine Dockerfile pattern. | ✅ |
| | Alpine | Larger, more CVEs | |

## Data Flow

```
Discord Gateway → MessageCreate event
       │
       ▼
  listener.go: filter (attachments/URLs)
       │
       ▼
  client.go: download CDN attachment (http GET)
       │
       ▼
  ratelimit.go: token bucket check (45 req/s)
       │
       ▼
  client.go: ContentAnalysisServiceClient.Analyze(ctx, req)
       │
       ▼
  handler.go: Decision enforcement
       ├─ BLOCK  → Delete message + DM author + audit log
       ├─ ALLOW  → Noop
       └─ QUEUED → Default BLOCK (pending_analysis)
```

Circuit breaker wraps the Analyze RPC call. Opened state → all messages BLOCKED with audit alert.

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `cmd/edge-discord/main.go` | Create | Bootstrap, config load, signal handling, disgo start |
| `edge/discord/client/client.go` | Create | ConnectRPC Analyze client wrapper with circuit breaker |
| `edge/discord/listener/listener.go` | Create | disgo MessageCreate + GuildCreate handlers |
| `edge/discord/commands/commands.go` | Create | Slash command registration + /moderate /status /config handlers |
| `edge/discord/handler/handler.go` | Create | Decision enforcement: delete, DM, audit |
| `edge/discord/ratelimit/ratelimit.go` | Create | Token bucket rate limiter (golang.org/x/time/rate) |
| `edge/discord/audit/audit.go` | Create | Simplified audit emitter (matches Engine emitter interface) |
| `deployments/Dockerfile.edge-discord` | Create | Multi-stage Dockerfile (golang:1.26-alpine → distroless/static) |
| `compose.yml` | Modify | Add edge-discord service entry |
| `go.mod` | Modify | Add `github.com/disgoorg/disgo`, promote `golang.org/x/time` to direct |

## Interfaces / Contracts

```go
// edge/discord/client/client.go
type AnalysisClient interface {
    Analyze(ctx context.Context, req *v1.AnalyzeRequest) (*v1.AnalyzeResponse, error)
}

// edge/discord/audit/audit.go
type Emitter interface {
    Emit(ctx context.Context, event AuditEvent) error
}
// AuditEvent struct mirrors engine/audit.AuditEvent with discord fields:
//   guild_id, author_id, source_platform="discord"

// edge/discord/ratelimit/ratelimit.go
type Limiter interface {
    Allow(ctx context.Context) bool  // true if token acquired
    Wait(ctx context.Context) error  // blocks until token or ctx deadline
}
```

`AnalysisClient` wraps `aureliomodv1connect.ContentAnalysisServiceClient` + circuit breaker. Constructor: `NewClient(engineURL string) *Client`.

## Testing Strategy

| Layer | What to Test | Approach |
|-------|-------------|----------|
| Unit | Rate limiter token exhaustion | Table-driven, `t.Context()`, `synctest` for time |
| Unit | Decision enforcement (BLOCK/ALLOW/QUEUED) | Mock `disgo` REST, verify delete+DM calls |
| Unit | Slash command handlers | Mock interaction responses |
| Integration | ConnectRPC client against real Engine | Requires Engine running; skip in CI without `ENGINE_URL` |
| Integration | Circuit breaker open/close transitions | `synctest` + mock failing client |

## Migration / Rollout

No migration required. Bot is stateless. Rollback: `docker compose stop edge-discord`.

## Open Questions

- [ ] Can `disgo` download CDN attachments directly, or must we use raw `http.Client` with CDN URLs extracted from `Message.Attachments`?
- [ ] Should `DISCORD_TOKEN` stay in `.env` (current) or move to Docker secrets for production?
- [ ] `/config` command — where is workspace toggle persisted? Memory-only or DragonflyDB? Spec says "workspace ID, toggle" but no persistence target defined.
