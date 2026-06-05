# Apply Progress: Engine Hardening — PR #1

**Date**: 2026-06-05
**Branch**: `feature/engine-hardening-pr1`
**Mode**: Strict TDD
**Status**: 5/15 tasks complete (33%)

## Completed Tasks

- [x] **T-001** GOMAXPROCS=N-1 in cmd/engine/main.go
- [x] **T-002** pprof :6060 with PASETO Bearer auth
- [x] **T-003** MIME validation gate (ENFORCE_MIME)
- [x] **T-004** .woodpecker.yml + .golangci.yml
- [x] **T-005** go.mod deps (testcontainers-go, google/safebrowsing)

## TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| T-001 | cmd/engine/main_test.go | Unit | ✅ 2/2 | ✅ Written | ✅ Passed | ✅ 2 cases | ➖ None needed |
| T-002 | cmd/engine/pprof_test.go | Unit | N/A (new file) | ✅ Written | ✅ Passed | ✅ 4 cases | ➖ None needed |
| T-003a | engine/hasher/mime_test.go | Unit | ✅ 10/10 | ✅ Written | ✅ Passed | ✅ 5 scenarios | ➖ None needed |
| T-003b | engine/service/handler_test.go | Integration | ✅ 6/6 | ✅ Written | ✅ Passed | ✅ 3 gate scenarios | ➖ None needed |
| T-004 | None | N/A | N/A | ⏭️ Structural | N/A | N/A | N/A |
| T-005 | None | N/A | N/A | ⏭️ Structural | N/A | N/A | N/A |

## Test Summary

- **Total tests passing**: 127
- **Tests written in this PR**: 17 new (2 GOMAXPROCS + 4 pprof + 10 MIME + 3 handler gate − 2 old detectMIME)
- **Layers used**: Unit (6), Integration (3)

## Files Changed

| File | Action | Description |
|------|--------|-------------|
| `cmd/engine/main.go` | Modified | +init() GOMAXPROCS, +pprof server wiring, +ENFORCE_MIME pass-through |
| `cmd/engine/main_test.go` | Modified | +TestGOMAXPROCS_Clamped, +TestGOMAXPROCS_ReservesCore |
| `cmd/engine/pprof.go` | Created | pprofMux() with PASETO auth middleware, startPprofServer() |
| `cmd/engine/pprof_test.go` | Created | 4 PASETO auth scenarios |
| `engine/hasher/mime.go` | Created | DetectMIME() + ValidateContentType() |
| `engine/hasher/mime_test.go` | Created | 5 MIME validation scenarios |
| `engine/hasher/normalizer.go` | Modified | Replaced detectMIME() → DetectMIME(), removed local detectMIME() |
| `engine/hasher/hasher_test.go` | Modified | detectMIME → DetectMIME |
| `engine/service/handler.go` | Modified | +enforceMIME field, +MIME gate before pipeline, +contentTypeToMIME() |
| `engine/service/handler_test.go` | Modified | +MIME gate tests (disabled/bypass, PE+IMAGE→reject, JPEG+IMAGE→accept) |
| `.woodpecker.yml` | Created | 5-stage CI: lint → test(-race) → govulncheck → syft → build |
| `.golangci.yml` | Created | govet, staticcheck, errcheck, gosec, goimports, ineffassign, misspell |
| `go.mod` | Modified | +testcontainers-go, +safebrowsing |
| `go.sum` | Modified | Updated checksums |

## Deviations from Design

None — implementation matches design exactly.

## Remaining Tasks

- [ ] T-006: FFmpeg nsjail sandbox (PR #2)
- [ ] T-007: yt-dlp nsjail sidecar (PR #2)
- [ ] T-008: Safe Browsing service (PR #2)
- [ ] T-009: Inject FFmpegRunner into Normalizer (PR #2)
- [ ] T-010: Dockerfile nsjail + sidecar (PR #2)
- [ ] T-011: testcontainers helpers (PR #3)
- [ ] T-012: cache integration tests (PR #3)
- [ ] T-013: pipeline TestMain (PR #3)
- [ ] T-014: JPEG anti-polyglot (PR #3)
- [ ] T-015: sandbox integration tests (PR #3)
