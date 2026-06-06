# Proposal: Edge Service — Discord Bot Adapter

## Intent

Implement the first Edge Service bot adapter for AurelioMod: a Discord bot that intercepts messages with attachments/URLs, forwards content to the Engine's ConnectRPC `ContentAnalysisService`, and enforces moderation decisions (BLOCK → delete+notify). This is the first of three Edge adapters (Telegram, Twitch follow).

## Scope

### In Scope
- Discord bot binary (`cmd/edge-discord/main.go`) reading `DISCORD_TOKEN` from `.env`
- `disgo` event listener: message create + guild create (register commands)
- ConnectRPC client calling Engine's `Analyze` RPC with `SOURCE_PLATFORM_DISCORD`
- Decision enforcement: BLOCK → delete message + ephemeral DM to author; ALLOW → noop
- Slash commands: `/moderate <url>`, `/status`, `/config` (workspace ID, toggle)
- Rate limiter: respects Discord's 50 req/s global + per-route
- Structured slog audit logging (JSON to stdout)
- Graceful shutdown (signal handling, `disgo.Close`)

### Out of Scope
- Discord bot sharding (single-shard only for MVP)
- Centrifugo real-time status push (future: NATS subscriber)
- Telegram/Twitch adapters (separate changes)
- Dashboard integration (Control service, future)
- Workspace multi-tenancy beyond env var `WORKSPACE_ID`

## Capabilities

### New Capabilities
- **discord-bot**: Discord gateway connection, attachment/URL interception, ContentAnalysisService client, structured audit logging
- **discord-commands**: Slash command registration and handling (`/moderate`, `/status`, `/config`)
- **discord-moderation**: Decision enforcement (message deletion, user notification, DSA compliance)

### Modified Capabilities
None — no existing specs in `openspec/specs/`.

## Approach

**Single deliverable**, ~6 packages, ~400 lines:

```
cmd/edge-discord/main.go       — bootstrap, signal handling, disgo start
edge/discord/client/client.go  — ConnectRPC ContentAnalysisServiceClient wrapper
edge/discord/listener/listener.go — disgo event handlers (message create + guild create)
edge/discord/commands/commands.go — slash command registration + handlers
edge/discord/handler/handler.go   — decision enforcement (delete, DM, audit)
edge/discord/ratelimit/ratelimit.go — token bucket rate limiter (50 req/s)
```

Follows Engine patterns: feature-gated via env vars (`ENFORCE_MODERATION=true` default), slog structured logging, context propagation, graceful shutdown.

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| `cmd/edge-discord/` | New | Bot binary entry point, config loading, signal handling |
| `edge/discord/client/` | New | ConnectRPC client for Engine `Analyze` RPC |
| `edge/discord/listener/` | New | `disgo` message-create + guild-create handlers |
| `edge/discord/commands/` | New | Slash command definitions + handlers |
| `edge/discord/handler/` | New | Decision enforcement: delete, DM, audit log |
| `edge/discord/ratelimit/` | New | Token-bucket rate limiter |
| `go.mod` | Modified | Add `github.com/disgoorg/disgo` |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| Discord API rate-limit 429s cause message drops | Medium | Token bucket at 45 req/s (headroom); exponential backoff on 429 |
| Engine unreachable → bot silently allows content | High | Circuit breaker: after 5 failures, default BLOCK with audit log; timeout 5s |
| `disgo` library breaking changes | Low | Pin exact version in go.mod; disgo is mature (v0.18+) |
| Large attachments exceed Engine proto size | Low | Skip messages with attachments >10MB; log warning |

## Rollback Plan

- Stop bot binary (`systemctl stop aureliomod-discord` or SIGTERM)
- Remove `cmd/edge-discord/` and `edge/discord/` directories
- Revert `go.mod` (remove `disgoorg/disgo` line)
- Bot is stateless — no database migration needed

## Dependencies

- `github.com/disgoorg/disgo` (new, not in go.mod)
- Engine service running at `ENGINE_URL` (env, default `http://localhost:8080`)
- `DISCORD_TOKEN` in `.env` (existing)

## Success Criteria

- [ ] Bot connects to Discord Gateway and sets presence
- [ ] Image/GIF/video attachments trigger `Analyze` RPC with correct `ContentType`
- [ ] URLs embedded in messages trigger `Analyze` with `EXTERNAL_URL`
- [ ] BLOCK decision deletes message within 500ms and DMs author with `block_reason`
- [ ] ALLOW decision passes through with zero action (no noise)
- [ ] `/moderate <url>` manually submits URL to Engine
- [ ] `/status` returns bot uptime, Engine health, cache stats
- [ ] Rate limiter prevents >45 concurrent requests to Engine
- [ ] All existing Engine tests pass unchanged (no regression)

## Estimated Effort

**Size**: M (Medium — ~400 lines, ~6 packages, 1 deliverable, 3-5 days)
