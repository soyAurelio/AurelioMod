# Proposal: Engine WaveFix

## Intent

Fix 4 architectural gaps causing ALL Engine analysis to fail in production per aureliomod-stack.md §1-4 requirements.

## Scope

### In Scope
- KNW-001: `Bulkhead(1)` on WaveSpeedClient, graceful 429→fallback to cache decisions
- KNW-002: `cmd/ytdlp-sidecar/main.go` — HTTP `GET /?url=` → exec yt-dlp → JSON
- KNW-003: YouTube URL `?t=` parse → FFmpeg `-ss` frame extraction → analysis
- KNW-004: Discord CDN attachment download → `RawBytes` → Engine Analyze

### Out of Scope
- Multi-instance bulkhead (phase 1 single-VPS)
- TikTok/Instagram extraction (YouTube only)
- Telegram/Twitch attachments (Discord only)
- Proto schema changes

## Capabilities

### New Capabilities
- `ytdlp-sidecar`: HTTP wrapper around yt-dlp CLI for sandboxed URL fetching
- `url-frame-extraction`: YouTube timestamp → FFmpeg seek → per-frame analysis

### Modified Capabilities
None — implementation-level fixes. No spec requirement changes.

## Approach

**KNW-001**: Add `BulkheadBuilder(1)` to `WaveSpeedExecutor`. On 429 or circuit-open, return `DECISION_ERROR` → pipeline falls through L1→L2→L3 cache (already built). Env gate: `WAVESPEED_MAX_CONCURRENT` (default 1).

**KNW-002**: Tiny Go HTTP server at `cmd/ytdlp-sidecar/main.go`: parse `?url=`, exec `yt-dlp --print-json`, return JSON + status codes. Replace compose.yml yt-dlp command with sidecar binary.

**KNW-003**: Pipeline detects `CONTENT_TYPE_EXTERNAL_URL` + YouTube domain → parse `?t=` → FFmpeg `-ss` extract frames → normalize each → analyze. Gate: `EXTRACT_FRAMES_ENABLED` (default false).

**KNW-004**: Edge bot downloads Discord CDN URL via `http.Get` → passes bytes in `RawBytes` field. No Engine change needed (pipeline already normalizes `req.RawBytes`).

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| `internal/circuitbreaker/` | Modified | Bulkhead(1) in executor chain |
| `engine/analyzer/wavespeed.go` | Modified | 429→DECISION_ERROR |
| `engine/pipeline/pipeline.go` | Modified | DECISION_ERROR→cache fallback |
| `cmd/ytdlp-sidecar/main.go` | New | HTTP sidecar for yt-dlp |
| `compose.yml` | Modified | yt-dlp → sidecar binary |
| `engine/media/ffmpeg.go` | Modified | `ExtractFrame(ctx, path, timestamp)` |
| `edge/discord/` | Modified | CDN download handler |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| Bulkhead(1) serializes calls — latency spike | High | Acceptable: L1/L2/L3 cache handles most traffic |
| yt-dlp crash on malformed URL | Medium | Timeout 30s, Engine handles 4xx/5xx gracefully |
| FFmpeg frame extraction fails on livestreams | Low | Partial results, env gate default-off |

## Rollback Plan

- KNW-001: `WAVESPEED_MAX_CONCURRENT=0` disables bulkhead
- KNW-002: Revert compose.yml to raw `jauderho/yt-dlp` image
- KNW-003: `EXTRACT_FRAMES_ENABLED=false` (default)
- KNW-004: Recompile edge-discord without CDN download logic

## Dependencies

- `failsafe-go` bulkhead (in go.mod)
- `jauderho/yt-dlp:2024.12.06` (in compose.yml)
- `jrottenberg/ffmpeg:7.1-ubuntu` (in Dockerfile)
- Edge Discord bot source recompilation required

## Success Criteria

- [ ] Engine returns cached decision (not ERROR) when WaveSpeed 429 storms
- [ ] `curl "http://ytdlp-sidecar/?url=https://youtube.com/watch?v=xxx"` → valid JSON
- [ ] `/moderate https://youtube.com/watch?v=xxx&t=120` extracts and analyzes frame at 120s
- [ ] `/moderate` with Discord attachment → bytes downloaded → analysis returns decision
