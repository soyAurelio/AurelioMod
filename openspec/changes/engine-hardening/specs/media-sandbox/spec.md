# Media Sandbox Specification

## Purpose

Run all external media tools (FFmpeg, yt-dlp) inside nsjail sandboxes with strict resource limits. Prevent arbitrary code execution, network exfiltration, and filesystem escape. Apply Google Safe Browsing URL reputation checks before any yt-dlp fetch.

## Requirements

### Requirement: Sandboxed FFmpeg Execution

The system MUST execute every FFmpeg invocation inside an nsjail sandbox with the following constraints: `--net none` (no network), `--cwd /tmp` as the only writable path, media input mounted read-only, and a wall-time timeout derived from the operation's context deadline.

#### Scenario: Successful normalization

- GIVEN a valid video file and a context with a 30s deadline
- WHEN FFmpeg is invoked through the sandbox wrapper
- THEN FFmpeg runs inside nsjail, processes the video, and exits 0
- AND the normalized output is returned to the caller

#### Scenario: Timeout enforced

- GIVEN a context deadline of 5 seconds and a media file requiring 30+ seconds to process
- WHEN FFmpeg is invoked through the sandbox wrapper
- THEN nsjail kills the process when the wall-time limit is reached
- AND the caller receives a `context.DeadlineExceeded` error

#### Scenario: Network access blocked

- GIVEN an FFmpeg invocation that attempts HTTP output
- WHEN FFmpeg runs inside the sandbox
- THEN nsjail rejects the network syscall
- AND the process fails with a sandbox violation error

#### Scenario: Filesystem escape blocked

- GIVEN an FFmpeg invocation that attempts to write outside `/tmp`
- WHEN FFmpeg runs inside the sandbox
- THEN nsjail denies the write syscall
- AND the process fails with a permission-denied error

### Requirement: Sandboxed yt-dlp as Sidecar

The system MUST run yt-dlp in a separate container (sidecar), not inside the Engine image. yt-dlp invocations MUST be wrapped in nsjail with `--net none` (it fetches via a proxy), a redirect depth limit of 5, and the same tmp/cwd restrictions as FFmpeg.

#### Scenario: yt-dlp unavailable

- GIVEN the yt-dlp sidecar container is not running
- WHEN a media URL requires yt-dlp retrieval
- THEN the Engine logs a warning and returns a degradation error
- AND no subprocess is spawned

### Requirement: Google Safe Browsing Pre-Fetch Check

The system MUST query the Google Safe Browsing v4 API for every URL before yt-dlp fetches it. URLs classified as `MALWARE`, `SOCIAL_ENGINEERING`, or `UNWANTED_SOFTWARE` MUST be blocked.

#### Scenario: Safe URL proceeds

- GIVEN URL `https://example.com/video.mp4`
- WHEN Safe Browsing lookup returns no threats
- THEN the Engine proceeds to the yt-dlp fetch

#### Scenario: Malicious URL blocked

- GIVEN URL `https://evil.com/malware.mp4`
- WHEN Safe Browsing lookup returns `THREAT_TYPE_MALWARE`
- THEN the Engine rejects the request with gRPC `PermissionDenied`
- AND yt-dlp is never invoked

#### Scenario: Safe Browsing API unavailable

- GIVEN the Safe Browsing API is unreachable (timeout 2s)
- WHEN a URL lookup is attempted
- THEN the Engine fails-closed: blocks the yt-dlp fetch
- AND returns a `PermissionDenied` error with detail "URL safety check unavailable"

#### Scenario: Safe Browsing disabled

- GIVEN `SAFEBROWSING_ENABLED=false`
- WHEN any URL is received for yt-dlp processing
- THEN the safety check is bypassed
- AND a `slog.Warn` is emitted noting safety is disabled

### Requirement: Single-Pass JPEG Re-encode from Pixels

The system MUST re-encode JPEG output from decoded RGB24 pixel data (not from the original input bytes), eliminating any polyglot payload in a single FFmpeg pass: decode → hash pixels → re-encode from pixels.

#### Scenario: Polyglot JPEG stripped

- GIVEN a JPEG file with trailing ZIP archive appended
- WHEN the normalizer decodes it to RGB24 pixels and re-encodes from pixels
- THEN the output is a clean JPEG with only image data
- AND the trailing ZIP data is irreversibly removed
