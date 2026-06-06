# Engine WaveFix — Delta Specs

Four new capabilities, per aureliomod-stack.md §1–4.

---

## 1. Graceful Degradation

### Purpose

WaveSpeed API degradation MUST NOT block moderation decisions. When WaveSpeed is overloaded or slow, the Engine SHALL fall back through the L1→L2→L3 cache chain instead of returning errors.

### Requirements

| # | Req | Keyword |
|---|-----|---------|
| R1.1 | WaveSpeed client SHALL serialize concurrent calls using `failsafe-go` Bulkhead(1) | MUST |
| R1.2 | On HTTP 429 or circuit-open, Engine SHALL return `DECISION_FALLBACK` (not ERROR) | MUST |
| R1.3 | Pipeline SHALL route `DECISION_FALLBACK` through L1 BLAKE3 → L2 pHash → L3 Weaviate | MUST |
| R1.4 | If ANY cache level has a near-match (Hamming ≤5 for L2, similarity >0.92 for L3), Engine SHALL return that cached decision | MUST |
| R1.5 | Env gate `WAVESPEED_MAX_CONCURRENT` (default 1) SHALL control bulkhead permits; 0 disables it | SHOULD |

### Scenarios

#### S1: WaveSpeed 429 storm → L1 cache hit
- GIVEN WaveSpeed returns HTTP 429 and L1 cache has BLAKE3 match for content
- WHEN Engine processes content
- THEN pipeline returns cached decision within 50ms AND does NOT call WaveSpeed

#### S2: WaveSpeed circuit open → L2 near-match
- GIVEN circuit breaker is open and L2 pHash has Hamming distance ≤5
- WHEN Engine processes content
- THEN pipeline returns nearest L2 cached decision AND logs `wave_speed_fallback: true`

#### S3: All caches miss, WaveSpeed available
- GIVEN no cache hit across L1/L2/L3 and bulkhead permit available
- WHEN Engine processes content
- THEN WaveSpeed is called, decision stored in L1/L2/L3, and returned

---

## 2. ytdlp-sidecar

### Purpose

Sandboxed HTTP wrapper around yt-dlp CLI. Engine calls sidecar instead of shelling out directly, satisfying §7 sandboxing requirement.

### Requirements

| # | Req | Keyword |
|---|-----|---------|
| R2.1 | HTTP server SHALL accept `GET /?url=<encoded_url>` and return yt-dlp JSON output | MUST |
| R2.2 | Feature gate `YTDLP_SIDECAR_ENABLED` (default `true`) SHALL control sidecar availability | MUST |
| R2.3 | Port SHALL be configurable via `YTDLP_SIDECAR_PORT` (default `8080`) | MUST |
| R2.4 | Sidecar SHALL enforce 30s timeout per yt-dlp invocation | MUST |
| R2.5 | On yt-dlp failure, SHALL return 5xx with JSON error body `{"error": "..."}` | MUST |

### Scenarios

#### S1: Valid YouTube URL → JSON metadata
- GIVEN `YTDLP_SIDECAR_ENABLED=true`
- WHEN `GET /?url=https%3A%2F%2Fyoutube.com%2Fwatch%3Fv%3Dabc123`
- THEN returns HTTP 200 with JSON containing `title`, `duration`, `thumbnail`, `formats`

#### S2: yt-dlp crash → 500 error JSON
- GIVEN yt-dlp process crashes on malformed URL
- WHEN sidecar receives request for that URL
- THEN returns HTTP 502 with `{"error": "yt-dlp execution failed"}` AND logs structured error via slog

#### S3: Feature gate disabled
- GIVEN `YTDLP_SIDECAR_ENABLED=false`
- WHEN Engine attempts sidecar call
- THEN Engine falls back to direct yt-dlp shell exec (existing behavior)

---

## 3. URL Frame Extraction

### Purpose

Parse YouTube `?t=` timestamp from URLs, extract keyframes via FFmpeg, and analyze each frame through the Engine normalization pipeline.

### Requirements

| # | Req | Keyword |
|---|-----|---------|
| R3.1 | Pipeline SHALL detect `CONTENT_TYPE_EXTERNAL_URL` with YouTube domain | MUST |
| R3.2 | System SHALL parse `?t=` (seconds) or `&t=` from YouTube URLs | MUST |
| R3.3 | FFmpeg SHALL extract 3 frames at `-ss {timestamp}`, `-vframes 3` from downloaded media | MUST |
| R3.4 | Each extracted frame SHALL pass through Engine normalize → BLAKE3 hash → L1/L2/L3 analysis | MUST |
| R3.5 | Env gate `EXTRACT_FRAMES_ENABLED` (default `false`) SHALL control this feature | MUST |
| R3.6 | If frame extraction fails (livestream, DRM), Engine SHALL return partial results with success frames only | SHOULD |

### Scenarios

#### S1: YouTube URL with timestamp → frames analyzed
- GIVEN `EXTRACT_FRAMES_ENABLED=true` and URL `https://youtube.com/watch?v=xxx&t=120`
- WHEN pipeline processes URL
- THEN yt-dlp downloads media, FFmpeg extracts 3 frames at 120s, each frame normalized and analyzed

#### S2: No timestamp in URL → skip extraction
- GIVEN `EXTRACT_FRAMES_ENABLED=true` and URL `https://youtube.com/watch?v=xxx` (no `?t=`)
- WHEN pipeline processes URL
- THEN extraction is skipped, pipeline falls through to standard content analysis

---

## 4. Discord Attachment Download

### Purpose

Auto-download Discord CDN attachments in edge listener, passing raw binary bytes to Engine instead of URL text.

### Requirements

| # | Req | Keyword |
|---|-----|---------|
| R4.1 | Discord edge listener SHALL detect CDN attachment URLs (`cdn.discordapp.com` or `media.discordapp.net`) | MUST |
| R4.2 | Listener SHALL download attachment bytes via `http.Get` before forwarding to Engine | MUST |
| R4.3 | Downloaded bytes SHALL be passed in `RawBytes` field (not URL text) to Engine Analyze | MUST |
| R4.4 | Env gate `ATTACHMENT_ANALYSIS_ENABLED` (default `true`) SHALL control this feature | MUST |
| R4.5 | Download failure SHALL be logged as structured error; message skipped (not crashed) | MUST |

### Scenarios

#### S1: Discord image attachment → bytes analyzed
- GIVEN `ATTACHMENT_ANALYSIS_ENABLED=true` and user sends image to Discord
- WHEN edge listener detects CDN URL in attachment
- THEN listener downloads bytes, passes `RawBytes` to Engine, Engine normalizes and returns decision

#### S2: CDN download fails → graceful skip
- GIVEN `ATTACHMENT_ANALYSIS_ENABLED=true` and CDN URL returns 404/403
- WHEN listener attempts download
- THEN error is logged via slog AND message is skipped without crashing the listener
