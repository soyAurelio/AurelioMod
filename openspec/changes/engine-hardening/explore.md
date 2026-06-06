## Exploration: engine-hardening

### Current State

The Engine service is functionally complete for the L1→L2→L3→WaveSpeed pipeline. All cache layers are implemented, ConnectRPC handler validates inputs, audit logging + NATS publishing + quarantine hooks are wired. However, **4 of 8 hardening items are missing entirely** (sandboxing, URL safety, GOMAXPROCS, pprof), **2 are partially implemented** (MIME validation has the detection function but no rejection logic, Woodpecker CI has no config file), and **2 are implemented but sub-optimally** (JPEG re-encoding exists but via a separate pass instead of on decoded pixels, testcontainers are absent — tests use miniredis mocks or manual DragonflyDB ping).

---

### Per-Item Analysis

#### 1. MIME Validation + Magic Bytes
**Status**: PARTIALLY IMPLEMENTED  
**Current state**: `engine/hasher/normalizer.go:125-167` contains `detectMIME()` which inspects magic bytes for JPEG, PNG, GIF, WebP, MP4, and ZIP (polyglot). The result is saved in `NormalizeResult.MIMEType` and used in `pipeline.go:226-229` as a parameter to WaveSpeed API.  
**Gap**: There is NO rejection mechanism. If someone sends an `.exe` renamed to `.mp4`, `detectMIME()` returns `"application/octet-stream"` but the pipeline still attempts FFmpeg normalization. The task asks for: "Validate that Content-Type matches actual file data" with rejection BEFORE normalization.  
**Files affected**: `engine/hasher/normalizer.go` (add `ValidateContentType`), `engine/service/handler.go` (call validation early), `engine/pipeline/pipeline.go` (or trigger from handler)

#### 2. Clean JPEG Re-encoding (Anti-Polyglot)
**Status**: IMPLEMENTED (needs verification)  
**Current state**: `engine/hasher/normalizer.go:88-103` `encodeJPEG()` runs a separate FFmpeg pass producing clean JPEG Q85 (`-q:v 3`). Metadata is stripped (`-map_metadata -1`). This effectively strips polyglot data because FFmpeg's decoder only reads valid image data from the input container.  
**Gap**: The task wants re-encoding ON THE DECODED PIXELS, not as a second pass on raw input. Currently `encodeJPEG()` takes the original `input` bytes, not the decoded `rgbPixels`. However, in practice FFmpeg's JPEG decoder already ignores trailing data, so the current approach IS anti-polyglot. The improvement would be: decode once → hash pixels → re-encode FROM pixels (single decode, cleaner).  
**Files affected**: `engine/hasher/normalizer.go` (change `encodeJPEG` to accept pixels)

#### 3. Sandboxed FFmpeg/yt-dlp (engine/media/)
**Status**: NOT IMPLEMENTED  
**Current state**: `engine/media/` directory is EMPTY. FFmpeg is called via `os/exec` in `engine/hasher/normalizer.go:runFFmpegPipe()`. No sandboxing. No yt-dlp code exists. The stack rules (`aureliomod-stack.md:343`) say "Sandboxing: yt-dlp y FFmpeg corren en firejail/nsjail". `compose.yml` does not include firejail/nsjail images. `Dockerfile.engine` copies FFmpeg binary but no sandbox tool.  
**Files to create**: `engine/media/ffmpeg.go`, `engine/media/ytdlp.go`, `engine/media/media_test.go`  
**Files to modify**: `engine/hasher/normalizer.go` (use sandboxed FFmpeg), `deployments/Dockerfile.engine` (install nsjail/firejail), `compose.yml` (optionally add yt-dlp service)

#### 4. Google Safe Browsing URL Reputation
**Status**: NOT IMPLEMENTED  
**Current state**: Zero URL handling code. No package references Google Safe Browsing API. The stack rules (`aureliomod-stack.md:56`) mention "Google Safe Browsing API — Verificación pre-yt-dlp". No `engine/safety/` package exists.  
**Files to create**: `engine/safety/safety.go`, `engine/safety/safety_test.go`  
**Files to modify**: `cmd/engine/main.go` (wire safety client), `engine/media/ytdlp.go` (pre-flight check before yt-dlp call)

#### 5. GOMAXPROCS = N-1
**Status**: NOT IMPLEMENTED  
**Current state**: `cmd/engine/main.go:76-143` does NOT call `runtime.GOMAXPROCS()`. The stack rules (`aureliomod-stack.md:142`) explicitly state "GOMAXPROCS = N-1 en el Engine (reservar 1 core para FFmpeg subprocess)". Go 1.25+ auto-detects GOMAXPROCS from cgroups, but does NOT subtract 1 for FFmpeg.  
**Files affected**: `cmd/engine/main.go` (add `runtime.GOMAXPROCS()` early in `main()`)

#### 6. pprof with Admin Auth
**Status**: NOT IMPLEMENTED  
**Current state**: `cmd/engine/main.go` creates an `http.Server` with `http.NewServeMux()` but has no pprof endpoint. The stack rules (`aureliomod-stack.md:143`) say "pprof endpoint protegido con auth admin siempre habilitado".  
**Files affected**: `cmd/engine/main.go` (add pprof mux + auth middleware on separate port/path)

#### 7. Testcontainers (Suite-Level)
**Status**: NOT IMPLEMENTED  
**Current state**: Zero usage of `testcontainers-go`. Current test strategies:
- `internal/cache/dragonfly_test.go`: Uses `miniredis` (in-process mock) for unit tests
- `internal/cache/integration_test.go`: Has `TestMain` with `//go:build integration` tag, pings real DragonflyDB, skips if unavailable. No container lifecycle.
- `engine/pipeline/pipeline_test.go`: Uses mock interfaces everywhere — no real DragonflyDB/NATS/Weaviate needed.
- All other test files: Pure unit tests with mocks.

The stack rules (`aureliomod-stack.md:266-279`) show the exact pattern desired: `TestMain` with one container per package, shared across tests.  
**`go.mod`**: Does NOT include `testcontainers-go` dependency.  
**Files affected**: `internal/cache/integration_test.go` (replace manual ping with testcontainers), new TestMain files in: `engine/pipeline/`, `engine/service/`, `engine/analyzer/`  
**Risk**: Requires Docker socket access in CI. Woodpecker CI must support Docker-in-Docker or Docker socket mounting.

#### 8. Woodpecker CI Pipeline
**Status**: NOT IMPLEMENTED  
**Current state**: No `.woodpecker.yml` exists. No `.woodpecker/` directory exists (despite being referenced in `aureliomod-stack.md:134`). The stack rules (`aureliomod-stack.md:375-415`) provide a complete reference pipeline: lint (golangci-lint) → test (go test -race) → security (govulncheck) → sbom (syft) → build (CGO_ENABLED=0) → deploy (kamal, main only).  
**Files to create**: `.woodpecker.yml`  
**Also missing**: `.golangci.yml` — golangci-lint config not present; `Makefile` — no makefile

---

### Additional Discoveries

| Finding | Severity | Detail |
|---------|----------|--------|
| `time.Sleep` in tests | Medium | `pipeline_test.go` uses `time.Sleep(50ms)` for hook assertions (lines 695, 728, 761, 794, 828). Should use `synctest` or `Wait()` method. |
| `errors.As` anti-pattern | Low | `handler_test.go:94,121,147` uses `errors.As(err, &connectErr)`. Should use `errors.AsType[*connect.Error](err)` per Go 1.26+. |
| `wg.Add/Done` instead of `wg.Go` | Low | `pipeline.go:352-376` `fireHooks()` uses manual `wg.Add(1)`/`wg.Done()`. Could migrate to `wg.Go()` (Go 1.25+). However, this captures closure correctly in Go 1.22+ loops. |
| No `.golangci.yml` | Medium | golangci-lint is referenced in CI pipeline but no config exists. |
| `NormalizeResult.MIMEType` field exists but unused for validation | Low | MIME type is detected and passed to WaveSpeed, but never compared against declared Content-Type. |

---

### Affected Areas

| Area | 1:MIME | 2:JPEG | 3:Sandbox | 4:URL | 5:GOMAX | 6:pprof | 7:TC | 8:CI |
|------|--------|--------|-----------|-------|---------|---------|------|------|
| `engine/hasher/normalizer.go` | ✅ | ✅ | ✅ | — | — | — | — | — |
| `engine/hasher/hasher_test.go` | ✅ | — | — | — | — | — | — | — |
| `engine/pipeline/pipeline.go` | ✅ | — | — | — | — | — | — | — |
| `engine/service/handler.go` | ✅ | — | — | — | — | — | — | — |
| `engine/service/handler_test.go` | ✅ | — | — | — | — | — | — | — |
| `engine/media/` (new) | — | — | ✅ | ✅ | — | — | ✅ | — |
| `engine/safety/` (new) | — | — | — | ✅ | — | — | ✅ | — |
| `cmd/engine/main.go` | — | — | — | ✅ | ✅ | ✅ | — | — |
| `deployments/Dockerfile.engine` | — | — | ✅ | — | — | — | — | — |
| `compose.yml` | — | — | ✅ | — | — | — | ✅ | — |
| `internal/cache/integration_test.go` | — | — | — | — | — | — | ✅ | — |
| `engine/pipeline/pipeline_test.go` | — | — | — | — | — | — | ✅ | — |
| `.woodpecker.yml` (new) | — | — | — | — | — | — | — | ✅ |
| `.golangci.yml` (new) | — | — | — | — | — | — | — | ✅ |
| `go.mod` | — | — | — | — | — | — | ✅ | — |

---

### Approaches

#### Implementation Order

**Recommended**: Three PRs (chained) ordered by dependency and risk:

**PR #1 — Security Foundation (low risk, high impact)**
1. **GOMAXPROCS = N-1** (simple, one-line change in main.go)
2. **pprof with admin auth** (separate port, internal-only, PASETO-protected)
3. **Woodpecker CI + golangci-lint config** (no code changes, pure config)
4. **MIME validation enforcement** (reject mismatched content types in handler)

**PR #2 — Sandboxing + URL Safety (medium risk, new packages)**
5. **Sandboxed FFmpeg in engine/media/** (nsjail wrapper, replace os/exec in normalizer)
6. **Sandboxed yt-dlp in engine/media/** (nsjail wrapper, redirect depth limits)
7. **Google Safe Browsing URL reputation** (engine/safety/ package, pre-yt-dlp check)
8. **Dockerfile.engine + compose.yml updates** (install nsjail/firejail)

**PR #3 — Test Hardening + Anti-Polyglot (medium risk, test infrastructure)**
9. **Testcontainers for DragonflyDB** (replace manual ping in integration tests)
10. **Testcontainers for NATS** (suite-level TestMain)
11. **Testcontainers for Weaviate** (suite-level TestMain)
12. **Clean JPEG re-encode from decoded pixels** (single-pass decode → hash → re-encode)
13. **Fix time.Sleep → synctest** in pipeline tests

---

### Recommendation

**Start with PR #1** — it has the lowest risk, fixes the most critical gaps (no CI, no pprof, no MIME validation), and establishes the infra foundation. Items 5 & 6 are 1-2 line changes. Items 8 & 4 are config + ~50 lines of validation logic.

**PR #2** is the heavy lift — nsjail/firejail integration requires careful testing with real FFmpeg/yt-dlp binaries. The Google Safe Browsing API client is straightforward (~150 lines).

**PR #3** requires testcontainers-go dependency addition and Docker-in-Docker CI support. The anti-polyglot re-encode improvement and synctest migration are cleanup items.

---

### Risks

1. **nsjail/firejail availability**: Distroless base image (`gcr.io/distroless/static-debian12:nonroot`) has no package manager. Installing nsjail requires switching to a non-distroless base or copying the binary. **Mitigation**: Use `debian:bookworm-slim` as base for the sandbox stage, or pre-compile nsjail in a builder stage.

2. **yt-dlp in container**: yt-dlp requires Python runtime (~100MB). Including it in the Engine image bloats the container significantly. **Mitigation**: Run yt-dlp as a separate sidecar container in compose.yml (recommended by the SentinelStream v3 doc — "media retrieval service").

3. **Testcontainers in Woodpecker CI**: Requires Docker socket access or Docker-in-Docker. If Woodpecker agents don't have Docker, tests will skip. **Mitigation**: Configure Woodpecker agent with Docker socket mount (`/var/run/docker.sock`).

4. **MIME validation may reject valid content**: Some content providers serve correct images with non-standard Content-Type headers. Too-strict validation could reject valid JPEGs served as `application/octet-stream`. **Mitigation**: Only reject when magic bytes clearly contradict the declared type (e.g., `Content-Type: image/jpeg` but bytes start with `MZ` for PE executable). Accept when magic bytes match OR when Content-Type is generic.

5. **GOMAXPROCS = N-1 on single-core**: If the container has only 1 vCPU, `N-1 = 0` would set GOMAXPROCS to 0 (unlimited). **Mitigation**: Clamp to `max(1, N-1)`.

6. **JPEG re-encode from pixels doubles FFmpeg calls**: Currently normalizer runs two FFmpeg passes. Re-encoding from decoded pixels would require piping RGB24 back into FFmpeg as rawvideo input, adding complexity. The current two-pass approach already strips polyglots effectively. **Mitigation**: Keep current approach but document why it's safe, or switch to a single-pass with output splitting.

7. **No golangci-lint version pinned**: Adding `.golangci.yml` without version pinning can cause CI failures when linter updates. **Mitigation**: Pin `golangci/golangci-lint:v2.2` in `.woodpecker.yml`.

---

### Ready for Proposal

**Yes**. All 8 items are well-understood with clear file locations and implementation approaches. The recommended 3-PR strategy separates concerns and allows incremental hardening. The exploration is complete and ready for `sdd-propose`.
