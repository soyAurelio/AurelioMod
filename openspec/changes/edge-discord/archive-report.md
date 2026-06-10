# Archive Report: edge-discord

**Change**: edge-discord  
**Archived**: 2026-06-10  
**Mode**: Strict TDD  
**Status**: COMPLETE — 17/17 tasks implemented, verified, and archived

## Executive Summary

Built the first Edge adapter: Discord bot with full content moderation pipeline.
- **PR 1**: Foundation — ratelimit (45 req/s), audit (JSON emitter), ConnectRPC client (circuit breaker), CDN listener
- **PR 2**: Behavior — slash commands (/moderate, /status, /config), decision handler (BLOCK/ALLOW/QUEUED), main.go wiring, compose.yml service

All 17 tasks completed. 69 tests passing.

## Completeness

| Metric | Value |
|--------|-------|
| Tasks total | 17 |
| Tasks complete | 17 |
| Tasks incomplete | 0 |
| Total tests | 69 |
| All tests passing | ✅ 22/22 packages |

## Artifacts

- `openspec/changes/edge-discord/proposal.md`
- `openspec/changes/edge-discord/spec.md`
- `openspec/changes/edge-discord/specs/known-issues/spec.md`
- `openspec/changes/edge-discord/design.md`
- `openspec/changes/edge-discord/tasks.md`
- `openspec/changes/edge-discord/apply-progress.md`

## Deviations

1. **DiscordRest interface**: Introduced minimal mockable interface instead of direct `disgo` dependency.
2. **Format*Reply extracted**: Pure functions extracted for testability per strict TDD.
3. **ContentType mapping in main.go**: `attContentType()`, `isImageType()`, etc. added for Discord MIME → proto mapping.
4. **snowflake.New(time.Time)**: Tests use direct uint64 casts instead.

## Risks

None remaining. Bot is functional and tested. Known issues documented in `specs/known-issues/spec.md`.
