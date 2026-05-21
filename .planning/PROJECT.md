# Kiro-Go Claude Code Compatibility and High Availability

## What This Is

Kiro-Go converts Kiro accounts into Anthropic/OpenAI-compatible APIs for Claude Code and other AI developer tools. This project is a brownfield optimization roadmap for making Kiro-Go behave like the official Anthropic API in Claude Code, while keeping `/www/sub2api` Opus 4.7 traffic sustainable through accurate account health, model pressure, and retry semantics.

## Core Value

sub2api should be able to call Opus 4.7 through Kiro-Go continuously whenever at least one real Kiro account remains viable, and Kiro-Go must report accurate degraded/blocked state when upstream capacity or account pool health makes success impossible.

## Current Milestone: v1.1 Opus 4.7 Sustainable Health

**Goal:** Make `sub2api -> Kiro-Go -> Kiro Opus 4.7` a measurable, sustainable health contract instead of a best-effort retry path.

**Target features:**
- Define and expose an Opus 4.7 readiness contract for sub2api using fleet health, safe concurrency, retryability, and real content-success signals.
- Use the already deployed local Kiro CLI as an account source for credential-shape diagnostics, refresh validation, and lightweight Opus 4.7 probes.
- Harden Kiro upstream failure classification so temporary account limits, model capacity pressure, rate limits, quota exhaustion, auth failure, and network/server failures drive different cooldown and retry behavior.
- Learn real `account + claude-opus-4.7` success state from live calls and use it to improve scheduler preview, readiness, and routing priority.
- Prove the contract with latest-code sub2api stream and non-stream UAT evidence, including request logs, sub2api usage/logs, fleet readiness, and admin screenshots.

## Requirements

### Validated

- PASS Anthropic `/v1/messages` and OpenAI `/v1/chat/completions` endpoints exist - existing codebase
- PASS Multi-account Kiro pool, token refresh, account import/export, SSE streaming, admin UI, and usage/request observability exist - existing codebase
- PASS Claude Code setup guidance exists for `ANTHROPIC_BASE_URL`, auth token, primary model, small-fast model, and MCP tool search - existing README
- PASS Kiro MCP web search bridge exists - existing integration map
- PASS sub2api to Kiro-Go Opus 4.7 high-concurrency baseline passes after routing fix: non-stream 100/100 PASS and stream 100/100 PASS - UAT 2026-05-20

### Active

- [ ] D. Make Opus 4.7 downstream health measurable for sub2api: fleet readiness, safe concurrency, retryability, content success, and stable fallback semantics.
- [ ] E. Use the existing local Kiro CLI for account discovery, credential-shape diagnostics, refresh validation, and minimal Opus 4.7 probes.
- [ ] F. Harden upstream failure classification and account/model learning so Kiro-Go routes around recoverable account failures without hiding real upstream exhaustion.
- [ ] G. Re-run latest-code sub2api Opus 4.7 UAT for stream and non-stream traffic and require aligned API/log/database/screenshot evidence before PASS.

### Out of Scope

- Running or managing local MCP servers inside Kiro-Go - Claude Code remains the MCP host and Kiro-Go should preserve the API/tool protocol.
- Hiding real upstream exhaustion from clients - Kiro-Go should route around recoverable account failures, then return accurate exhausted-pool errors when no viable account remains.
- Treating one account's temporary limit as a global risk-group lockout without direct evidence - recent tests show one account can 429 while another succeeds.
- Desktop machine ID mutation as a first-class server feature - this belongs to desktop account-switching tools, not the API gateway core.
- Multi-replica distributed state store in this milestone - current architecture is a single-process JSON-backed gateway.
- Installing or managing Kiro CLI itself - the deployment already has Kiro CLI available; this milestone uses it as an existing local dependency.
- Claiming upstream Kiro Opus 4.7 is globally healthy when Kiro-Go only has synthetic fallback or transport-level 200 responses - real model content success must be tracked separately.

## Context

Kiro-Go is a Go 1.21 single-binary HTTP service. Public API, admin API, translation, retries, model cache, account maintenance, and static UI are mostly orchestrated by `proxy/handler.go`; account routing state lives in `pool/account.go`; persistent config and credentials live in `data/config.json` through `config/config.go`.

Recent production debugging showed Claude Code retries were caused by Kiro upstream 429s passing through `/www/sub2api`, not Claude Code being stuck. The important correction is that 429 is a real upstream/runtime signal, but it must be classified precisely. `INSUFFICIENT_MODEL_CAPACITY` is model/provider pressure, while "temporary limits" is an account-level temporary limit unless direct evidence proves otherwise.

External project comparison shows `jwadow/kiro-gateway` has useful patterns around layered model resolution, account circuit breaking, per-request account exclusion, first-token retry, payload guards, WebSearch injection, and broad converter/streaming tests. Because it is AGPL-3.0, Kiro-Go should borrow design ideas rather than copy code.

`KiroSwitchManager` is closed source but documents operational expectations around multi-account management, CLI/IDE dual mode, quota-aware switching, token refresh, model-list validation, WebSearch injection, and user-readable account diagnostics. The most relevant local constraint is that this deployment already has Kiro CLI, so Kiro-Go should diagnose and consume existing CLI account state rather than plan installation.

Second-round v1.1 research found that `/www/sub2api` already has account/channel model mapping, usage logs, temp-unschedulable account state, 429/529 cooldown handling, and channel monitor history. The key missing integration is pre-dispatch consumption of Kiro-Go `/admin/api/fleet/readiness?model=...`; channel monitor alone cannot know Kiro-Go account pool state, Opus admission, or content-success continuity.

Official Kiro docs confirm CLI/IDE dual product surfaces, dynamic model availability, and Opus 4.7 tier/region sensitivity. Low-level Kiro runtime APIs and error body shapes should be treated as variable protocol surfaces, so Kiro-Go must parse structured errors when present, keep raw evidence for diagnostics, and fall back conservatively.

## Constraints

- **Compatibility**: Claude Code is the primary client; `/v1/messages` streaming, tool-use loops, large context, and model aliases must remain Anthropic-compatible.
- **Reliability**: sub2api high-concurrency calls must be verified with real stream and non-stream requests, not only unit tests.
- **Observability**: PASS requires aligned screenshot, API, log, and database evidence for admin pages and downstream routing.
- **Security**: Runtime credential files such as `data/config.json` and recovery snapshots must not be read into planning artifacts or exposed in logs.
- **Architecture**: Keep changes sympathetic to the current Go standard-library HTTP service and JSON config store unless a phase explicitly changes that boundary.
- **Git**: Planning docs are tracked in git for this project.
- **Kiro CLI**: Local Kiro CLI is already deployed and should be treated as an existing account/diagnostic source.
- **Truthfulness**: A stable downstream 200 response is not automatically a real Opus 4.7 model success; content success, fallback, and retryability must remain distinct.

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Use three coarse phases A -> B -> C | User requested this sequence and selected coarse GSD granularity. | Pending |
| Treat Claude Code official parity as the first phase | Client protocol correctness defines the rest of the reliability work. | Pending |
| Treat temporary limits as per-account by default | Real tests showed one account 429 while other accounts succeeded. | Pending |
| Require full-stack UAT evidence before PASS | User explicitly requires screenshot/API/database evidence and screenshot analysis before passing. | Pending |
| Keep Kiro-Go as API gateway, not MCP host or desktop machine-ID manager | Avoid scope that belongs to Claude Code or desktop account switching tools. | Pending |
| Define v1.1 around Opus 4.7 sustainable health | User wants sub2api Opus 4.7 calls to remain healthy and sustainable through Kiro-Go. | Pending |
| Use existing local Kiro CLI, do not install it | User confirmed the local deployment already has Kiro CLI. | Pending |
| Treat 100% health as a gateway contract, not an upstream guarantee | Kiro-Go cannot control Kiro global capacity, but it can avoid amplification, expose degraded state, and route while viable accounts exist. | Pending |

## Evolution

This document evolves at phase transitions and milestone boundaries.

**After each phase transition** (via `$gsd-transition`):
1. Requirements invalidated? -> Move to Out of Scope with reason
2. Requirements validated? -> Move to Validated with phase reference
3. New requirements emerged? -> Add to Active
4. Decisions to log? -> Add to Key Decisions
5. "What This Is" still accurate? -> Update if drifted

**After each milestone** (via `$gsd-complete-milestone`):
1. Full review of all sections
2. Core Value check - still the right priority?
3. Audit Out of Scope - reasons still valid?
4. Update Context with current state

---
*Last updated: 2026-05-21 after v1.1 milestone start*
