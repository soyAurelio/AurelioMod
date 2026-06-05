# AurelioMod — Skill Registry

> Generated: 2026-06-05 by sdd-init
> Scope: Go 1.26.4 backend monorepo — ConnectRPC, NATS, DragonflyDB, Weaviate, Centrifugo
> Conventions: `~/.config/opencode/skills/`, `.agent/rules/aureliomod-stack.md`

---

## Backend / Go Skills (HIGH relevance)

### go
- **Trigger**: Writing Go code, reviewing Go patterns, upgrading Go versions.
- **Path**: `~/.config/opencode/skills/go/SKILL.md`
- **Key Rules**:
  - Use `new(expr)` for inline pointer creation (Go 1.26+), NEVER `x := val; &x`.
  - Use `errors.AsType[T](err)` (Go 1.26+) instead of `errors.As` with pointer setup.
  - Use `wg.Go()` (Go 1.25+) for goroutine groups; NEVER manual `wg.Add(1)/wg.Done()`.
  - Use `t.Context()` for test contexts (Go 1.24+), cancelled automatically on test finish.
  - Use `sync.Pool` for byte buffers ≥32KB in streaming operations (GOMAXPROCS=N-1 for Engine).
  - Always propagate `context.Context` across gRPC/NATS boundaries.
  - NEVER use `time.Sleep` in concurrent tests — use `testing/synctest`.
  - Use `log/slog` for structured logging (JSON in production), NOT `log` or `fmt.Println`.
  - Iterators: `maps.Keys/Values`, `slices.Collect/Sorted` (Go 1.23+) for lazy evaluation.
  - `weak` pointers for memory-efficient caches (Go 1.24+).
  - `Swiss Tables` for maps are automatic in Go 1.26 — no config needed.

### go-testing
- **Trigger**: Go tests, go test coverage, golden files.
- **Path**: `~/.config/opencode/skills/go-testing/SKILL.md`
- **Key Rules**:
  - Table-driven tests with `t.Run(tt.name, ...)` — name cases by scenario, not input mechanics.
  - Use `t.Context()` for ALL test contexts (Go 1.24+), NOT `context.Background()`.
  - Use `t.TempDir()` for filesystem tests; NEVER real home directory.
  - Integration tests MUST be skippable with `testing.Short()`.
  - Golden files: deterministic, update via `-update` flag, verify without.
  - Test behavior and state transitions, NOT implementation details.
  - Use synctest for deterministic time testing — NO `time.Sleep` in tests.

### dragonfly
- **Trigger**: Integrating DragonflyDB in Go, cache TTL, streams as event bus, rate limiting, vector search.
- **Path**: `~/.config/opencode/skills/dragonfly/SKILL.md`
- **Key Rules**:
  - Use go-redis v9 with RESP2 protocol for DIALECT 2 vector search compatibility.
  - Pool size: 100 connections per Edge instance; 200 for Engine.
  - Cache mode: `--cache_mode=true` with `--maxmemory=4gb`.
  - BLAKE3 hashes as cache keys (L1), pHash for L2 (Hamming distance ≤5).
  - Streams: use XADD/XREADGROUP/XAUTOCLAIM for the Outbox pattern.
  - Rate limiting: Lua scripts with `{}` hashtags for multi-threaded atomic operations.
  - Per-field TTL: HTTL/HEXPIRE (v1.38+) on Hash fields.
  - Vector search: VECTOR_RANGE with DIALECT 2 (v1.38+); use FT.AGGREGATE for grouping.
  - CMS (Count-Min Sketch) and Top-K for probabilistic heavy-hitter tracking.
  - NEVER use `KEYS *` in production — use SCAN with cursor.

### api-rest
- **Trigger**: Designing REST APIs, creating endpoints, errors, pagination, auth, versioning.
- **Path**: `~/.config/opencode/skills/api-rest/SKILL.md`
- **Key Rules**:
  - Consistent error envelope: `{ "error": { "code": "...", "message": "...", "details": [...] } }`.
  - Pagination: cursor-based preferred over offset-based; return `next_cursor` and `has_more`.
  - Versioning: URL prefix `/v1/` or header `Accept: application/vnd.api+json;version=1`.
  - Auth: PASETO v4 tokens in `Authorization: Bearer <token>`; NO JWT.
  - Idempotency keys for mutating endpoints (`Idempotency-Key` header).
  - Rate limiting headers: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`.
  - Use Problem Details RFC 9457 for error responses.

---

## Git / PR / Collaboration Skills (MEDIUM relevance)

### branch-pr
- **Trigger**: Creating, opening, or preparing PRs for review.
- **Path**: `~/.config/opencode/skills/branch-pr/SKILL.md`
- **Key Rules**: Issue-first checks, draft PRs for WIP, conventional commit messages, link PR to issues.

### work-unit-commits
- **Trigger**: Implementation, commit splitting, keeping tests and docs with code.
- **Path**: `~/.config/opencode/skills/work-unit-commits/SKILL.md`
- **Key Rules**: Each commit = one reviewable work unit; tests + code in same commit; no mixed concerns.

### chained-pr
- **Trigger**: PRs over 400 lines, stacked PRs, review slices.
- **Path**: `~/.config/opencode/skills/chained-pr/SKILL.md`
- **Key Rules**: Split oversized changes into chained PRs; each PR targets the previous one's branch.

### issue-creation
- **Trigger**: Creating GitHub issues, bug reports, feature requests.
- **Path**: `~/.config/opencode/skills/issue-creation/SKILL.md`
- **Key Rules**: Issue-first checks; structured template; acceptance criteria; labels.

### comment-writer
- **Trigger**: PR feedback, issue replies, reviews, GitHub comments.
- **Path**: `~/.config/opencode/skills/comment-writer/SKILL.md`
- **Key Rules**: Warm, direct collaboration comments; constructive feedback with reasoning.

### judgment-day
- **Trigger**: Dual review, adversarial review.
- **Path**: `~/.config/opencode/skills/judgment-day/SKILL.md`
- **Key Rules**: Blind dual review, fix confirmed issues, re-judge.

---

## Documentation / Quality Skills (LOW relevance)

### cognitive-doc-design
- **Trigger**: Writing guides, READMEs, RFCs, onboarding, architecture docs.
- **Path**: `~/.config/opencode/skills/cognitive-doc-design/SKILL.md`
- **Key Rules**: Reduce cognitive load; progressive disclosure; visual hierarchy in docs.

### full-output-enforcement
- **Trigger**: Tasks requiring exhaustive, unabridged output.
- **Path**: `~/.agents/skills/full-output-enforcement/SKILL.md`
- **Key Rules**: Complete code generation; no placeholder patterns; handle token-limit splits cleanly.

### caveman-commit
- **Trigger**: Writing commit messages.
- **Path**: `~/.agents/skills/caveman-commit/SKILL.md`
- **Key Rules**: Conventional Commits; subject ≤50 chars; body only when "why" isn't obvious.

---

## Convention Files

| File | Purpose |
|------|---------|
| `~/.config/opencode/AGENTS.md` | Global agent rules (engram protocol, persona) |
| `.agent/rules/aureliomod-stack.md` | Stack & architecture rules (Go, ConnectRPC, NATS, DragonflyDB, Weaviate) |

---

## Excluded Skills

- `sdd-*` — SDD workflow phases (orchestrator-managed)
- `_shared` — Shared reference (not a skill)
- `skill-registry` — Registry builder (meta)
- `echo` — Echo v5 (not used in this project — ConnectRPC is the RPC framework)
- `webwright` — Browser automation (not applicable)
- `caveman`, `caveman-help`, `caveman-review`, `caveman-compress` — Ultra-compressed mode variants
- All `~/.agents/skills/` design skills (animate, audit, bolder, colorize, critique, delight, distill, etc.) — Frontend design skills, NOT applicable to Go backend; may become relevant for `control/dashboard` UI later
