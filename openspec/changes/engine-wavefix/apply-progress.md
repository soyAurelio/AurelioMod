# Apply Progress: Engine WaveFix — PR 1/3 + PR 2/3

**Batches**: 1+2 of 3
**Mode**: Strict TDD
**Date**: 2026-06-06

## Completed Tasks

### PR 1 — Foundation
- [x] 1.1 Proto: Added `degraded_confidence` (field 9, double) to `AnalyzeResponse` in `proto/aureliomod/v1/content.proto`; regenerated Go stubs via `buf generate`
- [x] 1.2 Circuitbreaker: Added `bulkhead.NewBuilder[R](mc).WithMaxWaitTime(0).Build()` as outermost policy in `WaveSpeedExecutor`; gated via `WAVESPEED_MAX_CONCURRENT` env (default 1, 0=off, invalid/negative → 1)
- [x] 1.3 Tests: 6 test cases covering bulkhead exhaustion, disabled, higher concurrency, invalid env, negative env, default enabled

### PR 2 — Core Capabilities
- [x] 2.1 Pipeline: `lastChanceRecheck(L1→L2→L3)` fallback when WaveSpeed errors (429/circuit-open/context-deadline). Returns cached decision with `DegradedConfidence` on hit, `DECISION_ERROR` on total miss.
- [x] 2.2 Pipeline tests: 7 new/updated tests covering L1/L2/L3 last-chance hits, all-cache-miss, no degradation on success, circuit breaker open gracefulness, context deadline gracefulness.
- [x] 2.3 `cmd/ytdlp-sidecar/main.go`: HTTP server `GET /?url=` → exec yt-dlp `--print-json` → JSON; 30s timeout; gates `YTDLP_SIDECAR_ENABLED` (default true), `YTDLP_SIDECAR_PORT` (default 8080); `/healthz` endpoint; graceful shutdown.
- [x] 2.4 ytdlp-sidecar tests: 7 unit tests covering 200 on valid URL, 502 on yt-dlp crash, 503 on gate-off, 400 on missing/empty url, Content-Type check, timeout handling.
- [x] 2.5 `deployments/Dockerfile.ytdlp-sidecar`: Multi-stage (golang:1.26-alpine → jrottenberg/ffmpeg:7.1-ubuntu); single base image = single supply chain surface.
- [x] 2.6 `compose.yml`: Replaced `jauderho/yt-dlp` service with `ytdlp-sidecar` build service; added `YTDLP_SIDECAR_URL` env to engine.

## TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| 1.1 | N/A (structural) | N/A | N/A | N/A | N/A | ➖ Skipped: structural proto field | N/A |
| 1.2 | `internal/circuitbreaker/circuitbreaker_test.go` | Unit | N/A (new) | ✅ Written | ✅ Passed | ✅ 6 cases | ✅ Clean |
| 1.3 | `internal/circuitbreaker/circuitbreaker_test.go` | Unit | N/A (new) | ✅ Written | ✅ Passed | ✅ 6 cases | ✅ Clean |
| 2.1 | `engine/pipeline/pipeline_test.go` | Unit | ✅ 22/22 | ✅ Written | ✅ Passed | ✅ 7 cases | ✅ Clean |
| 2.2 | `engine/pipeline/pipeline_test.go` | Unit | ✅ 22/22 | ✅ Written | ✅ Passed | ✅ 7 cases | ✅ Clean |
| 2.3 | N/A (structural w/testable handler) | N/A | N/A | N/A | N/A | ➖ Structural: main.go wires handler | N/A |
| 2.4 | `cmd/ytdlp-sidecar/handler_test.go` | Unit | N/A (new) | ✅ Written | ✅ Passed | ✅ 7 cases | ✅ Extracted handler |
| 2.5 | N/A (structural) | N/A | N/A | N/A | N/A | ➖ Skipped: Dockerfile, single approach | N/A |
| 2.6 | N/A (structural) | N/A | N/A | N/A | N/A | ➖ Skipped: compose config, single output | N/A |

## Test Summary

- **PR 1 tests written**: 6
- **PR 2 tests written**: 14 (7 pipeline + 7 sidecar)
- **Total tests passing**: 20 new tests (+ ~100 existing)
- **Layers used**: Unit (20)
- **Approval tests**: None — no refactoring tasks beyond test updates for changed behavior
- **Pure functions created**: 1 (`parseMaxConcurrent` in PR 1) + 1 (`envBool` in PR 2)

## Files Changed

| File | Action | What Was Done |
|------|--------|---------------|
| `proto/aureliomod/v1/content.proto` | Modified | Added `double degraded_confidence = 9` to `AnalyzeResponse` (PR 1) |
| `proto/aureliomod/v1/content.pb.go` | Regenerated | Auto-generated from proto (PR 1) |
| `internal/circuitbreaker/circuitbreaker.go` | Modified | Added `parseMaxConcurrent()`, Bulkhead policy (PR 1) |
| `internal/circuitbreaker/circuitbreaker_test.go` | Created | 6 test functions for Bulkhead (PR 1) |
| `engine/pipeline/pipeline.go` | Modified | Added `lastChanceRecheck()` method; WaveSpeed error → degraded fallback → DECISION_ERROR |
| `engine/pipeline/pipeline_test.go` | Modified | 5 new + 2 updated tests; `missThenHit*` smart mocks |
| `cmd/ytdlp-sidecar/main.go` | Created | HTTP server binary: env gates, real yt-dlp exec, graceful shutdown |
| `cmd/ytdlp-sidecar/handler.go` | Created | Testable handler with `YtdlpFunc` abstraction, `/healthz` endpoint |
| `cmd/ytdlp-sidecar/handler_test.go` | Created | 7 unit tests covering all handler behaviors |
| `deployments/Dockerfile.ytdlp-sidecar` | Created | Multi-stage: golang:1.26-alpine → jrottenberg/ffmpeg:7.1-ubuntu |
| `compose.yml` | Modified | Replaced `jauderho/yt-dlp` with `ytdlp-sidecar` build service + `YTDLP_SIDECAR_URL` |

## Deviations from Design

- **lastChanceRecheck triggers on ALL WaveSpeed errors**, not only 429/circuit-open. Context deadline exceeded is also treated as a graceful degradation trigger — if WaveSpeed times out, we still attempt cache re-check before returning DECISION_ERROR. This is stricter than the design which only mentions 429/circuit-open, but the rationale ("another concurrent request may have populated the cache") applies equally to timeout scenarios.
- **`newSidecarHandler` returns `http.Handler`** instead of `http.HandlerFunc` to support the Go 1.22+ `ServeMux` with method-aware routing for the `/healthz` endpoint.

## Issues Found

None.

## Remaining Tasks (PR 3 — Integration)

- [ ] 3.1 `ExtractFrames()` on `FFmpegRunner` interface + `NsJailFFmpeg`
- [ ] 3.2 `engine/media/urlframes.go` with `ParseTimestamp` + orchestrator
- [ ] 3.3 YouTube domain detection + frame extraction path in pipeline
- [ ] 3.4 `ParseTimestamp` unit tests
- [ ] 3.5 Discord listener + CDN attachment download
- [ ] 3.6 `cmd/edge-discord/main.go`
- [ ] 3.7 Integration test CDN download

## Workload / PR Boundary

- Mode: chained PR slice
- Current work unit: PR 2/3 — Core Capabilities (complete)
- Boundary: pipeline graceful degradation + ytdlp-sidecar
- Estimated review budget: ~350 lines changed (under 400-line budget)
- Chain strategy: feature-branch-chain
- Base: feature/engine-wavefix (PR 1) → Next: feature/engine-wavefix-pr2 → Target: feature/engine-wavefix

## Status

9/9 Phase 1+2 tasks complete. Ready for PR 3 (Integration).
