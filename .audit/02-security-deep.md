# Security Deep Audit Report — AurelioMod Pentest Readiness

**Date:** 2026-06-11  
**Scope:** Full codebase, compose files, Dockerfiles, schema, configuration  
**Methodology:** Source code review, config inspection, dependency audit  

---

## Overall Security Score: 62/100

> **Verdict:** NOT READY for pentesting. Three critical and six high-severity issues must be resolved. The architecture is sound (PASETO v4, nsjail, failsafe-go, BLAKE3) but implementation gaps exist in the areas of TLS verification, audit coverage, and secrets management.

---

## Critical Vulnerabilities (MUST FIX before pentest)

### C-1: yt-dlp Disables TLS Certificate Verification (MITM)
- **File:** `cmd/ytdlp-sidecar/main.go:43`
- **Severity:** CRITICAL
- **Evidence:**
  ```go
  cmd := exec.CommandContext(ctx, "yt-dlp",
      "--no-progress",
      "--print-json",
      "--no-check-certificates",  // ← DISABLES TLS VERIFICATION
      "--max-redirects", "5",
      rawURL,
  )
  ```
- **Exploit scenario:** An attacker performing MITM between the sidecar and YouTube can inject malicious content. The yt-dlp output JSON is trusted and fed into the analysis pipeline.
- **Fix:** Remove `--no-check-certificates`. If there's a specific cert issue in the sidecar container, install CA certificates from the `jrottenberg/ffmpeg:7.1-ubuntu` base or the builder stage instead of bypassing TLS. The base image already has `ca-certificates` installed (line 41 of Dockerfile.ytdlp-sidecar).

### C-2: Audit Events NOT Fired for Cache Hits — GDPR/NIS2 Compliance Gap
- **File:** `engine/pipeline/pipeline.go:317-330, 498-537`
- **Severity:** CRITICAL
- **Evidence:** `fireHooks()` (which calls `auditHook`) is ONLY called at line 553, which is within the WaveSpeed path. L1 cache hits (line 498), L2 cache hits (line 504-506), and L3 cache hits (line 511) return decisions directly WITHOUT emitting audit events.
  ```go
  // pipeline.go:498 — L1 cache hit → returns immediately, NO audit
  if d, ok := p.l1cache.GetL1(ctx, l1Hash); ok {
      return buildResponse(d, v1.CacheLevel_CACHE_LEVEL_L1_BLAKE3, l1Hash, time.Since(start)), nil
  }
  // No audit event here — gap!
  ```
- **Exploit scenario:** An auditor queries the audit_log and sees only a fraction of decisions. The cached result's origin, timestamp, and confidence are not recorded as immutable events. Under GDPR Art. 22, an impugned cached decision cannot be substantiated because no audit trail exists for it. NIS2 Art. 21 requires full pipeline traceability, which this breaks.
- **Fix:** `fireHooks()` should be called from ALL decision paths — L1, L2, L3 cache hits AND the WaveSpeed path. Add `fireHooks(ctx, req, l1Hash, cachedDecision, elapsed)` before each `buildResponse()` return in `executeStandard()`. Alternatively, elevate audit hook firing into `Execute()` as a post-decision hook that always runs.

### C-3: Centrifugo Default Secrets in Production Config
- **File:** `deployments/centrifugo.json`
- **Severity:** CRITICAL
- **Evidence:**
  ```json
  {
    "allowed_origins": ["*"],        // ← CORS wildcard — any origin
    "insecure": true,                // ← Disables API key checks
    "admin": {
      "password": "dev-admin-change-in-production",
      "secret": "dev-admin-secret-change-in-production",
      "enabled": true,
      "insecure": true               // ← Admin panel without auth
    },
    "token": {
      "hmac_secret_key": "dev-secret-change-in-production"
    },
    "http_api": {
      "key": "dev-api-key-change-in-production"
    }
  }
  ```
- **Exploit scenario:** In production (`compose.prod.yml` does not override or replace centrifugo.json), Centrifugo exposes an unauthenticated admin panel and accepts connections from any origin. This allows arbitrary WebSocket connections and subscription to all channels, including real-time moderation decisions from all workspaces (data leak across tenants). The admin panel allows channel inspection.
- **Fix:** Either (a) mount a separate `centrifugo.prod.json` in `compose.prod.yml`, (b) use environment-based substitution like `CENTRIFUGO_TOKEN_HMAC_SECRET_KEY`, or (c) enforce `allowed_origins` to specific dashboard domains and remove `insecure: true`.

---

## High Severity Issues

### H-1: Plan Quota Enforcement Fails Open on Missing Config
- **File:** `edge/discord/control/client.go:41-43`
- **Severity:** HIGH
- **Evidence:**
  ```go
  func (c *PlanClient) Consume(ctx context.Context, workspaceID string) bool {
      if c.baseURL == "" || c.token == "" {
          return true  // ← Fails open: always allows analysis
      }
  ```
  The `CONTROL_TOKEN` and `CONTROL_URL` are optional env vars in `compose.yml`. If they're not set (or misconfigured), every Discord message bypasses the quota system entirely. An attacker who knows this can consume unlimited WaveSpeed credits.
- **Fix:** Either (a) make both env vars required at startup and `os.Exit(1)` if missing, or (b) default to `false` (fail-closed) rather than `true`. Paired with the quota system, fail-closed is safer for billing. Note that `CONTROL_TOKEN` is a PASETO bearer token stored in `.env` — it has no expiration or rotation mechanism as a static token.

### H-2: Web Risk API Not Called Before yt-dlp Content Fetch
- **File:** `cmd/engine/main.go:198-210` (WebRisk service created) vs `engine/pipeline/pipeline.go:146-157` (EXTERNAL_URL path returns QUEUED)
- **Severity:** HIGH
- **Evidence:** The Web Risk service is initialized and wired into the Engine server (`wrService` at line 198), but it is **never actually called** before any URL content fetch. The pipeline's `Execute()` method returns `DECISION_QUEUED` for all EXTERNAL_URL content (line 146-157), deferring URL safety checks to an unimplemented queue consumer. The variable is annotated `_ = wrService // wired for future URL-sourced content pipeline integration`.
  ```go
  // pipeline.go:146
  if req.ContentType == v1.ContentType_CONTENT_TYPE_EXTERNAL_URL {
      return &v1.AnalyzeResponse{
          Decision: v1.Decision_DECISION_QUEUED,
          BlockReason: "pending_url_fetch",
      }, nil
  }
  // WebRisk never called here!
  ```
- **Risk:** Per the spec (stack.md §7), URL safety checks should run BEFORE content fetch. An attacker can submit known-malicious URLs that bypass Web Risk entirely because the check is deferred. When the queue consumer eventually processes the URL, there's no gating check first.
- **Fix:** Integrate `wrService.CheckURL()` into the EXTERNAL_URL path in `Execute()`. Call it BEFORE the YouTube frame extraction path. If `CheckURL` returns `ErrMaliciousURL`, return `DECISION_BLOCK` immediately. Only proceed to frame extraction/queueing for safe URLs.

### H-3: PASETO Key Rotation Not Wired in Engine
- **File:** `cmd/engine/main.go:242-255`
- **Severity:** HIGH
- **Evidence:** The `paseto.NewRotatable()` and `PASETO_PREVIOUS_KEY` env var exist (declared in `compose.yml:281`) but are never used in `cmd/engine/main.go`. The Engine creates a `TokenManager` via `paseto.NewFromHex(keyHex)` at line 242, which only accepts the current key. The `RotatableTokenManager` is available in `internal/paseto/paseto.go` but is never instantiated in the Engine.
  ```go
  tm, err := paseto.NewFromHex(keyHex)  // Only current key — no rotation
  ```
  Meanwhile, the Control API's `compose.yml:280` declares `PASETO_PREVIOUS_KEY` but `cmd/control/main.go` also only uses `PASETO_SECRET_KEY` via `NewFromHex`.
- **Fix:** Both Engine and Control should use `paseto.NewRotatable(currentHex, previousHex)` where `previousHex` is read from `PASETO_PREVIOUS_KEY`. The RotatableTokenManager already exists and is tested — it just needs to be wired.

### H-4: No Request Body Size Limit on Engine ConnectRPC Handler
- **File:** `cmd/engine/main.go:256-259`, `engine/service/handler.go:46-76`
- **Severity:** HIGH
- **Evidence:** The HTTP server is created with:
  ```go
  return &http.Server{
      Addr:              ":" + cfg.Port,
      Handler:           mux,
      ReadHeaderTimeout: 5 * time.Second,
      IdleTimeout:       120 * time.Second,
      // No ReadTimeout, no MaxHeaderBytes!
  }, nil
  ```
  The handler validates `len(msg.RawBytes) == 0` but there is NO upper bound. An attacker can send a multi-gigabyte request body that passes through to FFmpeg via `normalizer.Normalize(ctx, input)`, causing OOM or disk exhaustion inside the nsjail.
- **Fix:** Add `http.MaxBytesReader` or a middleware at the ConnectRPC handler level. The Analyze endpoint should enforce a reasonable max (e.g., 50 MB for standard content, 500 MB for video). Also add `ReadTimeout: 30 * time.Second` to the HTTP server.

### H-5: OTLP Telemetry Uses Insecure gRPC (No TLS)
- **Files:** `engine/telemetry/telemetry.go:133, 156`
- **Severity:** HIGH
- **Evidence:**
  ```go
  exporter, err := otlptracegrpc.New(ctx,
      otlptracegrpc.WithEndpoint(endpoint),
      otlptracegrpc.WithInsecure(),  // ← NO TLS for traces
  )
  exporter, err := otlpmetricgrpc.New(ctx,
      otlpmetricgrpc.WithEndpoint(endpoint),
      otlpmetricgrpc.WithInsecure(), // ← NO TLS for metrics
  )
  ```
- **Risk:** Traces and metrics are sent in plaintext over the network between Engine and Tempo/VictoriaMetrics. These traces contain request metadata, workspace IDs, content hashes, and potentially sensitive operational data. In a production environment (even single-VPS), an attacker on the same network segment can intercept telemetry data.
- **Fix:** Remove `WithInsecure()` and use TLS for OTLP exporters. In the single-VPS case where Tempo is localhost, this is acceptable as-is because traffic never leaves the loopback interface. But document clearly and add a production env guard: only allow insecure if `OTEL_INSECURE=true` and default to TLS.

### H-6: Engine Control API Auth Token — Static, No Expiration
- **Files:** `edge/discord/control/client.go:30-33`, `cmd/edge-discord/main.go:77-79`
- **Severity:** HIGH
- **Evidence:** The edge-discord service uses `CONTROL_TOKEN` (from `.env`) as a static PASETO bearer token for Control API quota checks. This token:
  - Has no `exp` claim (PASETO `ServiceToken` generates tokens with a TTL, but `CONTROL_TOKEN` is pre-generated and stored as an env var).
  - Is shared across all Edge services.
  - Cannot be rotated without restarting all Edge services.
  - If leaked via `.env` or env inspection, grants unlimited quota-bypass ability.
- **Fix:** Either (a) have the Edge service call `/v1/auth/login` with a workspace API key to obtain short-lived tokens (24h), refreshing them automatically, or (b) use the existing `PASETO_SECRET_KEY` to generate a service token with a TTL inside the Edge startup code, refreshing on expiry.

---

## Medium Severity Issues

### M-1: Engine Docker Base Image is jrottenberg/ffmpeg (Not Distroless)
- **File:** `deployments/Dockerfile.engine:48` — `FROM jrottenberg/ffmpeg:7.1-ubuntu AS runtime`
- **Detail:** While Control and Edge-Discord use `gcr.io/distroless/static-debian12:nonroot` (minimal attack surface, no shell), the Engine uses `jrottenberg/ffmpeg:7.1-ubuntu` which is a full Ubuntu image with shell, package manager, and shared libraries. This was chosen because nsjail requires glibc (protobuf shared libs), but it increases the attack surface significantly (shell access in the container, `apt` available, Ubuntu packages with known CVEs).
- **Mitigation:** The Engine container has `ports: []` in production (no external port exposure), so this is mitigated. Document as a technical debt item for a future static FFmpeg build phase.

### M-2: Weaviate Anonymous Access in Dev
- **File:** `compose.yml:90` — `AUTHENTICATION_ANONYMOUS_ACCESS_ENABLED: "true"`
- **Detail:** Weaviate allows unauthenticated access on ports 8090 (HTTP) and 50051 (gRPC) in the dev compose file. In production, these ports are internal (`ports: []`) but if the Weaviate container is compromised or misconfigured externally, the vector database (and L3 cache) is fully exposed.
- **Fix:** Enable Weaviate API key authentication in production. Add to `compose.prod.yml` override: `AUTHENTICATION_ANONYMOUS_ACCESS_ENABLED: "false"` and `AUTHENTICATION_APIKEY_ALLOWED_KEYS`.

### M-3: MinIO Hardcoded Credentials in Dev
- **File:** `compose.yml:106-107`
  ```yaml
  MINIO_ROOT_USER: minioadmin
  MINIO_ROOT_PASSWORD: minioadmin
  ```
  The Engine also uses these exact credentials in `R2_ACCESS_KEY_ID` and `R2_SECRET_ACCESS_KEY` at lines 208-209. In production, these are overridden by `.env` variables, but the defaults in compose.yml could be accidentally used in production if the `.env.prod` is missing those keys. Defense-in-depth: never hardcode credentials, even for dev.

### M-4: No Max Body Size Limit for Edge Discord CDN Downloads
- **Files:** `edge/discord/listener/listener.go:16` (`MaxAttachmentBytes = 10 << 20 // 10 MiB`) and `cmd/edge-discord/main.go:188-201`
- **Detail:** The listener enforces a 10MB limit but this is a soft limit — the download reads up to `maxBytes+1` then returns an error. The limit is not enforced at the HTTP client level (no `MaxBytesReader` on the CDN download). A malicious CDN response with `Content-Length` exceeding 10MB could still consume memory up to 10MB in the Edge service.
- **Risk:** Low in practice (Discord CDN should honor Content-Length), but a defense-in-depth measure would be to use `http.MaxBytesReader` on the response body.

### M-5: Grafana Hardcoded Admin Credentials
- **File:** `compose.yml:152-153`
  ```yaml
  GF_SECURITY_ADMIN_USER: admin
  GF_SECURITY_ADMIN_PASSWORD: admin
  ```
- **Detail:** In production, Grafana port is blocked (`ports: []`), but these default credentials are still in the configuration. If Grafana is accidentally exposed, an attacker gains full observability access including dashboards with workspace-specific metrics.

### M-6: Engine Health Endpoint Ignores Request Method
- **File:** `cmd/engine/main.go:191`
  ```go
  mux.HandleFunc("GET /healthz", healthHandler)
  ```
- **Detail:** Go 1.22+ method routing is used, so this is actually correct. The function name `healthHandler` ignores the method in the function signature but the mux ensures only GET is routed. **No issue found — confirmed correct after code review.** (Listed here as audit note.)

### M-7: DB Connection Strings in Error Messages May Leak Credentials
- **Files:** `cmd/control/main.go:89`, `engine/audit/neon.go:71,83`
- **Detail:** When database connections fail, the error is logged via `slog.Error()` which writes to stdout. If the Neon DB URL includes embedded credentials (e.g., `postgres://user:password@host/db`), the password portion of the URL would appear in container logs, potentially accessible to anyone with log access.
- **Fix:** Sanitize the database URL before logging. Strip password component: `postgres://user:***@host/db`.

---

## Low Severity / Recommendations

### L-1: pprof Port Hardcoded to 6060
- **File:** `cmd/engine/pprof.go:32` — `Addr: ":6060"`
- **Recommendation:** Make configurable via `PPROF_PORT` env var.

### L-2: s3 Audit Store Hardcoded Default Credentials
- **File:** `engine/audit/r2.go:33-35`
  ```go
  accessKey := envOrDefault("R2_ACCESS_KEY_ID", "minioadmin")
  secretKey := os.Getenv("R2_SECRET_ACCESS_KEY")
  if secretKey == "" { secretKey = "minioadmin" }
  ```
- **Recommendation:** Remove hardcoded defaults. Require explicit configuration in production.

### L-3: Slog JSON Handler Option Configuration
- **File:** `cmd/engine/main.go:97`, `cmd/control/main.go:59`, `cmd/edge-discord/main.go:36`, `cmd/ytdlp-sidecar/main.go:21`
- **Detail:** `slog.NewJSONHandler` is called without `AddSource` option. This means log lines don't include file:line references. For production debugging and incident response, source locations are valuable.
- **Recommendation:** Add `AddSource: true` to `slog.HandlerOptions` in production, or gate it behind an env var.

### L-4: Idempotent Migration Warnings as `slog.Warn`
- **File:** `cmd/control/main.go:222`
  ```go
  slog.Warn("alter table migration failed (may already exist)", "error", err)
  ```
- **Detail:** This is a benign pattern but `slog.Warn` may trigger alerts in monitoring. Use `slog.Debug` or `slog.Info` with a "migration_check" message key.

### L-5: PASETO v4 Token Type Clarification
- **File:** `internal/paseto/paseto.go:1-5`
- **Detail:** The package doc says "PASETO v4 uses Ed25519 (asymmetric, signing) or XChaCha20 (symmetric, encryption)". The actual implementation uses `V4AsymmetricSecretKey` with `V4Sign` — asymmetric signing. This is correct for service-to-service auth since each service needs its own keypair. The documentation should clarify that AURELIOMOD ONLY USES ASYMMETRIC (PUBLIC) MODE, NEVER SYMMETRIC (LOCAL), to prevent confusion.

### L-6: Frame Extraction Edge Case — Infinite Loop
- **File:** `engine/media/ffmpeg.go:86-109` (`ExtractFrames`)
- **Detail:** If `maxFrames` is provided as a very large number (unlikely via the current constant `maxFrames = 3` in pipeline.go), the loop would continue extracting frames without a practical upper bound. The context deadline provides a natural timeout, but an explicit cap is safer. (Currently safe because pipeline.go hardcodes `maxFrames = 3`.)

---

## Pentest Attack Surface Summary

What an attacker would try:

1. **MITM yt-dlp traffic:** Exploit `--no-check-certificates` to inject malicious content via HTTPS interception. The attacker controls what enters the analysis pipeline.

2. **Bypass quota system entirely:** If `CONTROL_TOKEN` or `CONTROL_URL` is misconfigured, submit unlimited analysis requests. Target: WaveSpeed API credit exhaustion.

3. **Exploit unauthenticated Centrifugo:** Connect to WebSocket endpoint, subscribe to `aureliomod.decisions.*` channels, receive all moderation decisions from all workspaces in real-time (cross-tenant data leak).

4. **Craft polyglot files:** Send a file that is valid as both JPEG and ZIP. If the anti-polyglot JPEG re-encode fails or the normalizer path is bypassed, the archive may be extractable. The current `encodeJPEG` re-encodes from RGB24 pixels (pipeline step 5), so this is well-mitigated — but the attacker would still try.

5. **Content-Type spoofing:** Send `.exe` with Content-Type `image/jpeg`. MIME validation (when `ENFORCE_MIME=true`) rejects this by checking magic bytes. When `ENFORCE_MIME=false` (default), the magic bytes are still checked via `DetectMIME()` which returns `application/octet-stream`, but the handler would still pass it to the normalizer. The normalizer calls FFmpeg, which may fail safely on non-media input.

6. **DoS via large request body:** Send a request with `raw_bytes` containing gigabytes of data. No body size limit on the Engine HTTP server. This could cause OOM in the Engine container or FFmpeg process.

7. **SQL injection via workspace_id:** All queries use `$1`, `$2` parameterization. Confirmed safe. No dynamic SQL construction.

8. **Token brute-force:** PASETO v4 tokens are Ed25519-signed. Brute-forcing is computationally infeasible. The 24h TTL limits the window. Key rotation with `PASETO_PREVIOUS_KEY` needs to be wired.

9. **Database credential leakage:** Monitor container logs for Neon DB URLs containing embedded credentials. If the DB connection fails, the URL is logged verbatim.

10. **Dependency chain attack:** No `govulncheck` output available at audit time (requires running `go install golang.org/x/vuln/cmd/govulncheck@latest`). The Woodpecker CI runs `govulncheck` in the `security` stage. Ensure this stage is not skipped in practice.

---

## Hardcoded Secrets Scan Results

| Location | Finding | Severity |
|----------|---------|----------|
| `deployments/centrifugo.json` | `hmac_secret_key`, `password`, `secret`, `http_api.key` — all hardcoded defaults | CRITICAL |
| `compose.yml:106-107` | `MINIO_ROOT_USER=minioadmin`, `MINIO_ROOT_PASSWORD=minioadmin` | MEDIUM |
| `compose.yml:152-153` | `GF_SECURITY_ADMIN_USER=admin`, `GF_SECURITY_ADMIN_PASSWORD=admin` | LOW |
| `engine/audit/r2.go:33-35` | Hardcoded fallback `minioadmin` / `minioadmin` for S3 store | LOW |
| `.env.example` | Template file (expected, no actual secrets) | INFO |
| `deployments/production/Caddyfile:4` | Email `aurelio@aureliomod.com` hardcoded — this is a config detail for Let's Encrypt, not a secret | INFO |

**No hardcoded API keys, database passwords, or token secrets were found in Go source code.** Secrets are correctly loaded from environment variables. The `.gitignore` properly excludes `.env` and `.env.*`.

---

## Checklist Status

| Area | Status | Notes |
|------|--------|-------|
| **Auth & AuthZ** | ⚠️ WARN | PASETO v4 correct; key rotation not wired in Engine; Control token static; all endpoints gated behind `PASETO_AUTH_ENABLED=true` (off by default); pprof protected |
| **Input Validation** | ⚠️ WARN | MIME validation exists but gated behind `ENFORCE_MIME=false`; no max body size limit; 10MB soft cap on Edge; polyglot mitigation via pixel re-encode is solid |
| **Secrets Management** | ⚠️ WARN | No hardcoded secrets in code — Doppler/.env pattern correct for Fase 1; Centrifugo defaults need Prod override; MinIO dev creds in compose.yml |
| **Sandboxing & Isolation** | ✅ PASS | nsjail compiled into Engine image; FFmpeg runs via `NsJailFFmpeg` with `--disable_clone_newnet`; yt-dlp via sidecar container; all production ports blocked (`ports: []`) |
| **Rate Limiting & DoS** | ✅ PASS | KrakenD per-endpoint rate limits (100 global, 20 per client for workspaces); Caddy 200r/s rate limit; Discord rate limiter 45 req/s; failsafe-go circuit breaker + bulkhead on WaveSpeed |
| **SQL Injection** | ✅ PASS | All queries use positional parameters (`$1`, `$2`, ...); no string concatenation for SQL; lib/pq handles escaping |
| **API Security** | ⚠️ WARN | Most endpoints require PASETO; login has no rate limit (bypass in krakenD?); Stripe webhook signature verified correctly; no CORS headers needed (KrakenD/Caddy handles); verbose errors in billing endpoint leak Stripe error details |
| **TLS & Transport** | ⚠️ WARN | Caddy auto LE, HSTS, HTTP→HTTPS redirect (good); internal services via plaintext (mitigated by single-VPS/loopback for Fase 1); OTLP telemetry uses insecure gRPC |
| **Audit Trail** | ❌ FAIL | Cache hits produce NO audit events (C-2); Edge Discord handler only emits on BLOCK, not QUEUED; MultiEmitter pattern is correct but missing coverage |
| **Container Security** | ⚠️ WARN | Control/Edge use Distroless nonroot (great); Engine uses Ubuntu base (needs nsjail glibc); HEALTHCHECK on all containers; OCI labels present; `cap_add`/`privileged: true` absent |
| **Dependency Security** | ⚠️ WARN | govulncheck in CI pipeline; sumdb.golang.org verification inherent in Go toolchain; go.mod versions look current; need actual `govulncheck` run to confirm |

---

## Compliance Readiness

| Regulation | Requirement | Status | Gap |
|------------|-------------|--------|-----|
| **GDPR Art. 22** | Audit trail for automated decisions | ❌ FAIL | Cache hits not audited |
| **DSA Art. 15** | Statement of reasons on block | ✅ PASS | DM to user with block_reason |
| **NIS2 Art. 21** | Full pipeline traceability | ❌ FAIL | Same as GDPR gap |
| **AI Act Art. 52** | Analyst version documented | ✅ PASS | `analyst_version` field populated |
| **DPIA** | Data protection impact assessment | 🔲 PENDING | Document not reviewed |

---

## Recommended Remediation Priority

### Before Pentest (Blocking):
1. **C-1:** Remove `--no-check-certificates` from yt-dlp command **(1 line)**
2. **C-2:** Emit audit events for L1/L2/L3 cache hits **(~15 lines in pipeline.go)**
3. **C-3:** Create production Centrifugo config with secrets from env **(new file)**
4. **H-1:** Make CONTROL_URL/CONTROL_TOKEN required or fail-closed **(~3 lines)**

### Before Production:
5. **H-2:** Wire Web Risk check into EXTERNAL_URL pipeline path **(~30 lines)**
6. **H-3:** Wire RotatableTokenManager in Engine and Control mains **(~10 lines each)**
7. **H-4:** Add request body size limit to Engine HTTP server **(~5 lines)**
8. **H-5:** Gate OTLP insecure behind env var / default to TLS **(~10 lines)**
9. **H-6:** Implement token refresh cycle for Edge Control client **(~50 lines)**

### Post-Pentest / Hardening:
10. **M-1–M-7:** All medium issues
11. **L-1–L-6:** All low recommendations
12. Run `govulncheck ./...` and fix any reported vulnerabilities
13. Add RBAC enforcement on `audit_log` table (prevent UPDATE/DELETE at DB level)
