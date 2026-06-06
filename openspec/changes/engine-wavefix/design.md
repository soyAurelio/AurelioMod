# Design: Engine WaveFix

## Technical Approach

Add Bulkhead(1) to existing failsafe-go executor chain wrapping WaveSpeed calls. When WaveSpeed rejects or circuit opens, pipeline does a last-chance re-check of L1→L2→L3 caches (which may have been populated by concurrent requests while this one waited), then returns `DECISION_ERROR` if empty. New `cmd/ytdlp-sidecar` binary wraps yt-dlp as HTTP server. FFmpeg runner gains `ExtractFrames` for YouTube `?t=` timestamps. Edge Discord listener downloads CDN attachments in-memory, passes bytes via `RawBytes`.

## Architecture Decisions

| Decision | Choice | Rejected | Rationale |
|----------|--------|----------|-----------|
| Bulkhead placement | In `WaveSpeedExecutor` chain (same as CB), outermost policy | Pipeline-layer semaphore | Already using failsafe-go; Bulkhead(1) as outermost guard prevents all concurrent calls, retry/circuit logic remains untouched. Pipeline stays cache-only concern. |
| 429/circuit-open flow | Last-chance L1→L2→L3 re-check, then `DECISION_ERROR` with `degraded_confidence` | Return error immediately | While Bulkhead held this request, another concurrent call may have populated cache. Re-checking costs <5ms vs fresh WaveSpeed call. |
| ytdlp-sidecar Dockerfile | Base `jrottenberg/ffmpeg:7.1-ubuntu` (already has yt-dlp) | Separate `golang:alpine` image | Single base image = single supply chain surface. FFmpeg image is already in Engine build chain. |
| Frame extraction concurrency | Sequential (one frame at a time) | Concurrent goroutine pool | Bulkhead(1) on WaveSpeed means no parallelism benefit. Sequential is simpler, less coordination. |
| Discord attachment download | In-memory `http.Get` → `RawBytes` | Stream to temp file | 10MB max per message, Go http.Client buffers efficiently. Temp file adds disk I/O + cleanup complexity for zero gain at this scale. |

## Data Flow

### Graceful Degradation (KNW-001)
```
Edge → Pipeline.Execute()
  └→ L1 check → miss → L2 check → miss → L3 check → miss
    └→ WaveSpeed.Analyze() ──[Bulkhead(1)]──→ 429/circuit-open
      └→ lastChanceRecheck(L1→L2→L3)
        ├─ hit → return cached decision, CACHE_LEVEL_L{1|2|3}, degraded_confidence
        └─ miss → return DECISION_ERROR, CACHE_LEVEL_NONE, confidence=0
```

### Frame Extraction (KNW-003)
```
Edge ──URL(youtube.com/?v=xxx&t=120)──→ Pipeline
  └→ detect CONTENT_TYPE_EXTERNAL_URL + youtube domain
    └→ urlframes.ParseTimestamp(url) → 120s
      └→ ytdlp.Fetch(url) → download media to /tmp
        └→ ffmpeg ExtractFrames(path, 120s, 3)
          └→ for each frame: normalize → BLAKE3 → L1→L2→L3→WaveSpeed
            └→ aggregate results → AnalyzeResponse
```

### Discord Attachment (KNW-004)
```
Discord Gateway ──MESSAGE_CREATE(attachments)──→ Edge Listener
  └→ http.Get(cdn.discordapp.com/attachments/...)
    └→ read body (≤10MB)
      └→ AnalyzeRequest{RawBytes: body, ContentType: mapped_from_mime}
        └→ Engine.Analyze() → response
```

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/circuitbreaker/circuitbreaker.go` | Modify | Add `bulkhead.NewBuilder[R](1)` as outermost policy in `WaveSpeedExecutor` |
| `engine/pipeline/pipeline.go` | Modify | WaveSpeed error path: last-chance cache re-check → `DECISION_ERROR` with `DegradedConfidence` field |
| `cmd/ytdlp-sidecar/main.go` | Create | HTTP server: `GET /?url=` → `exec yt-dlp --print-json` → JSON response, 30s timeout |
| `deployments/Dockerfile.ytdlp-sidecar` | Create | Multi-stage: `golang:1.26-alpine` build → `jrottenberg/ffmpeg:7.1-ubuntu` runtime with yt-dlp |
| `compose.yml` | Modify | Replace `jauderho/yt-dlp` service with `ytdlp-sidecar` binary build |
| `engine/media/urlframes.go` | Create | `ParseTimestamp(url)`, `ExtractFrames(ffmpeg, path, ts, n)` orchestrator |
| `engine/media/ffmpeg.go` | Modify | Add `ExtractFrames(ctx, inputPath, timestampSec, maxFrames int) ([][]byte, error)` to FFmpegRunner interface + NsJailFFmpeg |
| `engine/pipeline/pipeline.go` | Modify | `CONTENT_TYPE_EXTERNAL_URL` + youtube domain detection → frame extraction path |
| `edge/discord/listener/listener.go` | Create | Discord gateway listener with attachment download on MESSAGE_CREATE |
| `cmd/edge-discord/main.go` | Create | Wire listener to Engine RPC client, env gates, slog |
| `proto/aureliomod/v1/content.proto` | Modify | Add `degraded_confidence` field to `AnalyzeResponse` (optional, proto3) |

## Interfaces / Contracts

```go
// FFmpegRunner — new method
ExtractFrames(ctx context.Context, inputPath string, timestampSec int, maxFrames int) ([][]byte, error)

// URL frame extraction
func ParseTimestamp(rawURL string) (int, bool)  // seconds, ok
func ExtractAndAnalyze(ctx context.Context, ffmpeg FFmpegRunner, pipe Pipeline, url string, timestampSec int) ([]*v1.AnalyzeResponse, error)

// ytdlp-sidecar endpoint
// GET /?url=<encoded> → 200 JSON | 502 {"error":"..."}
// Port: YTDLP_SIDECAR_PORT (default 8080)
// Gate: YTDLP_SIDECAR_ENABLED (default true)
```

## Testing Strategy

| Layer | What to Test | Approach |
|-------|-------------|----------|
| Unit — Bulkhead | Permit exhaustion returns error, 0 disables | Mock executor with fake permits |
| Unit — Pipeline | 429 → last-chance re-check → DECISION_ERROR | Inject mock analyzer that returns 429, verify cache re-check |
| Unit — ytdlp-sidecar | HTTP 200 on valid URL, 502 on crash, gate off → 503 | httptest.Server, mock exec |
| Unit — Frame extraction | ParseTimestamp edge cases, ffmpeg arg construction | Table-driven, nil ffmpeg |
| Integration — Disc CDN | Real http.Get to test CDN URL → bytes | httptest.NewServer serving test attachment |

## Migration / Rollout

All 4 capabilities are gated by env vars (defaults per proposal §Rollback). No data migration. Feature flags: `WAVESPEED_MAX_CONCURRENT`, `YTDLP_SIDECAR_ENABLED`, `EXTRACT_FRAMES_ENABLED`, `ATTACHMENT_ANALYSIS_ENABLED`. Compose.yml acquires `ytdlp-sidecar` build service alongside engine.

## Open Questions

None — all key decisions resolved in architecture decisions table above.
