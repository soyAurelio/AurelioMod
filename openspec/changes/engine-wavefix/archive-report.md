# Archive Report: engine-wavefix

**Change**: engine-wavefix  
**Archived**: 2026-06-10  
**Mode**: Strict TDD  
**Status**: COMPLETE — 16/16 implementation tasks + 3/3 verification tasks completed

## Executive Summary

Fixed 4 critical gaps in the Engine service across 3 PRs:
- **PR 1**: `degraded_confidence` proto field + Bulkhead(1) to serialize WaveSpeed calls
- **PR 2**: Graceful degradation pipeline (429 → cache re-check) + ytdlp-sidecar (sandboxed HTTP wrapper)
- **PR 3**: YouTube frame extraction (ParseTimestamp + DownloadAndExtractFrames) + Discord attachment listener

All 19 tasks (16 impl + 3 verification) completed. 48 new tests added.

## Completeness

| Metric | Value |
|--------|-------|
| Implementation tasks | 16/16 |
| Verification tasks | 3/3 |
| Total tasks complete | 19/19 |
| New tests | 48 |
| All tests passing | ✅ 22/22 packages |

## Verification Report (Phase 4 — Completed 2026-06-10)

### 4.1 Env Gates
| Gate | Status | Evidence |
|------|--------|----------|
| `WAVESPEED_MAX_CONCURRENT=0` | ✅ | `parseMaxConcurrent()` in circuitbreaker.go; when 0, Bulkhead not created |
| `YTDLP_SIDECAR_ENABLED=false` | ✅ | `envBool("YTDLP_SIDECAR_ENABLED", true)` in sidecar handler; returns 503 when false |
| `EXTRACT_FRAMES_ENABLED=false` | ✅ | `envBool("EXTRACT_FRAMES_ENABLED", false)` in pipeline.go; frame extraction skipped |
| `ATTACHMENT_ANALYSIS_ENABLED=false` | ✅ | Added during SDD #1 verification; attachment download skipped when false |

### 4.2 Full Test Suite
✅ 22/22 packages pass with `go test -count=1 -short ./...`

### 4.3 Proto Drift Check
✅ `buf build` passes. Proto field 9 (`degraded_confidence`) present in both `.proto` and `.pb.go`.

## Deviations

1. **lastChanceRecheck triggers on ALL WaveSpeed errors** — not just 429/circuit-open. Context deadline exceeded also triggers cache re-check (rationale: another concurrent request may have populated the cache).
2. **ATTACHMENT_ANALYSIS_ENABLED gate** was specified in task 3.6 but not implemented until SDD #1 verification (2026-06-10).

## Risks

None remaining. All 4 capabilities are properly gated and tested.
