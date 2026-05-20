# Kiro-Go Claude Code Compatibility and High Availability

## What This Is

Kiro-Go converts Kiro accounts into Anthropic/OpenAI-compatible APIs for Claude Code and other AI developer tools. This project is a brownfield optimization roadmap for making Kiro-Go behave like the official Anthropic API in Claude Code, while remaining reliable through `/www/sub2api` under high-concurrency Kiro account-pool traffic.

## Core Value

Claude Code through Kiro-Go should behave like the official Anthropic API while routing Kiro accounts correctly enough that one account's 429 never poisons the whole downstream path.

## Requirements

### Validated

- PASS Anthropic `/v1/messages` and OpenAI `/v1/chat/completions` endpoints exist - existing codebase
- PASS Multi-account Kiro pool, token refresh, account import/export, SSE streaming, admin UI, and usage/request observability exist - existing codebase
- PASS Claude Code setup guidance exists for `ANTHROPIC_BASE_URL`, auth token, primary model, small-fast model, and MCP tool search - existing README
- PASS Kiro MCP web search bridge exists - existing integration map
- PASS sub2api to Kiro-Go Opus 4.7 high-concurrency baseline passes after routing fix: non-stream 100/100 PASS and stream 100/100 PASS - UAT 2026-05-20

### Active

- [ ] A. Complete Claude Code official compatibility: model aliases, SSE, tool loops, thinking, prompt cache handling, large context safety, and automated UAT evidence.
- [ ] B. Make account scheduling and sub2api routing high availability: per-account temporary-limit isolation, failure taxonomy, adaptive admission, background-probe throttling, and 10x10 stream/non-stream verification.
- [ ] C. Improve Kiro ecosystem operations: CLI/account import diagnostics, scheduler policy controls, WebSearch/MCP observability, admin fleet actions, and documented operational boundaries.

### Out of Scope

- Running or managing local MCP servers inside Kiro-Go - Claude Code remains the MCP host and Kiro-Go should preserve the API/tool protocol.
- Hiding real upstream exhaustion from clients - Kiro-Go should route around recoverable account failures, then return accurate exhausted-pool errors when no viable account remains.
- Treating one account's temporary limit as a global risk-group lockout without direct evidence - recent tests show one account can 429 while another succeeds.
- Desktop machine ID mutation as a first-class server feature - this belongs to desktop account-switching tools, not the API gateway core.
- Multi-replica distributed state store in this milestone - current architecture is a single-process JSON-backed gateway.

## Context

Kiro-Go is a Go 1.21 single-binary HTTP service. Public API, admin API, translation, retries, model cache, account maintenance, and static UI are mostly orchestrated by `proxy/handler.go`; account routing state lives in `pool/account.go`; persistent config and credentials live in `data/config.json` through `config/config.go`.

Recent production debugging showed Claude Code retries were caused by Kiro upstream 429s passing through `/www/sub2api`, not Claude Code being stuck. The important correction is that 429 is a real upstream/runtime signal, but it must be classified precisely. `INSUFFICIENT_MODEL_CAPACITY` is model/provider pressure, while "temporary limits" is an account-level temporary limit unless direct evidence proves otherwise.

External project comparison shows `jwadow/kiro-gateway` has useful patterns around model resolution, account circuit breaking, first-token retry, payload guards, WebSearch injection, and broad converter/streaming tests. `KiroSwitchManager` documents ecosystem expectations around multi-account management, CLI/IDE dual mode, quota-aware switching, WebSearch, and fleet operations.

## Constraints

- **Compatibility**: Claude Code is the primary client; `/v1/messages` streaming, tool-use loops, large context, and model aliases must remain Anthropic-compatible.
- **Reliability**: sub2api high-concurrency calls must be verified with real stream and non-stream requests, not only unit tests.
- **Observability**: PASS requires aligned screenshot, API, log, and database evidence for admin pages and downstream routing.
- **Security**: Runtime credential files such as `data/config.json` and recovery snapshots must not be read into planning artifacts or exposed in logs.
- **Architecture**: Keep changes sympathetic to the current Go standard-library HTTP service and JSON config store unless a phase explicitly changes that boundary.
- **Git**: Planning docs are tracked in git for this project.

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Use three coarse phases A -> B -> C | User requested this sequence and selected coarse GSD granularity. | Pending |
| Treat Claude Code official parity as the first phase | Client protocol correctness defines the rest of the reliability work. | Pending |
| Treat temporary limits as per-account by default | Real tests showed one account 429 while other accounts succeeded. | Pending |
| Require full-stack UAT evidence before PASS | User explicitly requires screenshot/API/database evidence and screenshot analysis before passing. | Pending |
| Keep Kiro-Go as API gateway, not MCP host or desktop machine-ID manager | Avoid scope that belongs to Claude Code or desktop account switching tools. | Pending |

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
*Last updated: 2026-05-20 after initialization*
