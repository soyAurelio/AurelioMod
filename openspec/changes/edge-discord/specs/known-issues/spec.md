# Known Issues & Future Work

## P0 — Critical (blocks production readiness)

### KNW-001: WaveSpeed concurrency limit (1 req/simultáneo)

**Severity**: CRITICAL  
**Impact**: Every content analysis call fails with HTTP 429 if more than 1 request is in flight. This means a single busy Discord server with 2+ users posting content simultaneously will experience analysis failures.

**Root cause**: WaveSpeed free tier enforces a strict concurrency limit of 1. The Engine pipeline retries up to 3 times per call, but if ANY concurrent call is in progress, the second call receives 429 on all retries.

**Affected components**:
- `engine/analyzer/wavespeed.go` — Submit + Poll loop
- `engine/pipeline/` — Retry logic (3 attempts)
- `edge/discord/handler/` — Returns ERROR to user when Engine fails

**Current behavior**:
- `/moderate <url>` → "⚠️ Analysis ERROR"
- `/status` → Engine healthy *but* all actual analysis fails
- Engine logs: `wavespeed HTTP 429: You have reached your concurrency limit`

**Proposed solutions** (ordered by complexity):
1. **Queue with single worker** (simple): Use a buffered channel or DragonflyDB queue to serialize all WaveSpeed calls. Only 1 in-flight at a time, others wait. Tradeoff: latency under load.
2. **Upgrade WaveSpeed plan** (if available): Purchase higher concurrency tier.
3. **Fallback to cached decisions** (partial): If WaveSpeed 429, check if L1/L2/L3 cache has a near-match and use that decision with degraded confidence.
4. **Multi-provider**: Add a second AI provider as failover (e.g., OpenAI moderation API).

**Decision**: Deferred to post-MVP. For now, serialized queue via DragonflyDB streams is the recommended approach.

---

### KNW-002: yt-dlp HTTP sidecar not implemented

**Severity**: CRITICAL  
**Impact**: All `CONTENT_TYPE_EXTERNAL_URL` analysis is broken. The Engine expects an HTTP sidecar at `http://yt-dlp:8080/?url=<target>` that runs yt-dlp and returns JSON metadata. The current `compose.yml` yt-dlp service just runs the CLI with no URL — it exits immediately and restarts in an infinite loop.

**Root cause**: The sidecar was designed as an HTTP wrapper around yt-dlp but never implemented. The `engine/media/ytdlp.go` code makes HTTP GET requests to the sidecar URL, but no HTTP server exists.

**Affected components**:
- `compose.yml` yt-dlp service — crash loop
- `engine/media/ytdlp.go` — HTTP calls fail with connection refused
- `engine/pipeline/` — URL analysis path broken
- `edge/discord/commands/` — `/moderate <youtube_url>` returns ERROR

**Current behavior**:
- Docker logs: `yt-dlp: error: no such option: --max-redirects` (looping)
- Engine: yt-dlp fetch fails silently
- Bot: all URL submissions return analysis ERROR

**Required implementation**:
```go
// cmd/ytdlp-sidecar/main.go — HTTP wrapper
// GET /?url=https://youtube.com/watch?v=xxx
// → runs yt-dlp --no-progress --print-json <url>
// → returns JSON to caller
```
Or: replace sidecar pattern with in-process yt-dlp via nsjail using the binary from `jrottenberg/ffmpeg:7.1-ubuntu` (which may include yt-dlp).

**Dependencies**: Must be fixed before YouTube/Twitter/TikTok URL analysis works.

---

## P1 — High (quality degradation)

### KNW-003: YouTube timestamp-specific frame extraction

**Severity**: HIGH  
**Impact**: Users sharing YouTube links with timestamps (e.g., `youtu.be/xxx?t=1m30s`) get no timestamp-specific analysis. The current pipeline would analyze the whole video (if yt-dlp worked) or fall back to URL text analysis.

**What's needed**:
1. Parse `?t=` parameter from YouTube URLs
2. yt-dlp downloads video segment starting at timestamp
3. FFmpeg extracts keyframes around the timestamp (e.g., t-5s, t, t+5s)
4. Each frame sent to WaveSpeed for analysis
5. Decision: BLOCK if ANY frame violates policy

**Status**: Not implemented. This is a post-MVP feature (Fase 3+).

---

### KNW-004: Discord attachment download not implemented

**Severity**: MEDIUM  
**Impact**: The bot currently only analyzes message text and URLs. Attachments (images, GIFs, videos uploaded directly to Discord) are detected by the listener but their binary content is not downloaded from Discord's CDN. Only the Content-Type is extracted.

**Root cause**: `edge/discord/listener/listener.go` identifies attachments but `handleMessage()` in `main.go` only sends `event.Message.Content` (text) as `raw_bytes`. The attachment URL and binary content are not fetched.

**Required**: Download attachment from Discord CDN URL, send binary bytes to Engine's `Analyze` RPC with the correct `ContentType`.

**Workaround**: Users can use `/moderate <url>` with the CDN URL, but the automatic message filter won't catch attachments.
