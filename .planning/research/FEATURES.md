# Feature Research

**Domain:** Kiro-Go as a Claude Code / Anthropic-compatible gateway for Kiro accounts
**Researched:** 2026-05-20
**Confidence:** HIGH for local codebase evidence, MEDIUM for external Kiro ecosystem behavior

## Research Summary

Kiro-Go already provides the core proxy: Anthropic `/v1/messages`, OpenAI `/v1/chat/completions`, multi-account routing, token refresh, SSE, admin UI, model cache, Kiro MCP web search, and usage/account observability. The next milestone should make that proxy behave like the official Anthropic API when used by Claude Code, while preserving high availability through Kiro account-level scheduling and sub2api downstream integration.

The important distinction from recent debugging is that a model being listed by Kiro-Go is not the same as an account being currently schedulable. Upstream Kiro can return real 429 classes for model capacity or account temporary limits. The gateway must route around per-account temporary limits where possible, avoid spreading one account's limit to the full pool, and return accurate downstream errors only after all viable accounts are exhausted.

## Evidence Baseline

### Existing Kiro-Go Capabilities

| Capability | Evidence | Current State |
|------------|----------|---------------|
| Anthropic and OpenAI compatible endpoints | `README.md`, `.planning/codebase/ARCHITECTURE.md` | Existing |
| Multi-account pool with weighted round-robin | `pool/account.go`, `.planning/codebase/ARCHITECTURE.md` | Existing |
| Token refresh and multiple account import flows | `auth/*.go`, `.planning/codebase/INTEGRATIONS.md` | Existing |
| SSE streaming and response conversion | `proxy/handler.go`, `proxy/translator.go` | Existing |
| Admin UI and request/account observability | `web/index.html`, `proxy/handler.go` | Existing |
| Kiro MCP web search bridge | `.planning/codebase/INTEGRATIONS.md` | Existing |
| Claude Code configuration guidance | `README.md` | Existing |
| Opus 4.7 10x10 stream/non-stream sub2api UAT | `docs/superpowers/uat/sub2api-kiro-opus47-account-lb-20260520144855/UAT-RESULT.md` | Validated baseline |

### Recent Validated Fixes

- Commit `abf1170 fix: improve kiro account routing reliability` removed risk-group expansion for temporary limits and preserved per-account fallback behavior.
- Real UAT on 2026-05-20 showed `/www/sub2api -> Kiro-Go -> Kiro` Opus 4.7:
  - non-stream 100/100 PASS, max latency 46651 ms
  - stream 100/100 PASS, max latency 56595 ms
- UAT screenshots, Kiro-Go APIs, and sub2api database evidence agreed before PASS.

## Official Compatibility Targets

### Claude Code / Anthropic API Expectations

| Area | Why It Matters | Kiro-Go Requirement |
|------|----------------|---------------------|
| Base URL and auth compatibility | Claude Code is configured through Anthropic-compatible environment/settings. | `/v1/messages`, `/v1/models`, auth headers, and model aliases must remain compatible with Claude Code expectations. |
| Streaming wire format | Claude Code depends on correct SSE event ordering and tool loop progress. | Emit valid Anthropic-style `message_start`, content/tool/thinking deltas, stop events, and error handling. |
| Tool use and tool result loops | Claude Code subagents and Explore/Task workflows depend on tool calls returning to the model. | Preserve `tools`, `tool_choice`, `tool_use`, `tool_result`, `tool_reference`, and large tool payloads without corrupt conversion. |
| Prompt caching metadata | Claude Code and Anthropic clients may send cache controls. | Preserve or safely ignore cache metadata while logging cache estimates and avoiding invalid upstream payloads. |
| Thinking/reasoning | Users expect reasoning-capable Claude variants to work consistently. | Normalize thinking config and model suffixes while returning valid text/thinking streams. |
| Large context | Claude Code sends repository context, file content, and tool results. | Guard payload size explicitly; do not silently trim below official-model expectations unless configured and observable. |

## External Open Source Comparison

### `jwadow/kiro-gateway`

`kiro-gateway` is a Python/FastAPI Kiro proxy that targets many Anthropic/OpenAI-compatible clients. Useful patterns to borrow:

| Capability | Observed Pattern | Kiro-Go Improvement Direction |
|------------|------------------|-------------------------------|
| Smart model resolution | Normalizes hyphen/dot/versioned model names and hidden models. | Centralize alias normalization and expose resolver diagnostics in admin/API. |
| Multi-account failover | Tracks attempted accounts, retries recoverable failures, and uses circuit breaker/backoff. | Make per-request account attempt traces first-class and configurable. |
| Streaming resilience | Has explicit streaming tests and first-token retry concepts. | Add first-token timeout/retry settings with wire-format tests. |
| Payload/truncation recovery | Provides payload guards and truncation recovery tests. | Convert Kiro-Go guard metadata into configurable policy plus UAT. |
| Web search support | Supports web search injection. | Keep Kiro MCP web search but make client-visible behavior and errors clearer. |
| Test suite breadth | Unit/integration tests cover converters, streaming, payload guards, account errors, routes. | Expand Kiro-Go tests around Claude Code tool loops and scheduler edge cases. |

### `zeoak9297/KiroSwitchManager`

`KiroSwitchManager` is release-only, but its README documents user-facing ecosystem capabilities:

| Capability | User Value | Kiro-Go Improvement Direction |
|------------|------------|-------------------------------|
| Social / AWS Builder ID / Enterprise IDC auth | Users need account import options across common Kiro identities. | Keep auth import parity and improve import diagnostics. |
| CLI / IDE dual mode | Users may source credentials from Kiro IDE or CLI. | Add documented CLI credential import and rollback-safe account onboarding. |
| Per-account machine ID binding | Desktop switching tools use stable per-account identity surfaces. | Treat this as operator documentation/risk context, not a server-side first-class requirement. |
| Auto switch strategies | Users want quota-aware rotation. | Add scheduler policies: round-robin, least-recently-used, quota-aware, latency-aware. |
| WebSearch injection and model lock | Users need deterministic model/tool behavior. | Improve model lock/readiness visibility and web search observability. |
| Batch operations and quota UI | Admin users need fleet management. | Add account batch health/refresh controls and clearer quota/status panels. |

## Candidate Requirements

### A. Claude Code Official Compatibility

| Candidate REQ | Feature | Why Expected | Complexity | Testable Requirement |
|----------------|---------|--------------|------------|----------------------|
| CC-01 | Compatibility matrix | Avoid ambiguous "works with Claude Code" claims. | MEDIUM | Matrix lists endpoint/model/tool/stream/cache/thinking/count-token behavior with PASS/PARTIAL/MISSING evidence. |
| CC-02 | Model resolver parity | Claude Code and users send variant aliases. | MEDIUM | Dot, hyphen, versioned, and small-fast aliases resolve deterministically and are visible in request logs. |
| CC-03 | Tool loop correctness | Explore/Task/subagents fail if tool loops are malformed. | HIGH | Claude Code tool-use and tool-result flows produce tool calls, consume results, and stop correctly. |
| CC-04 | SSE wire correctness | Claude Code UI can appear stuck when stream protocol is invalid. | HIGH | Streaming tests assert event ordering for text, tool use, tool result, thinking, first-token retry, and upstream errors. |
| CC-05 | Large context safety | Claude Code sends large repo context. | HIGH | Large request policy is configurable, observable, and verified with realistic payload sizes through sub2api. |
| CC-06 | Prompt cache/thinking behavior | Official clients send advanced fields. | MEDIUM | Cache controls and thinking config are preserved, normalized, or rejected with clear compatibility errors. |
| CC-07 | Automated Claude Code UAT | Manual checks are too easy to overclaim. | HIGH | UAT runs real non-stream/stream/tool-loop requests and records API, DB, screenshot, and log evidence before PASS. |

### B. High-Availability Account Pool and sub2api Correctness

| Candidate REQ | Feature | Why Expected | Complexity | Testable Requirement |
|----------------|---------|--------------|------------|----------------------|
| HA-01 | Per-account temporary-limit isolation | One account 429 must not stop all accounts. | HIGH | A temporary-limited account is skipped while other schedulable accounts continue serving. |
| HA-02 | Failure taxonomy | Capacity, quota, auth, network, and temporary-limit require different actions. | MEDIUM | Logs/API classify `model_capacity`, `temporary_limited`, `rate_limited`, `quota`, `auth`, and `network` separately. |
| HA-03 | Account-aware admission control | Static global Opus limits waste capacity or overload upstream. | HIGH | Admission limits are configurable and consider model, viable account count, queue depth, and observed failures. |
| HA-04 | Background probe throttling | Auto-refresh/health-check can amplify real traffic. | MEDIUM | Background jobs have bounded concurrency, jitter, cooldown awareness, and do not probe cooling accounts. |
| HA-05 | sub2api failover semantics | Downstream same-account retry can amplify 429 storms. | HIGH | sub2api receives clear retryability/error classes and does not repeat a known temporary-limited Kiro path. |
| HA-06 | Request-level attempt trace | Debugging needs exact account path. | MEDIUM | Every request log exposes attempted accounts, final account, failure reasons, latency, first-token latency, and payload policy. |
| HA-07 | Reliability SLO UAT | User explicitly requires 100% correct high-concurrency Claude Code calls. | HIGH | 10 concurrent x 10 non-stream and stream Opus 4.7 through sub2api return expected content with max latency recorded. |

### C. Kiro Ecosystem Enhancements

| Candidate REQ | Feature | Why Expected | Complexity | Testable Requirement |
|----------------|---------|--------------|------------|----------------------|
| KE-01 | CLI credential import | Kiro users may rely on Kiro CLI / Amazon Q CLI credential stores. | MEDIUM | Admin can import supported CLI credential files with validation and rollback on failure. |
| KE-02 | Account onboarding diagnostics | Fleet setup fails without clear token/profile/proxy errors. | MEDIUM | Import/refresh UI shows auth method, profile ARN state, model list state, quota, and actionable error. |
| KE-03 | Scheduler policy controls | Different operators prefer least-used, quota-aware, or stable routing. | MEDIUM | Admin can choose policy and observe decisions without code changes. |
| KE-04 | WebSearch/MCP observability | Search injection can silently change model input. | MEDIUM | Logs show when search ran, query/result count, upstream MCP status, and injected payload size. |
| KE-05 | Admin fleet operations | 21+ accounts need batch workflows. | MEDIUM | Admin supports batch refresh, health check, enable/disable, export, and readiness filtering. |
| KE-06 | Operational safety hardening | Current codebase concerns include config durability and admin security gaps. | HIGH | Atomic config writes, safer admin auth/session behavior, and secret redaction land with tests. |

## Anti-Features

| Anti-Feature | Why Excluded | Better Alternative |
|--------------|--------------|--------------------|
| Hide all upstream 429s from clients | It creates false success and masks real exhaustion. | Route around per-account failures; report accurate exhausted-pool errors with evidence. |
| Treat risk-group temporary-limit as a global pool state | Recent manual tests show one account can 429 while others succeed. | Track temporary limit per account unless verified shared evidence exists. |
| Claim 100% availability against an exhausted upstream | No gateway can guarantee success when all upstream accounts are blocked. | Guarantee correct scheduling, retry, and failure semantics; measure UAT pass rate for the current pool. |
| Run/manage local MCP servers inside Kiro-Go | Claude Code remains the MCP host. | Preserve Claude Code tool protocol and document MCP configuration in Claude Code. |
| Server-side machine ID mutation as core gateway behavior | This is desktop account-switching behavior with risk. | Document as ecosystem risk; keep server focused on API proxy/account routing. |

## MVP Definition

### Launch With

- [ ] CC-01 through CC-07: official Claude Code compatibility contract and automated UAT.
- [ ] HA-01 through HA-07: high-availability per-account scheduling and sub2api correctness.
- [ ] KE-01 through KE-05: Kiro ecosystem admin improvements that support reliable operation.

### Defer

- [ ] KE-06: deeper operational safety hardening after user-facing compatibility and HA paths are stable.
- [ ] Strict external state store for multi-replica deployments.
- [ ] Desktop machine ID management.

## Roadmap Implications

1. Start with Claude Code compatibility because it defines the exact client contract Kiro-Go must satisfy.
2. Follow with high-availability routing because correctness under 10x10 concurrent sub2api calls depends on scheduler behavior, failure taxonomy, and background-job discipline.
3. Add Kiro ecosystem enhancements after the core API path is reliable, prioritizing CLI import, admin fleet operations, and observable WebSearch/MCP behavior.

## Sources

- Local codebase map: `.planning/codebase/ARCHITECTURE.md`, `.planning/codebase/INTEGRATIONS.md`, `.planning/codebase/CONCERNS.md`, `.planning/codebase/TESTING.md`.
- Local README: `README.md`.
- Local high-concurrency UAT: `docs/superpowers/uat/sub2api-kiro-opus47-account-lb-20260520144855/UAT-RESULT.md`.
- Local smoke/full-stack correctness UAT: `docs/superpowers/uat/claude-code-high-concurrency-correctness-20260520114626/UAT-RESULT.md`.
- Prior local research: `docs/superpowers/research/2026-05-19-kiro-gateway-parity-and-capacity-root-cause.md`.
- External clone: `/tmp/kiro-research-current/kiro-gateway/README.md` and tests.
- External clone: `/tmp/kiro-research-current/KiroSwitchManager/README.md`.
- Official references requested for phase planning: Anthropic Claude Code settings, Anthropic Messages streaming/tool/prompt-cache documentation, Kiro docs, Kiro CLI hooks, and Kiro MCP usage.

---
*Feature research for: Kiro-Go Claude Code official parity and high-availability routing*
*Researched: 2026-05-20*
