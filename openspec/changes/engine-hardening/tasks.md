# Tasks: Engine Hardening

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | ~900 (200+400+300) |
| 400-line budget risk | High |
| Chained PRs recommended | Yes |
| Suggested split | PR #1 → PR #2 → PR #3 |
| Delivery strategy | auto-chain |
| Chain strategy | feature-branch-chain |

Decision needed before apply: No
Chained PRs recommended: Yes
Chain strategy: feature-branch-chain
400-line budget risk: High

### Suggested Work Units

| Unit | Goal | Likely PR | Base | Notes |
|------|------|-----------|------|-------|
| 1 | GOMAXPROCS + pprof + MIME gate + CI | PR #1 | feature/engine-hardening | ~200 lines |
| 2 | nsjail sandbox + Safe Browsing + sidecar | PR #2 | PR #1 branch | ~400 lines |
| 3 | Testcontainers + JPEG anti-polyglot | PR #3 | PR #2 branch | ~300 lines |

## Phase 1: Security Foundation (PR #1 ~200 lines)

- [x] **T-001** `cmd/engine/main.go` — `init()`: `GOMAXPROCS=max(1,NumCPU-1)` + `cmd/engine/main_test.go` TDD: `TestGOMAXPROCS_Clamped` (1 vCPU→1), `TestGOMAXPROCS_ReservesCore` (4→3). S, none.

- [x] **T-002** `cmd/engine/pprof.go` — pprof mux `:6060` with PASETO `Bearer` middleware + `cmd/engine/pprof_test.go` TDD: valid token→200, missing→401, expired→401 (spec prod-readiness R2). M, paseto.go (exists).

- [x] **T-003** `engine/hasher/mime.go` — extract `detectMIME()` from `normalizer.go`, add `ValidateContentType(raw, contentType)`. `engine/service/handler.go` — call before `pipeline.Execute()`, gate via `ENFORCE_MIME`. TDD: JPEG+JPEG→accept, JPEG+octet-stream→accept+warn, PE+JPEG→reject, JPEG+MP4→reject, empty→reject (spec mime-validation R1/R2). M, T-001.

- [x] **T-004** `.woodpecker.yml` (5-stage: lint→test-race→govulncheck→syft→build) + `.golangci.yml` (govet, staticcheck, errcheck, gosec, goimports, ineffassign, misspell, go=1.26, timeout=5m). S, none.

- [x] **T-005** `go.mod` — add `testcontainers-go`, `google/safebrowsing`; `go mod tidy`. S, none (prep for PR#2/#3).

## Phase 2: Sandboxing + URL Safety (PR #2 ~400 lines)

- [ ] **T-006** `engine/media/ffmpeg.go` — `FFmpegRunner` interface + `NsJailFFmpeg{Run(ctx,args,stdin)}` with `--net none`, `/tmp` rw, media ro. Gate: `MEDIA_SANDBOX_ENABLED`. TDD: mock runner table-driven tests. M, T-003.

- [ ] **T-007** `engine/media/ytdlp.go` — `YtDlpRunner` interface + `NsJailYtDlp{Fetch(ctx,url)}` redirect limit 5, HTTP to sidecar. TDD: mock HTTP transport. M, T-006.

- [ ] **T-008** `engine/safety/safety.go` — `URLReputationService` interface + `SafeBrowsingService` with DragonflyDB `SETEX` cache (TTL 15m) + `engine/safety/safety_test.go` TDD: clean→pass, malware→block, API timeout→fail-closed, disabled→bypass (spec media-sandbox R3). M, T-005.

- [ ] **T-009** `engine/hasher/normalizer.go` — inject `FFmpegRunner` into `Normalizer` struct, swap `os/exec` for `runner.Run()`. `cmd/engine/main.go` — wire `NsJailFFmpeg` + `SafeBrowsingService`. TDD: update tests for interface injection. M, T-006, T-008.

- [ ] **T-010** `deployments/Dockerfile.engine` — nsjail static build stage (musl-gcc+make). `compose.yml` — yt-dlp sidecar (`jauderho/yt-dlp:latest`). S, T-006, T-007.

## Phase 3: Test Hardening + Anti-Polyglot (PR #3 ~300 lines)

- [ ] **T-011** `internal/testutil/containers.go` — `sync.Once` helpers: `StartDragonfly`, `StartNATS`, `StartWeaviate` via testcontainers-go. Self-test verifies container startup. M, T-005.

- [ ] **T-012** `internal/cache/integration_test.go` — replace manual ping with `testutil.StartDragonfly()`. TDD: existing tests pass with testcontainers, `-short` skips. S, T-011.

- [ ] **T-013** `engine/pipeline/pipeline_test.go` — `TestMain` using `testutil.StartNATS`/`StartWeaviate`, synctest deadline scenarios. TDD: RED→GREEN flow. M, T-011.

- [ ] **T-014** `engine/hasher/normalizer.go` — change `encodeJPEG` to re-encode from decoded RGB24 pixels (not raw input). TRIANGULATE: JPEG+ZIP polyglot→clean JPEG output (spec media-sandbox R4). S, T-009.

- [ ] **T-015** `engine/media/media_test.go` — sandbox integration tests (build tag `integration`): deadline kill, network block, write denial + `engine/safety/safety_test.go` — cache TTL, DragonflyDB fallback. TDD: spec media-sandbox R1/R4 scenarios. M, T-006, T-008, T-011.
