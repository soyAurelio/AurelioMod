# Edge Discord Bot Adapter — Specifications

Proto: `proto/aureliomod/v1/content.proto` — `Analyze` RPC, `SOURCE_PLATFORM_DISCORD`.

---

## discord-bot

### Req: Gateway & Content Interception

Bot MUST connect via `disgo` using `DISCORD_TOKEN` and intercept `MessageCreate` events with attachments or URLs. Content forwarded to Engine `Analyze` with correct `ContentType` and `SOURCE_PLATFORM_DISCORD`. Plain text messages ignored. Attachments >10MB skipped with warning.

**Scenarios:**

| # | Scenario | GIVEN | THEN |
|---|----------|-------|------|
| 1 | Image triggers analysis | Message with image ≤10MB | `Analyze` called with `CONTENT_TYPE_IMAGE`, unique `content_id` |
| 2 | URL triggers analysis | Message with external URL, no attachments | `Analyze` called with `CONTENT_TYPE_EXTERNAL_URL`, URL as `raw_bytes` |
| 3 | Plain text ignored | Message, no attachments or URLs | No `Analyze` call |

### Req: Circuit Breaker & Graceful Shutdown

After 5 consecutive `Analyze` failures in 30s: breaker opens, default BLOCK all, log `event=circuit_breaker_open`. Half-open probe every 60s. On `SIGTERM`: drain in-flight RPCs, `disgo.Close`, exit 0 within 10s.

**Scenarios:**

| # | Scenario | GIVEN | THEN |
|---|----------|-------|------|
| 1 | Breaker opens | 5 consecutive failures | 6th message default-blocked; audit alert emitted |
| 2 | Shutdown drains RPCs | 3 in-flight RPCs, `SIGTERM` | All RPCs complete before `disgo.Close`; exit 0 |

### Req: Structured Audit Logging

All moderation actions emit JSON `slog` (stdout): `event`, `content_id`, `decision`, `source_platform=discord`, `workspace_id`, `elapsed_ms`. BLOCK includes `block_reason`, `category`, `analyst_version`. Must follow Engine's multi-emitter pattern.

**Scenarios:**

| # | Scenario | GIVEN | THEN |
|---|----------|-------|------|
| 1 | BLOCK logged | Engine returns `DECISION_BLOCK` with reason | JSON line: `event=moderation_block`, all required fields |

---

## discord-commands

### Req: Slash Command Registration

On `GuildCreate`, register `/moderate <url>`, `/status`, `/config workspace_id <id> enforce <on|off>` via `disgo.ApplicationCommandCreate`.

**Scenarios:**

| # | Scenario | GIVEN | THEN |
|---|----------|-------|------|
| 1 | Guild join registers | Bot joins guild | All 3 commands registered; `event=slash_commands_registered` |
| 2 | /moderate submits URL | User runs `/moderate <url>` | `Analyze` called with `EXTERNAL_URL`; interaction reply shows decision |

### Req: /status Command

Ephemeral response with: bot uptime, Engine health (HEAD probe), circuit breaker state, rate limiter available tokens.

**Scenarios:**

| # | Scenario | GIVEN | THEN |
|---|----------|-------|------|
| 1 | /status returns diagnostics | Engine reachable, breaker closed | Ephemeral: `uptime`, `engine: healthy`, `circuit_breaker: closed`, token count |

---

## discord-moderation

### Req: Decision Enforcement

`DECISION_BLOCK`: delete message ≤500ms + DM author with `block_reason` (DSA Art. 15). `DECISION_ALLOW`: noop. `DECISION_QUEUED`: fallback to default BLOCK with `block_reason="pending_analysis"`. Feature gate: `ENFORCE_MODERATION` env var (default `true`).

**Scenarios:**

| # | Scenario | GIVEN | THEN |
|---|----------|-------|------|
| 1 | BLOCK deletes+notifies | `DECISION_BLOCK`, reason `"violence_graphic"` | Message deleted ≤500ms; ephemeral DM with reason |
| 2 | ALLOW silent | `DECISION_ALLOW` | No API calls, no log lines |
| 3 | QUEUED falls back | `DECISION_QUEUED`, default=BLOCK | Message deleted with `block_reason="pending_analysis"` |

### Req: Rate Limiting

Token bucket at 45 req/s (headroom below Discord 50/s). Burst: 10 extra tokens. Queue: 2s deadline. Exceeding bucket+queue: drop + log `event=rate_limit_drop`.

**Scenarios:**

| # | Scenario | GIVEN | THEN |
|---|----------|-------|------|
| 1 | Normal rate | Bucket has ≥3 tokens, 3 concurrent deletes | All 3 execute immediately |
| 2 | Bucket exhausted | Bucket empty, queue full | Request dropped; `rate_limit_drop` logged |

### Req: Audit Trail

Per-enforcement audit record via configurable multi-emitter (default stdout): `timestamp`, `workspace_id`, `content_id`, `content_hash`, `decision`, `block_reason`, `guild_id`, `author_id`, `elapsed_ms`. Emitter interface matches Engine's `emitter.Emitter`.

**Scenarios:**

| # | Scenario | GIVEN | THEN |
|---|----------|-------|------|
| 1 | Audit record on BLOCK | BLOCK enforcement complete | stdout JSON with all fields; `elapsed_ms` ≤ 500 |
