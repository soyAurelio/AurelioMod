# Design: Engine Hardening

## Technical Approach

3 chained PRs hardening the Engine from MVP to production-ready. PR#1: MIME validation + GOMAXPROCS + pprof + CI infrastructure. PR#2: nsjail sandboxing + Safe Browsing. PR#3: testcontainers + JPEG anti-polyglot.

MIME validation executes in the ConnectRPC handler BEFORE `pipeline.Normalize()` — fast-reject mismatches at the edge. Sandboxing wraps FFmpeg/yt-dlp exec calls behind interfaces, allowing drop-in swap from `os/exec` to nsjail. Safe Browsing caches lookups in DragonflyDB (TTL 15min) to avoid repeated API calls.

## Architecture Decisions

| Decision | Options | Choice | Rationale |
|----------|---------|--------|-----------|
| nsjail vs firejail | nsjail, firejail | nsjail | Static binary (musl-compilable), compatible with distroless base. Firejail needs shared libs absent from distroless. |
| pprof port | Same port, separate port | Separate :6060 | Don't expose debug endpoints on public mux. Internal-only port, independent lifecycle. |
| TestMain pattern | Per-package, shared testutil | Per-package with `sync.Once` helpers | Each package owns its container lifecycle. `internal/testutil/` provides reusable `StartDragonfly`/`StartNATS`/`StartWeaviate` helpers gated by `sync.Once`. |
| MIME gate name | `MIME_STRICT_VALIDATION`, `ENFORCE_MIME` | `ENFORCE_MIME` | Matches spec contract. Shorter, consistent with `SAFEBROWSING_ENABLED` pattern. |
| JPEG anti-polyglot | Single-invoke filter-complex, two-invoke | Two-invoke (decode→pixels, pixels→JPEG) | `extractPixels` already produces raw RGB24. Re-encoding from decoded pixels (not input bytes) strips polyglots. Filter-complex adds complexity without benefit. |
| Safe Browsing cache | None, DragonflyDB | DragonflyDB, 15min TTL | Same backend as L1/L2. `go-redis` already a dependency. `SETEX` auto-expires entries. |
| YtDlp deployment | Engine container, sidecar | Sidecar container | yt-dlp adds ~100MB Python. Sidecar avoids image bloat, separates lifecycle. |

## Data Flow

```
Handler.Analyze()
  │
  ├─→ hasher.ValidateContentType(rawBytes, contentType)  ← MIME gate
  │     └─ (reject if mismatch + ENFORCE_MIME=true)
  │
  └─→ pipeline.Execute()
        │
        └─→ normalizer.Normalize()
              │
              ├─→ media.FFmpegRunner.Run(args, input)  ← nsjail sandbox
              │     ├─ decode → raw RGB24 pixels
              │     └─ pixels → JPEG Q85 (anti-polyglot)
              │
              └─→ (if URL source)
                    ├─→ safety.URLReputation.CheckURL(url)
                    │     ├─ DragonflyDB cache hit? → return
                    │     └─ Safe Browsing API → cache (TTL 15m)
                    └─→ media.YtDlpRunner.Fetch(url)

pprof mux (:6060)
  └─→ PASETO middleware → http.DefaultServeMux (/debug/pprof/)
```

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `engine/hasher/mime.go` | Create | `ValidateContentType()` + extracted `detectMIME()` with magic-byte signatures |
| `engine/hasher/normalizer.go` | Modify | Remove `detectMIME()` (→ mime.go), inject `FFmpegRunner`, re-encode JPEG from decoded pixels |
| `engine/media/ffmpeg.go` | Create | `FFmpegRunner` interface, `NsJailFFmpeg` (nsjail: `--net none`, `/tmp` rw, media ro, wall-time limit) |
| `engine/media/ytdlp.go` | Create | `YtDlpRunner` interface, `NsJailYtDlp` (redirect limit 5, HTTP to sidecar) |
| `engine/media/media_test.go` | Create | Sandbox integration tests (build tag: integration) |
| `engine/safety/safety.go` | Create | `URLReputationService` interface, `SafeBrowsingService` + DragonflyDB `SETEX` cache |
| `engine/safety/safety_test.go` | Create | Safety service unit tests with mock HTTP transport |
| `cmd/engine/main.go` | Modify | `init()`: GOMAXPROCS, pprof server goroutine, safety client wiring |
| `cmd/engine/pprof.go` | Create | pprof mux on :6060 with PASETO `Authorization: Bearer` middleware |
| `cmd/engine/pprof_test.go` | Create | PASETO auth tests: valid/missing/expired token scenarios |
| `engine/service/handler.go` | Modify | MIME validation call before `pipeline.Execute()`, gated by `ENFORCE_MIME` |
| `internal/testutil/containers.go` | Create | `sync.Once` helpers: `StartDragonfly`, `StartNATS`, `StartWeaviate` |
| `internal/cache/integration_test.go` | Modify | Replace manual DragonflyDB check with `testutil.StartDragonfly()` |
| `engine/pipeline/pipeline_test.go` | Modify | Add testcontainers `TestMain` for NATS/Weaviate (build tag: integration) |
| `deployments/Dockerfile.engine` | Modify | Add nsjail static build stage (`musl-gcc` + `make`) |
| `compose.yml` | Modify | Add yt-dlp sidecar service (image: `jauderho/yt-dlp:latest`) |
| `.woodpecker.yml` | Create | 5-stage CI: lint → test(-race) → security(govulncheck) → sbom(syft) → build |
| `.golangci.yml` | Create | govet, staticcheck, errcheck, gosec, goimports, ineffassign, misspell |
| `go.mod` | Modify | Add `testcontainers-go`, `google/safebrowsing` |

## Interfaces / Contracts

```go
// engine/media/ffmpeg.go
type FFmpegRunner interface {
    Run(ctx context.Context, args []string, stdin []byte) ([]byte, error)
}

// engine/media/ytdlp.go
type YtDlpRunner interface {
    Fetch(ctx context.Context, url string) ([]byte, error)
}

// engine/safety/safety.go
type URLReputationService interface {
    CheckURL(ctx context.Context, url string) error // nil = safe
}
```

## Feature Gates

| Env Var | Default | Behavior |
|---------|---------|----------|
| `ENFORCE_MIME` | `false` | Enable MIME validation rejection |
| `MEDIA_SANDBOX_ENABLED` | `true` | Use nsjail (disable → `os/exec`) |
| `SAFEBROWSING_ENABLED` | `true` | Query Safe Browsing API |
| `PPROF_ADMIN_KEY` | required | Hex-encoded PASETO Ed25519 secret key |

## Testing Strategy

| Layer | What | Approach |
|-------|------|----------|
| Unit | MIME validation, PASETO middleware, Safe Browsing client | Table-driven, mock interfaces (`FFmpegRunner`, `YtDlpRunner`, `URLReputationService`) |
| Integration | nsjail FFmpeg, testcontainers (DragonflyDB/NATS/Weaviate) | Build tag `integration`, `t.Short()` skip, `sync.Once` container start |
| CI | Pipeline stages | Woodpecker: lint → test(-race) → vulncheck → sbom → build |

## Migration / Rollout

All features are gated behind env vars (default off for MIME, default on for sandbox/Safe Browsing with `os/exec` fallback). Rollback is removing env vars or setting to `false`. No data migration required. nsjail binary is added via Dockerfile builder stage — existing images work unchanged (fallback to `os/exec` when nsjail not found).

## Open Questions

None. All design decisions resolved against existing codebase patterns and spec requirements.
