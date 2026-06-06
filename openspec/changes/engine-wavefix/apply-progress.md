# Apply Progress: Engine WaveFix — PR 1/3 (Foundation)

**Batch**: 1 of 3
**Mode**: Strict TDD
**Date**: 2026-06-06

## Completed Tasks

- [x] 1.1 Proto: Added `degraded_confidence` (field 9, double) to `AnalyzeResponse` in `proto/aureliomod/v1/content.proto`; regenerated Go stubs via `buf generate`
- [x] 1.2 Circuitbreaker: Added `bulkhead.NewBuilder[R](mc).WithMaxWaitTime(0).Build()` as outermost policy in `WaveSpeedExecutor`; gated via `WAVESPEED_MAX_CONCURRENT` env (default 1, 0=off, invalid/negative → 1)
- [x] 1.3 Tests: 6 test cases covering bulkhead exhaustion, disabled, higher concurrency, invalid env, negative env, default enabled

## TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| 1.1 | N/A (structural) | N/A | N/A | N/A | N/A | ➖ Skipped: structural proto field, single output | N/A |
| 1.2 | `internal/circuitbreaker/circuitbreaker_test.go` | Unit | N/A (new) | ✅ Written | ✅ Passed | ✅ 6 cases | ✅ Clean |
| 1.3 | `internal/circuitbreaker/circuitbreaker_test.go` | Unit | N/A (new) | ✅ Written | ✅ Passed | ✅ 6 cases | ✅ Clean |

## Test Summary

- **Total tests written**: 6
- **Total tests passing**: 6
- **Layers used**: Unit (6)
- **Approval tests**: None — no refactoring tasks
- **Pure functions created**: 1 (`parseMaxConcurrent`)

## Files Changed

| File | Action | What Was Done |
|------|--------|---------------|
| `proto/aureliomod/v1/content.proto` | Modified | Added `double degraded_confidence = 9` to `AnalyzeResponse` |
| `proto/aureliomod/v1/content.pb.go` | Regenerated | Auto-generated from proto by `buf generate` |
| `internal/circuitbreaker/circuitbreaker.go` | Modified | Added `parseMaxConcurrent()`, Bulkhead policy gated by `WAVESPEED_MAX_CONCURRENT` env |
| `internal/circuitbreaker/circuitbreaker_test.go` | Created | 6 test functions covering all Bulkhead scenarios |

## Deviations from Design

None — implementation matches design.md exactly. Bulkhead is outermost policy, `WAVESPEED_MAX_CONCURRENT` env gate with 0=off, `WithMaxWaitTime(0)` for immediate rejection.

## Issues Found

None.

## Remaining Tasks (PR 2 — Core Capabilities)

- [ ] 2.1 `lastChanceRecheck(L1→L2→L3)` fallback in pipeline
- [ ] 2.2 Pipeline 429 → cache re-check unit tests
- [ ] 2.3 `cmd/ytdlp-sidecar/main.go`
- [ ] 2.4 ytdlp-sidecar unit tests
- [ ] 2.5 `deployments/Dockerfile.ytdlp-sidecar`
- [ ] 2.6 `compose.yml` ytdlp-sidecar service

## Workload / PR Boundary

- Mode: chained PR slice
- Current work unit: PR 1/3 — Foundation
- Boundary: proto field + circuitbreaker changes only
- Estimated review budget: ~120 lines changed (well under 400-line budget)

## Status

3/3 Phase 1 tasks complete. Ready for PR 2 (Core Capabilities).
