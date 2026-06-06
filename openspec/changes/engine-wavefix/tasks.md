# Tasks: Engine WaveFix

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | 650-700 |
| 400-line budget risk | High |
| Chained PRs recommended | Yes |
| Suggested split | PR 1 (Foundation) → PR 2 (Core) → PR 3 (Integration) |
| Delivery strategy | ask-on-risk |
| Chain strategy | feature-branch-chain |

Decision needed before apply: Yes
Chained PRs recommended: Yes
Chain strategy: feature-branch-chain
400-line budget risk: High

### Suggested Work Units

| Unit | Goal | Likely PR | Notes |
|------|------|-----------|-------|
| 1 | Proto + circuitbreaker foundation | PR 1 | Base: feature/engine-wavefix; unblocks all other work |
| 2 | Graceful degradation pipeline + ytdlp-sidecar | PR 2 | Base: PR 1 branch; capabilities #1 and #2 |
| 3 | URL frame extraction + Discord attachment | PR 3 | Base: PR 2 branch; capabilities #3 and #4 |

## Phase 1: Foundation (PR 1)

- [x] 1.1 Add `degraded_confidence` float field to `AnalyzeResponse` in `proto/aureliomod/v1/content.proto`; regenerate Go stubs
- [x] 1.2 Add `bulkhead.NewBuilder[R](1)` as outermost policy in `WaveSpeedExecutor` chain in `internal/circuitbreaker/circuitbreaker.go`; gate with `WAVESPEED_MAX_CONCURRENT` env (0=off)
- [x] 1.3 Write unit test for Bulkhead permit exhaustion and `WAVESPEED_MAX_CONCURRENT=0` disable in `internal/circuitbreaker/circuitbreaker_test.go`

## Phase 2: Core Capabilities (PR 2)

- [ ] 2.1 Add `lastChanceRecheck(L1→L2→L3)` fallback path in `engine/pipeline/pipeline.go` when WaveSpeed returns 429 or circuit-open; return `DECISION_ERROR` with `DegradedConfidence` on miss (spec R1.1-R1.4)
- [ ] 2.2 Unit test pipeline 429 → cache re-check → `DECISION_ERROR` with mock analyzer in `engine/pipeline/pipeline_test.go` (spec S1, S2)
- [ ] 2.3 Create `cmd/ytdlp-sidecar/main.go`: HTTP server `GET /?url=` → exec yt-dlp `--print-json` → JSON; 30s timeout; gates `YTDLP_SIDECAR_ENABLED`/`YTDLP_SIDECAR_PORT` (spec R2.1-R2.5)
- [ ] 2.4 Unit test ytdlp-sidecar: 200 on valid URL, 502 on crash, 503 on gate-off via `httptest.Server` (spec S1, S2, S3)
- [ ] 2.5 Create `deployments/Dockerfile.ytdlp-sidecar`: multi-stage (golang:1.26-alpine build → jrottenberg/ffmpeg:7.1-ubuntu runtime)
- [ ] 2.6 Add ytdlp-sidecar build service to `compose.yml`; replace `jauderho/yt-dlp` service

## Phase 3: Integration (PR 3)

- [ ] 3.1 Add `ExtractFrames(ctx, inputPath, timestampSec, maxFrames int) ([][]byte, error)` to `FFmpegRunner` interface and `NsJailFFmpeg` in `engine/media/ffmpeg.go` (spec R3.3)
- [ ] 3.2 Create `engine/media/urlframes.go` with `ParseTimestamp(url) (int, bool)` and `ExtractAndAnalyze(ctx, ffmpeg, pipe, url, ts)` orchestrator (spec R3.2, R3.4)
- [ ] 3.3 Add YouTube domain + `CONTENT_TYPE_EXTERNAL_URL` detection and frame extraction path to `engine/pipeline/pipeline.go`; gate `EXTRACT_FRAMES_ENABLED` (spec R3.1, R3.5)
- [ ] 3.4 Unit test `ParseTimestamp` edge cases and ffmpeg arg construction in `engine/media/urlframes_test.go` (spec S1, S2)
- [ ] 3.5 Create `edge/discord/listener/listener.go`: Discord gateway listener, detect CDN URLs on MESSAGE_CREATE, download bytes via `http.Get`, pass `RawBytes` to Engine (spec R4.1-R4.3)
- [ ] 3.6 Create `cmd/edge-discord/main.go`: wire listener to Engine RPC, env gates (`ATTACHMENT_ANALYSIS_ENABLED`), structured slog (spec R4.4, R4.5)
- [ ] 3.7 Integration test Discord CDN download with `httptest.NewServer` serving test attachment (spec S1, S2)

## Phase 4: Verification

- [ ] 4.1 Verify all 4 env gates properly disable each capability per spec: `WAVESPEED_MAX_CONCURRENT=0`, `YTDLP_SIDECAR_ENABLED=false`, `EXTRACT_FRAMES_ENABLED=false`, `ATTACHMENT_ANALYSIS_ENABLED=false`
- [ ] 4.2 Run full test suite: `go test ./...`; confirm all new and existing tests pass
- [ ] 4.3 Verify proto regeneration produces no drift from committed stubs
