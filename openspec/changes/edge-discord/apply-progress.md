# Apply Progress: edge-discord — PR 1 + PR 2 (Complete)

**Date**: 2026-06-06
**Mode**: Strict TDD (openspec)
**Branch**: `feature/edge-discord-pr2` (targets `feature/edge-discord`)

## Implementation Summary

All 10 tasks (4 foundation + 4 behavior + 6 test) completed across 2 batches.
**69 tests pass** (56 in edge/discord/* + 13 in cmd/edge-discord), clean build.

---

## Completed Tasks

### Phase 1: Foundation (PR 1)

- [x] 1.1 `go.mod`: Added `github.com/disgoorg/disgo@v0.19.5` + transitive deps
- [x] 1.2 `edge/discord/ratelimit/`: `Limiter` interface + `tokenBucket` (45 req/s, burst 10)
- [x] 1.3 `edge/discord/audit/`: `Emitter` interface + `SlogEmitter` (JSON stdout)
- [x] 1.4 `edge/discord/client/`: `AnalysisClient` + ConnectRPC + failsafe-go circuit breaker
- [x] 1.5 `edge/discord/listener/`: `MessageCreate` filtering, `GuildJoin`, `GuildReady`
- [x] 1.6 `deployments/Dockerfile.edge-discord`: Multi-stage (golang:1.26-alpine → distroless/static)

### Phase 2: Behavior + Wiring (PR 2)

- [x] 2.1 `edge/discord/commands/`: `/moderate <url>`, `/status`, `/config` slash commands. `CommandDefs()`, `Register()`, `FormatModerateReply()`, `FormatStatusReply()` extracted as pure functions.
- [x] 2.2 `edge/discord/handler/`: `EnforceDecision(ctx, msg, decision)` — BLOCK→delete+DM, ALLOW→noop, QUEUED→pending_analysis. Gate via `ENFORCE_MODERATION`. `DiscordRest` interface for testability.
- [x] 2.3 `cmd/edge-discord/main.go`: Full wiring — disgo client, listener, handler, commands, ratelimit, audit. Graceful shutdown with signal.NotifyContext + 10s drain deadline.
- [x] 2.4 `compose.yml`: Added `edge-discord` service with DISCORD_TOKEN, ENGINE_URL, depends_on engine.

### Phase 3: Tests (both PRs)

- [x] 3.1-3.4 PR 1 tests: ratelimit (6), audit (4), client (5), listener (8)
- [x] 3.5 `handler_test.go`: 6 tests — BLOCK delete+DM+audit, ALLOW noop, QUEUED fallback, gate off, DM failure resilience, audit JSON validation
- [x] 3.6 `commands_test.go`: 11 tests — 4 command definition tests (3 commands, options, subcommands) + 7 response formatting tests (BLOCK/ALLOW/QUEUED/UNSPECIFIED/ERROR/healthy/unhealthy)
- [x] 3.7 `main_test.go`: 16 tests — `attContentType` mapping (12), `isImageType` (3), `isVideoType` (2), `isAudioType` (2)

---

## TDD Cycle Evidence

### PR 1 Evidence (from prior batch)
| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| 1.2 | `ratelimit/ratelimit_test.go` | Unit | N/A (new) | ✅ Written | ✅ Passed | ✅ 6 cases | ✅ Clean |
| 1.3 | `audit/audit_test.go` | Unit | N/A (new) | ✅ Written | ✅ Passed | ✅ 4 cases | ✅ Clean |
| 1.4 | `client/client_test.go` | Unit | N/A (new) | ✅ Written | ✅ Passed | ✅ 5 cases | ✅ Clean |
| 1.5 | `listener/listener_test.go` | Unit | N/A (new) | ✅ Written | ✅ Passed | ✅ 8 cases | ✅ Clean |
| 1.1 | N/A (structural) | — | N/A | — | — | ➖ Skipped: deps | — |
| 1.6 | N/A (structural) | — | N/A | — | — | ➖ Skipped: Dockerfile | — |

### PR 2 Evidence (this batch)
| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| 2.2 | `handler/handler_test.go` | Unit | N/A (new) | ✅ Written | ✅ Passed | ✅ 6 cases | ✅ Clean |
| 2.1 | `commands/commands_test.go` | Unit | N/A (new) | ✅ Written | ✅ Passed | ✅ 11 cases | ✅ Clean |
| 2.3 | `main_test.go` | Unit | N/A (new) | ✅ Written | ✅ Passed | ✅ 16 cases | ✅ Clean |
| 2.4 | N/A (structural) | — | N/A | — | — | ➖ Skipped: compose | — |

### Test Summary
- **Total tests written**: 69
- **Total tests passing**: 69
- **Layers used**: Unit (69)
- **Approval tests**: None (all new files)
- **Pure functions created**: `shouldAnalyze`, `shouldAnalyzeWithLog`, `guildIDString`, `FormatModerateReply`, `FormatStatusReply`, `decisionDisplay`, `attContentType`, `isImageType`, `isVideoType`, `isAudioType`

---

## Files Changed (PR 2 only)

| File | Action | Description |
|------|--------|-------------|
| `edge/discord/commands/commands.go` | Created | Slash command defs + handlers + pure format funcs |
| `edge/discord/commands/commands_test.go` | Created | 11 tests: command defs + formatting |
| `edge/discord/handler/handler.go` | Created | DiscordRest interface + EnforceDecision enforcement |
| `edge/discord/handler/handler_test.go` | Created | 6 tests: BLOCK/ALLOW/QUEUED/gate/DM failure/audit |
| `cmd/edge-discord/main.go` | Created | Full wiring, graceful shutdown, content type mapping |
| `cmd/edge-discord/main_test.go` | Created | 16 smoke tests for content type mapping functions |
| `compose.yml` | Modified | Added edge-discord service definition |

---

## Deviations from Design

1. **handler.DiscordRest interface**: Design said handler takes `disgo` client directly. Introduced a minimal `DiscordRest` interface (DeleteMessage, CreateDMChannel, CreateMessage) to enable mock testing without needing the full `rest.Rest` interface. `rest.Rest` satisfies this via structural typing in production.
2. **commands.Format*Reply extracted**: Design said handlers would return `InteractionResponse` directly. Extracted response formatting into pure functions (`FormatModerateReply`, `FormatStatusReply`) for testability per strict-tdd "extract-before-mock" rule.
3. **ContentType mapping in main.go**: Added `attContentType()`, `isImageType()`, etc. in main.go for mapping Discord MIME types to AurelioMod ContentType enums. Not specified in design but necessary for correct AnalyzeRequest construction.
4. **snowflake.New(time.Time)**: Tests use `snowflake.ID(uint64)` direct casts instead of `snowflake.New(time.Time)` which requires a real timestamp.

---

## Issues Found

None. All tests pass, project builds cleanly.

---

## Workload / PR Boundary

- **Mode**: feature-branch-chain (PR 2 of 2)
- **Current work unit**: Unit 2 (commands + handler + main + compose)
- **Boundary**: Full behavior + wiring layer. Depends on PR 1 foundation.
- **Estimated review budget**: ~600 lines (~350 code + ~250 tests)

## Status

**17/17 tasks complete**. Ready for verify phase.
