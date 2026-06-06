# Apply Progress: edge-discord — PR 1 / Batch 1

**Date**: 2026-06-06
**Mode**: Strict TDD (openspec)
**Branch**: `feature/edge-discord`

## Implementation Summary

All 6 foundation tasks + 4 paired test tasks completed. 23 tests pass, clean build.

---

## Completed Tasks

### Phase 1: Foundation

- [x] 1.1 `go.mod`: Added `github.com/disgoorg/disgo@v0.19.5` + transitive deps (snowflake, json, omit, godave, gorilla/websocket, go-csync). `golang.org/x/time/rate` promoted to direct dependency automatically.
- [x] 1.2 `edge/discord/ratelimit/`: `Limiter` interface (Allow/Wait) + `tokenBucket` using `x/time/rate` at 45 req/s, burst 10. `Wait()` enforces 2s queue deadline, logs `event=rate_limit_drop`.
- [x] 1.3 `edge/discord/audit/`: `Emitter` interface (Emit) + `AuditEvent` struct with discord fields (guild_id, author_id, source_platform). `SlogEmitter` writes JSON to stdout.
- [x] 1.4 `edge/discord/client/`: `AnalysisClient` interface + ConnectRPC wrapper with failsafe-go circuit breaker (5 failures/60s, 30s open). Logs `circuit_breaker_open/close/half_open` events.
- [x] 1.5 `edge/discord/listener/`: `MessageCreate` handler filtering attachments/URLs. Pure-function `shouldAnalyze()` for testability. >10MB attachments skipped with warning. `GuildJoin` and `GuildReady` handlers (stubs for PR 2).
- [x] 1.6 `deployments/Dockerfile.edge-discord`: Multi-stage (golang:1.26-alpine → distroless/static), CGO_ENABLED=0, busybox wget HEALTHCHECK.

### Phase 3: Tests (paired with Phase 1)

- [x] 3.1 `ratelimit/ratelimit_test.go`: 6 tests — normal rate, exhaustion, Wait acquire, Wait timeout+log, queue deadline, interface compliance
- [x] 3.2 `audit/audit_test.go`: 4 tests — valid JSON fields, JSON unmarshalability, context cancelled, interface compliance
- [x] 3.3 `client/client_test.go`: 5 tests — success delegation, error propagation, CB open/log, CB recovery, interface compliance
- [x] 3.4 `listener/listener_test.go`: 8 tests — plain text skip, empty skip, small image analyze, URL analyze, large attachment skip+log, mixed attachments, whitespace skip, multiple URLs

---

## TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| 1.2 | `ratelimit/ratelimit_test.go` | Unit | N/A (new) | ✅ Written | ✅ Passed | ✅ 6 cases | ✅ Clean |
| 1.3 | `audit/audit_test.go` | Unit | N/A (new) | ✅ Written | ✅ Passed | ✅ 4 cases | ✅ Clean |
| 1.4 | `client/client_test.go` | Unit | N/A (new) | ✅ Written | ✅ Passed | ✅ 5 cases | ✅ Clean |
| 1.5 | `listener/listener_test.go` | Unit | N/A (new) | ✅ Written | ✅ Passed | ✅ 8 cases | ✅ Clean |
| 1.1 | N/A (structural) | — | N/A | — | — | ➖ Skipped: deps | — |
| 1.6 | N/A (structural) | — | N/A | — | — | ➖ Skipped: Dockerfile | — |

### Test Summary
- **Total tests written**: 23
- **Total tests passing**: 23
- **Layers used**: Unit (23)
- **Approval tests**: None (all new files)
- **Pure functions created**: `shouldAnalyze`, `shouldAnalyzeWithLog`, `guildIDString`

---

## Files Changed

| File | Action | Description |
|------|--------|-------------|
| `go.mod` | Modified | Added disgo v0.19.5 + transitive deps |
| `go.sum` | Modified | Updated dependency checksums |
| `edge/discord/ratelimit/ratelimit.go` | Created | Token bucket rate limiter (45 req/s, burst 10) |
| `edge/discord/ratelimit/ratelimit_test.go` | Created | 6 table-driven tests with synctest |
| `edge/discord/audit/audit.go` | Created | Emitter interface + SlogEmitter for audit logging |
| `edge/discord/audit/audit_test.go` | Created | 4 tests for JSON output validation |
| `edge/discord/client/client.go` | Created | ConnectRPC wrapper with failsafe-go circuit breaker |
| `edge/discord/client/client_test.go` | Created | 5 tests including CB open/recovery via synctest |
| `edge/discord/listener/listener.go` | Created | MessageCreate/GuildJoin handlers with pure filtering |
| `edge/discord/listener/listener_test.go` | Created | 8 tests for attachment/URL filtering |
| `deployments/Dockerfile.edge-discord` | Created | Multi-stage Dockerfile, distroless/static |

---

## Deviations from Design

1. **GuildCreate → GuildJoin**: disgo v0.19.5 uses `GuildJoin` (bot joins guild) and `GuildReady` (guild becomes loaded) events instead of `GuildCreate`. Both are handled.
2. **Allow() is strictly non-blocking**: The spec's "queue: 2s deadline" concept was confusing with `Allow()`. Clarified: `Allow()` returns immediately (no blocking), `Wait()` blocks with 2s deadline and logs `rate_limit_drop` on timeout. This separation is cleaner and matches `golang.org/x/time/rate.Limiter` semantics.
3. **Circuit breaker typed on Response**: The failsafe-go circuit breaker is parameterized on `connect.Response[aureliomodv1.AnalyzeResponse]` instead of `any`, which is required by the failsafe-go type system.

---

## Issues Found

None. All tests pass, project builds cleanly.

---

## Workload / PR Boundary

- **Mode**: feature-branch-chain (PR 1 of 2)
- **Current work unit**: Unit 1 (deps + ratelimit + audit + client + listener + Dockerfile)
- **Boundary**: Foundation layer only. No behavior/wiring (commands, handler, main, compose reserved for PR 2).
- **Estimated review budget impact**: ~500 lines total (~300 code + ~200 tests)

## Status

10/10 tasks complete for PR 1 / Batch 1. Ready for PR 2 (Phase 2 + remaining tests).
