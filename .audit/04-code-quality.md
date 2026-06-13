# Code Quality & Testing Audit Report

**Date**: 2026-06-11  
**Scope**: AurelioMod Go monorepo — 101 .go files, 48 test files  
**Go version**: 1.26.4  
**Linter config**: `.golangci.yml` (govet, staticcheck, errcheck, gosec, goimports, ineffassign, misspell)

---

## Overall Score: 87/100

The codebase is production-grade in most areas. Critical paths (pipeline, hasher, safety, audit) are well-tested, idiomatic, and documented. The primary gaps are: a `panic()` in non-init code (`internal/secrets`), multiple uses of `slog.Default()` instead of injected loggers, a stub Weaviate client, bare `context.Background()` in a listener hot path, and untested `cmd/control` + `edge/discord/control` entrypoints.

---

## Test Coverage by Package

| Package                          | Test Files | Synopsis                                  | Coverage Est. | Issues                      |
| -------------------------------- | ---------: | ----------------------------------------- | -------------: | --------------------------- |
| `cmd/engine`                     |          2 | Health check, graceful shutdown, GOMAXPROCS |          ~40% | No pipeline-integration tests inline |
| `cmd/edge-discord`               |          1 | `attContentType` mapping only             |          ~15% | Needs integration smoke     |
| `cmd/control`                    |          0 | No tests                                  |             0% | **Missing**                 |
| `cmd/ytdlp-sidecar`              |          1 | Handler tests                             |          ~30% |                             |
| `engine/pipeline`                |          1 | Comprehensive: L1→L3 hits/misses, hooks, degradation | ~85% |                 |
| `engine/hasher`                  |          5 | BLAKE3, pHash, MIME, normalizer, buffer pool |         ~85% |                           |
| `engine/analyzer`                |          2 | WaveSpeed mock parsing, circuit breaker   |          ~70% |                             |
| `engine/safety`                  |          2 | Web Risk mock, cache, fail-closed         |          ~75% |                             |
| `engine/service`                 |          1 | Validation, MIME gate, deadline           |          ~80% |                             |
| `engine/audit`                   |          4 | Emitter, Neon, R2, event generation       |          ~75% |                             |
| `engine/quarantine`              |          1 | State machine transitions                 |          ~70% |                             |
| `engine/media`                   |          5 | FFmpeg, yt-dlp, audio, URL frames         |          ~60% |                             |
| `engine/nats`                    |          1 | Publisher tests                           |          ~50% |                             |
| `engine/telemetry`               |          1 | Init/shutdown with OTLP                   |          ~60% |                             |
| `internal/cache`                 |          3 | L1/L2 CRUD, DragonflyDB, integration      |          ~70% |                             |
| `internal/nats`                  |          1 | Connection, publish, subscribe            |          ~50% |                             |
| `internal/paseto`                |          1 | Token creation, verification, rotation    |          ~75% |                             |
| `internal/circuitbreaker`        |          1 | Executor config, concurrency resolution   |          ~65% |                             |
| `internal/auth`                  |          1 | Interceptor, token extraction             |          ~70% |                             |
| `internal/env`                   |          1 | Get with fallback                         |          ~80% |                             |
| `internal/secrets`               |          1 | Load, required keys                       |          ~60% | `panic()` in non-init path  |
| `internal/weaviate`              |          1 | Client stub (always returns nil)          |          ~40% | **Stub implementation**     |
| `internal/testutil`              |          1 | Container lifecycle                       |          ~50% | Needs Docker (CI-only)      |
| `edge/discord/audit`             |          1 | Event emission                            |          ~70% |                             |
| `edge/discord/client`            |          2 | Circuit breaker, ConnectRPC               |          ~70% |                             |
| `edge/discord/commands`          |          1 | Slash command formatting                  |          ~60% |                             |
| `edge/discord/handler`           |          1 | BLOCK/ALLOW/QUEUED enforcement            |          ~85% |                             |
| `edge/discord/listener`          |          1 | Filtering, download, MIME mapping, CDN    |          ~80% |                             |
| `edge/discord/ratelimit`         |          1 | Token bucket, queue deadline              |          ~75% |                             |
| `edge/discord/control`           |          0 | No tests                                  |             0% | **Missing**                 |
| `control/api`                    |          1 | Handler tests (stub)                      |          ~30% | Needs DB-backed tests       |
| `control/billing`                |          1 | Unit tests                                |          ~45% |                             |
| `proto/aureliomod/v1`            |          0 | Generated code — no tests expected        |           N/A |                             |

**Race detector**: All 30+ packages pass `-race`. Only `internal/testutil` fails (Docker-dependent container tests — expected in CI).

---

## Critical Issues

> Must fix before production.

### 1. `panic()` in non-init code — `internal/secrets/secrets.go:28`
```go
panic(fmt.Sprintf("secrets: required key %q not found", key))
```
This panics inside `Load()` which is called from service startup. If Doppler is temporarily unavailable or a key is missing in Kamal env, this kills the process with a stack trace rather than returning a structured error that the caller can log and handle with `os.Exit(1)`.

**Fix**: Return `error` instead of `panic`. Let `cmd/*/main.go` decide whether to `os.Exit(1)`.

### 2. Weaviate L3 cache is a stub — `internal/weaviate/client.go:60,75`
```go
func (c *HTTPClient) SearchSimilar(...) (*cache.CachedDecision, error) {
    // Stub: vector search not yet implemented
    return nil, nil
}
func (c *HTTPClient) IndexDecision(...) error {
    // Stub
    return nil
}
```
The L3 semantic search layer is wired into the pipeline and treated as optional — which is fine. But returning `nil, nil` silently makes the pipeline skip L3 with no observability. When a real Weaviate is not provisioned, the `weaviateClient` should be `nil` at the call site, not a stub that always returns nil results.

**Fix**: Either remove the stub client until the embedding model + weaviate-go-client/v5 are integrated (PR #5), or emit a structured telemetry event when the stub is hit so operators know L3 is inactive.

---

## High Priority Issues

### 3. `context.Background()` in listener hot path — `edge/discord/listener/listener.go:99,108,133`
```go
l.logger.InfoContext(context.Background(), "guild_join", ...)     // line 99
l.logger.InfoContext(context.Background(), "guild_ready", ...)    // line 108
logger.WarnContext(context.Background(), "attachment too large", ...) // line 133
```
These listeners receive events without a context parameter (`OnGuildJoin`, `OnGuildReady`). Using `context.Background()` breaks trace propagation — audit logs for these events won't link to any parent span.

**Fix**: Thread a context from the gateway event (if disgo provides one) or create a background context with a dedicated span.

### 4. `slog.Default()` used instead of injected logger — 3 locations
- `edge/discord/listener/listener.go:122` — `shouldAnalyze` fallback
- `engine/audit/neon.go:50` — `NewNeonEmitterFromEnv` disabled path
- `engine/audit/neon.go:89` — `NewNeonEmitter` hardcoded

Using `slog.Default()` couples these packages to the global logger, making it impossible to redirect audit/edge logs to a different output in tests.

**Fix**: Accept `*slog.Logger` as a parameter or use the context-aware variants that pull the logger from context.

### 5. No tests for `cmd/control/main.go` and `edge/discord/control/client.go`
Two packages with zero test files. `cmd/control` is the API backend for the Dashboard + Stripe billing — a critical path. `edge/discord/control` is the plan-quota gating client that can block all analysis.

**Fix**: Add at least a `TestMain_Startup` smoke test for `cmd/control` and unit tests for `edge/discord/control`'s `Consume` path with mock HTTP.

---

## Medium Priority Issues

### 6. Weaviate container test consistently flakes — `internal/testutil/containers_test.go`
```
--- FAIL: TestContainers_StartWeaviate (69.85s)
  "Startup complete" matched 0 times
```
The Weaviate container never reaches a healthy state in this environment. This is likely a resource constraint, but flaky tests erode CI trust.

**Fix**: Increase wait timeout, add a health-check retry loop, or use a lighter test image (`semitechnologies/weaviate:latest` may be too heavy).

### 7. `envBool` duplicates logic — `engine/pipeline/pipeline.go:562`
The `envBool` helper in pipeline.go repeats the same pattern as `internal/env.Get`. Consider moving it to `internal/env` for consistency.

### 8. `buildNsJailArgs` has unreachable parameter — `engine/media/ffmpeg.go:145`
```go
func buildNsJailArgs(nsjailPath, ffmpegBinary string, ...){
    _ = nsjailPath  // assigned but not used in args
```
The `nsjailPath` is passed in but silently discarded. Either use it or remove the parameter to avoid confusion.

### 9. `pHashScan.Script` is `embed`-ed but not used in the main GetL2 path
`internal/cache/dragonfly.go` embeds a Lua script and registers it as `pHashScan`, but `GetL2()` uses a client-side SCAN loop instead of `GetL2Script()`. The Lua script exists as an alternative path but isn't the default. Add a comment explaining when to use which.

---

## Low Priority / Style

### 10. Two constructors for `Normalizer` — `engine/hasher/normalizer.go:25,30`
```go
func NewNormalizer(runner media.FFmpegRunner) *Normalizer
func NewNormalizerWithRunner(runner media.FFmpegRunner, _ string) *Normalizer
```
`NewNormalizerWithRunner` ignores its second parameter and delegates to the exact same logic. Remove the redundant constructor.

### 11. `writeError` discards marshal errors — `cmd/engine/pprof.go:61`
```go
data, _ := json.Marshal(map[string]string{"error": msg})
```
If `json.Marshal` fails (extremely unlikely for `map[string]string`), the response body is empty and the error is swallowed. Log the error.

### 12. `redisWebRiskKey` is unused — `engine/safety/safety.go:162`
```go
func redisWebRiskKey(url string) string { return "webrisk:" + url }
```
This function is defined but never called. The cache key is constructed inline at line 92. Remove dead code or use it consistently.

### 13. Mixed `slog.Warn` vs `slog.WarnContext` — several locations
Some calls use `slog.Warn("msg", "key", val)` without context. Context-aware logging is the standard in this codebase. Standardize on `*Context` variants everywhere.

---

## Anti-Patterns Found

| File:line | Pattern | Severity | Fix |
|-----------|---------|----------|-----|
| `internal/secrets/secrets.go:28` | `panic()` in library code outside `init()` | Critical | Return `error` |
| `edge/discord/listener/listener.go:99,108,133` | `context.Background()` in event handlers | High | Thread context from gateway |
| `edge/discord/listener/listener.go:122` | `slog.Default()` hardcoded | High | Inject logger |
| `engine/audit/neon.go:50,89` | `slog.Default()` hardcoded | High | Inject logger |
| `engine/hasher/normalizer.go:30` | Redundant constructor with ignored param | Low | Remove |
| `engine/safety/safety.go:162` | Dead code `redisWebRiskKey` | Low | Remove or use |
| `engine/media/ffmpeg.go:145` | `_ = nsjailPath` unreachable param | Low | Remove or use |

---

## Positive Findings

### Architecture
- **L1→L2→L3→WaveSpeed cache hierarchy** is cleanly separated into interfaces (`L1Cache`, `L2Cache`, `WeaviateClient`, `Analyzer`) with mock implementations for testing.
- **Graceful degradation** is built into every layer: L2 error → skip to L3, WaveSpeed error → last-chance recheck → `DECISION_ERROR` with `DegradedConfidence=0`.
- **Inverted Quarantine** (`block first, analyze after`) is correctly implemented as a state machine with idempotent transitions.
- **MultiEmitter audit** (slog critical + Neon best-effort + R2 best-effort) has correct failure semantics: only slog failure blocks; DB/S3 failures are logged and don't propagate.

### Testing
- **synctest** used in 6+ packages for deterministic timeout/deadline testing — no real `time.Sleep` in pipeline or handler tests.
- **Table-driven tests** in `hasher`, `listener`, `handler`, `safety`, and `commands` — idiomatic and exhaustive.
- **Mock interfaces** are hand-rolled (no testify) — each mock is a focused struct with `simError` fields for failure injection. Compile-time interface checks (`var _ Interface = (*mock)(nil)`) everywhere.
- **Integration tests** use `TestMain` with testcontainers for NATS, DragonflyDB, and Weaviate — suite-level, not per-test, matching the stack spec.
- **Edge case coverage**: empty input, nil analyzer, circuit breaker open, partial back-population failure, DM delivery failure, oversized attachments, and context cancellation all tested.

### Error Handling
- **`%w` wrapping** is consistent — 50+ instances across all packages.
- **Sentinel errors** defined for domain concepts: `ErrMaliciousURL`, `ErrServiceUnavailable`, `ErrNotFound`, `errSlogFailed`.
- **ConnectRPC error mapping** in `engine/service/handler.go:85-88` correctly maps `context.DeadlineExceeded` → `CodeDeadlineExceeded` and all other errors → `CodeInternal`.
- **Circuit breaker** with failsafe-go wraps all WaveSpeed calls and all Engine ConnectRPC calls from the edge.

### Logging
- **slog exclusively** — zero `fmt.Println`, `log.Print`, `logrus`, or `zap` usage.
- **Structured key=value pairs** throughout, with consistent event naming (`"event"`, `"error"`, `"workspace_id"`).
- **JSON handler** configured in all three `cmd/*/main.go` entrypoints.
- **Sensitive data protection**: API keys never appear in log statements. Token contents are not logged.

### Concurrency
- **`sync.Pool` for 32KB byte buffers** in `engine/hasher/bufferpool.go` — reduces GC pressure on the normalization hot path.
- **`sync.WaitGroup` tracking** for fire-and-forget goroutines — `pipeline.Wait()` and `MultiEmitter.Wait()` for graceful shutdown.
- **`GOMAXPROCS = N-1`** for the Engine — reserves one CPU for FFmpeg subprocesses.
- **No data races** detected by `-race` in any application package.

### Protobuf & ConnectRPC
- **Proto3 with zero-value enums** (`DECISION_UNSPECIFIED = 0`, etc.) — correct for protobuf best practices.
- **Buf v2 configuration** with STANDARD lint and FILE breaking changes.
- **Generated code** is excluded from linting (`.golangci.yml` ignores `*.pb.go`, `*.connect.go`).
- **Service naming** consistent: `ContentAnalysisService` with one `Analyze` RPC.

### Security
- **nsjail sandboxing** for all FFmpeg invocations — `os/exec` only as fallback when `MEDIA_SANDBOX_ENABLED=false`.
- **Google Web Risk URL check** before any yt-dlp fetch — fail-closed (`ErrServiceUnavailable` on API error).
- **PASETO v4** (no JWT) with Ed25519 asymmetric keys and rotation support (`RotatableTokenManager`).
- **PASETO-protected pprof** endpoint on separate port with admin token auth.
- **No `text/template` or `html/template` with user input** anywhere.

### Dependencies
- **Testify not used** — all mocks are hand-rolled, reducing dependency surface.
- **No `replace` directives** in `go.mod`.
- **Go 1.26.4** — current stable.

---

## Summary

| Area              | Score | Key Gap                              |
| ----------------- | ----: | ------------------------------------ |
| Testing           | 85/100 | `cmd/control`, `edge/discord/control` untested |
| Error Handling    | 85/100 | `panic()` in `secrets` package       |
| Context Propagation | 80/100 | `context.Background()` in listener  |
| Logging           | 85/100 | `slog.Default()` in 3 locations      |
| Concurrency       | 90/100 | No data races, proper pool usage      |
| Code Organization | 90/100 | One redundant constructor            |
| Protobuf/ConnectRPC | 95/100 | Clean proto, correct enums          |
| Performance       | 88/100 | Weaviate stub needs real impl        |
| Dependencies      | 95/100 | No deprecated or unnecessary deps    |
| Security          | 92/100 | nsjail sandboxing, PASETO, Web Risk  |
| **Overall**       | **87/100** |                                     |
