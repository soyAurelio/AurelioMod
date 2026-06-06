# Proposal: Engine Hardening

## Intent

Bridge the Engine service from functionally-complete to production-ready. Eight hardening items per `aureliomod-stack.md` and SentinelStream v3: 4 missing entirely (sandboxing, URL safety, GOMAXPROCS, pprof), 2 partially implemented (MIME validation, CI), 2 sub-optimal (JPEG anti-polyglot, testcontainers).

## Scope

### In Scope
- MIME magic-byte validation with **rejection** (not just detection)
- Clean JPEG re-encode from decoded pixels (single-pass anti-polyglot)
- nsjail-sandboxed FFmpeg in `engine/media/`; yt-dlp sidecar
- Google Safe Browsing URL reputation pre-yt-dlp check (`engine/safety/`)
- `GOMAXPROCS = max(1, N-1)` reserving 1 core for FFmpeg
- pprof endpoint on separate port with PASETO admin auth
- Testcontainers suite (`TestMain`) for DragonflyDB, NATS, Weaviate
- `.woodpecker.yml` CI pipeline + `.golangci.yml`

### Out of Scope
- Distroless → debian base image migration (separate infra decision)
- yt-dlp integration beyond sandbox wrapper (sidecar in compose only)
- Production Kamal deploy config
- Refactoring existing handler tests (CI + synctest migration only)

## Capabilities

### New Capabilities
- **mime-validation**: Reject uploads where Content-Type contradicts magic bytes. Only allow through when bytes match OR Content-Type is generic (`application/octet-stream`).
- **media-sandbox**: nsjail-sandboxed FFmpeg/yt-dlp execution — no network, no write outside tmp, read-only media access, time limit enforced.
- **url-reputation**: Google Safe Browsing v4 lookup pre-yt-dlp — block known malicious URLs before any fetch.

### Modified Capabilities
None (no existing specs in `openspec/specs/`).

## Approach

**3 chained PRs** ordered by dependency and risk:

| PR | Items | Risk | Lines (est.) |
|----|-------|------|--------------|
| #1 — Security Foundation | GOMAXPROCS, pprof, Woodpecker CI + golangci-lint, MIME validation | Low | ~200 |
| #2 — Sandboxing + URL Safety | nsjail FFmpeg, yt-dlp sidecar, Google Safe Browsing, Dockerfile/compose | Medium | ~400 |
| #3 — Tests + Anti-Polyglot | Testcontainers (DragonflyDB, NATS, Weaviate), JPEG pixel re-encode, synctest | Medium | ~300 |

PR #1 establishes infra foundation. PR #2 depends on #1 (Dockerfile changes). PR #3 depends on #2 (testcontainers need Docker in CI from #1).

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| `engine/hasher/normalizer.go` | Modified | MIME rejection, JPEG pixel re-encode, sandboxed FFmpeg |
| `engine/hasher/hasher_test.go` | Modified | MIME rejection test cases |
| `engine/service/handler.go` | Modified | MIME validation call before pipeline |
| `engine/media/ffmpeg.go` | New | nsjail-wrapped FFmpeg execution |
| `engine/media/ytdlp.go` | New | nsjail-wrapped yt-dlp sidecar call |
| `engine/media/media_test.go` | New | Sandbox integration tests |
| `engine/safety/safety.go` | New | Safe Browsing v4 client |
| `engine/safety/safety_test.go` | New | Safe Browsing unit tests |
| `cmd/engine/main.go` | Modified | GOMAXPROCS, pprof server, safety client wiring |
| `deployments/Dockerfile.engine` | Modified | nsjail/firejail installation |
| `compose.yml` | Modified | yt-dlp sidecar, testcontainers config |
| `internal/cache/integration_test.go` | Modified | Testcontainers DragonflyDB |
| `engine/pipeline/pipeline_test.go` | Modified | testcontainers NATS/Weaviate, synctest |
| `.woodpecker.yml` | New | CI pipeline: lint, test, security, sbom, build |
| `.golangci.yml` | New | Linter config |
| `go.mod` | Modified | Add `testcontainers-go`, `google/safebrowsing` |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| N-1 = 0 on single-core container | Low | Clamp to `max(1, N-1)` |
| MIME validation rejects valid content with non-standard Content-Type | Medium | Accept when type is generic (`application/octet-stream`) |
| nsjail/firejail not in distroless base | High | Builder stage copies static nsjail binary; fallback to firejail from apt |
| yt-dlp bloats Engine image (~100MB Python) | Medium | Run as sidecar container, not in Engine image |
| testcontainers need Docker socket in Woodpecker | Medium | Mount `/var/run/docker.sock` on CI agent; skip with `t.Short()` guard |
| JPEG pixel re-encode doubles FFmpeg calls | Low | Single-pass: decode → hash RGB → encode pixels (one FFmpeg call) |

## Rollback Plan

- **GOMAXPROCS/pprof**: Remove 3-4 lines from `main.go`, redeploy. Immediate.
- **MIME validation**: Feature-gate behind env var `ENFORCE_MIME=true`, default false initially.
- **Sandboxing**: Revert `normalizer.go` to use `os/exec` directly; nsjail is a wrapper — drop-in swap.
- **Testcontainers/CI**: Revert `.woodpecker.yml` and test files; no runtime impact.
- **Safe Browsing**: Disable via env var `SAFEBROWSING_ENABLED=false`.

## Dependencies

- `testcontainers-go` module (new dependency)
- `google/safebrowsing` Go client (new dependency)
- nsjail binary in builder stage or apt-available base
- Docker socket access for Woodpecker agent (CI infra)

## Success Criteria

- [ ] MIME mismatch uploads rejected with gRPC `InvalidArgument` before FFmpeg spawns
- [ ] No FFmpeg process escapes nsjail sandbox (verified by e2e test)
- [ ] Malicious URLs blocked before yt-dlp fetch (Safe Browsing integration test)
- [ ] `GOMAXPROCS` ≤ `runtime.NumCPU()-1` (verified in `main_test.go`)
- [ ] pprof accessible only with valid PASETO token on admin port
- [ ] CI pipeline passes on every push: lint → test (-race) → vulncheck → sbom → build
- [ ] Integration tests run real DragonflyDB/NATS/Weaviate containers in CI
- [ ] All existing tests pass unchanged (no regression)

## Estimated Effort

**Total**: L (Large — ~900 lines across 17 files, 3 PRs)

| PR | Effort |
|----|--------|
| #1 — Security Foundation | M (2-3 days) |
| #2 — Sandboxing + URL Safety | L (3-5 days) |
| #3 — Tests + Anti-Polyglot | M (2-3 days) |
