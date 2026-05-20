# Phase 1: A - Claude Code Official Compatibility - Specification

**Created:** 2026-05-20
**Ambiguity score:** 0.09 (gate: <= 0.20)
**Requirements:** 7 locked

## Goal

Kiro-Go behaves like the official Anthropic Messages API for Claude Code's real request surface, with all Kiro-Go-controlled compatibility behavior passing and any upstream-unprovable official behavior explicitly marked `PARTIAL` or `BLOCKED_BY_UPSTREAM` with evidence.

## Background

Kiro-Go is already a Go HTTP gateway with `/v1/messages`, `/v1/messages/count_tokens`, `/v1/models`, OpenAI-compatible routes, SSE streaming, multi-account routing, model mapping, prompt-cache estimation, payload guarding, request logs, and Claude Code readiness surfaces. Relevant code exists in `proxy/handler.go`, `proxy/translator.go`, `proxy/claude_sse_writer.go`, `proxy/payload_guard.go`, `proxy/cache_tracker.go`, and `proxy/request_log.go`.

Existing UAT artifacts show real Claude Code-style tool-loop and sub2api calls can succeed, and recent 10x10 sub2api Opus 4.7 tests passed after account-routing fixes. The remaining Phase 1 requirement is not to claim vague "Claude Code works", but to lock an official-compatibility contract: what endpoints and request shapes are supported, what is fully passable, what is degraded because Kiro upstream cannot prove official Anthropic parity, and what evidence is required before marking the phase complete.

## Requirements

1. **Compatibility matrix**: Kiro-Go documents and tests a Claude Code compatibility matrix for the actual Claude Code Anthropic surface.
   - Current: Readiness APIs and prior research mention many capabilities, but there is no phase-locked matrix tying endpoints, request shapes, evidence, and PASS/PARTIAL status to CC-01 through CC-07.
   - Target: A matrix covers `/v1/messages`, `/v1/messages/count_tokens`, `/v1/models`, auth headers, model aliases, stream/non-stream, `tools`, `tool_choice`, `tool_use`, `tool_result`, `tool_reference`, thinking, prompt cache controls, large context, and upstream-degraded behaviors.
   - Acceptance: The matrix gives each item a status of `PASS`, `PARTIAL`, `BLOCKED_BY_UPSTREAM`, or `FAIL`, includes evidence links or test names, and does not mark an upstream-unprovable official behavior as `PASS`.

2. **Model alias and readiness contract**: Claude model aliases used by Claude Code resolve deterministically and expose schedulability separately from model listing.
   - Current: Model mapping exists for dashed, dotted, versioned, Opus/Sonnet/Haiku, and thinking suffix forms; model readiness APIs expose requested/mapped/schedulable state, but the phase requirements do not yet specify exact observable output.
   - Target: Requested model, mapped Kiro model, thinking variant, listed model state, eligible account count, schedulable account count, cooldown/quota/rate-limit/missing-model reasons, and routing reason are visible through readiness and request logs.
   - Acceptance: Requests for representative aliases, including `claude-opus-4-7`, `claude-opus-4.7`, versioned Sonnet/Haiku forms, and `ANTHROPIC_SMALL_FAST_MODEL`-style values, produce deterministic mapped models and observable readiness fields.

3. **Tool-loop fidelity**: Claude Code tool loops preserve official request and response shapes without dropping required context or emitting invalid tool calls to strict clients.
   - Current: Translation and payload guards handle `tools`, `tool_choice`, `tool_use`, `tool_result`, `tool_reference`, tool-result images, orphaned tool results, tool description relocation, tool input repair, and suppressed tool-use logging, but this is spread across code and UAT artifacts.
   - Target: Claude Code client-tool loops can complete two-turn `tool_use` -> `tool_result` flows; tool references are materialized or deferred with logs; invalid unrepairable model-emitted tool calls are suppressed or converted to `end_turn` rather than causing a client-side schema pause.
   - Acceptance: Automated tests and real UAT prove at least one non-stream tool loop and one stream tool loop produce valid Anthropic `tool_use` blocks, consume matching `tool_result` blocks, and finish with `end_turn`; request logs show tool count, suppressed tool details, materialized/deferred references, and current message shape.

4. **Anthropic SSE contract**: Streaming output follows Anthropic-compatible event ordering for text, thinking, tool use, and errors.
   - Current: `claude_sse_writer.go` emits `message_start`, content block events, `input_json_delta`, thinking deltas, `message_delta`, `message_stop`, and stream errors; prior UAT parsed SSE successfully.
   - Target: Streams are valid for normal text, thinking/reasoning, tool use, upstream errors before the first chunk, upstream errors after headers start, and first-token retry decisions where safe.
   - Acceptance: Stream tests assert event ordering and reconstructed content/tool input for representative text, thinking, and tool-use streams; UAT includes saved SSE or parsed summaries proving `message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, and `message_stop` where applicable.

5. **Large-context payload policy**: Claude Code large context requests are guarded using official documentation and mature gateway practice without silently corrupting the current user turn.
   - Current: `payload_guard.go` trims history/tool data, enforces size limits, records original/final size, kept/trimmed tools, compacted results, and recovery notes; prior logs show payload trimming in real Claude Code requests.
   - Target: Large-context behavior follows Anthropic/Claude Code documented constraints and open-source gateway best practices: explicit configurable limits, deterministic trimming or rejection, preservation of current user input, preservation of current tool results when feasible, and full observability of any mutation.
   - Acceptance: Tests or UAT cover a realistic oversized Claude Code payload and verify the gateway either rejects with a clear compatibility error or proceeds with logged `payloadOriginalBytes`, `payloadFinalBytes`, `payloadTrimmed`, trim counts, kept/trimmed tool names, compacted tool-result counts, and a recovery note; no test may pass if current user input is silently removed.

6. **Prompt cache, thinking, and count-token disclosure**: Advanced Anthropic fields are preserved, normalized, estimated, or rejected with explicit compatibility status.
   - Current: `ClaudeRequest` accepts thinking and extra official fields; prompt-cache usage is locally estimated; `/v1/messages/count_tokens` exists as an estimate; readiness can mark exact official parity as partial.
   - Target: `cache_control`, prompt-cache warmup, thinking config, `max_tokens=0`, assistant prefill, and count-token behavior are documented as full pass only when Kiro-Go can prove official-equivalent behavior; otherwise they are exposed as Claude Code-compatible estimates or upstream-blocked partials.
   - Acceptance: Matrix/readiness entries distinguish Claude Code compatibility from official Anthropic parity for count tokens, prompt cache warmup, fine-grained tool streaming, and assistant prefill; tests verify compatible response shapes and logs include mode fields such as estimated/local/emulated when used.

7. **Real Claude Code UAT evidence**: Phase 1 cannot pass on unit tests alone; it must include real service evidence, including `/www/sub2api`.
   - Current: Existing UAT artifacts already cover tool loops, admin screenshots, readiness JSON, database usage rows, and 10x10 sub2api runs, but Phase 1 needs a single acceptance bundle tied to this SPEC.
   - Target: Final UAT runs the latest Kiro-Go code and captures stream, non-stream, tool-loop, readiness, request-log, screenshot, and `/www/sub2api` downstream evidence without modifying `/www/sub2api` source or exposing secrets.
   - Acceptance: A Phase 1 UAT report includes API responses or parsed summaries, Kiro-Go logs/request-log evidence, sub2api database or usage evidence, Playwright screenshots for Kiro-Go readiness/log surfaces and sub2api usage/account surfaces, and an explicit PASS/PARTIAL/FAIL summary where screenshots, API, logs, and database evidence agree.

## Boundaries

**In scope:**
- Claude Code's real Anthropic-compatible request surface: `/v1/messages`, `/v1/messages/count_tokens`, `/v1/models`, stream/non-stream, auth headers, model aliases, tool loops, `tool_reference`, thinking, prompt cache controls, and large context.
- Compatibility matrix with evidence-backed `PASS`, `PARTIAL`, `BLOCKED_BY_UPSTREAM`, or `FAIL` status.
- Kiro-Go-controlled behavior required for Claude Code not to pause on invalid tool schemas, malformed SSE, dropped tool results, or silent payload corruption.
- Readiness and request-log observability needed to explain model mapping, schedulability, payload policy, tool behavior, and degraded official parity.
- Real UAT through Kiro-Go and `/www/sub2api`, including API/log/database/screenshot evidence.

**Out of scope:**
- Running or managing local MCP servers inside Kiro-Go - Claude Code remains the MCP host.
- Editing `/www/sub2api` source code - sub2api is a downstream validation target for this phase.
- Guaranteeing official Anthropic parity for behavior Kiro upstream cannot prove - such behavior must be marked `PARTIAL` or `BLOCKED_BY_UPSTREAM`.
- Solving high-availability account-pool policy beyond what is needed to avoid Phase 1 UAT false failures - Phase 2 owns scheduler and 429 isolation work.
- Kiro CLI credential import, fleet admin batch operations, and WebSearch/MCP operator enhancements - Phase 3 owns ecosystem operations.
- Multi-replica distributed state, admin session hardening, and atomic config persistence - these are v2 operational hardening items.

## Constraints

- Use official Anthropic/Claude Code documentation as the reference contract for Messages API shape, streaming, tool use, prompt caching, count tokens, and Claude Code gateway configuration.
- Use mature open-source gateway practice, including `kiro-gateway`-style model resolution, payload guards, first-token/retry awareness, and broad converter/streaming tests, as design input for large-context and compatibility behavior.
- Do not advertise official `PASS` for local estimates, emulation, or upstream-unproven behavior.
- Do not read, expose, or commit runtime secrets from `data/config.json`, recovery snapshots, API keys, refresh tokens, or admin passwords.
- Keep changes sympathetic to the current Go standard-library HTTP service, existing `proxy` package organization, JSON config store, and co-located Go tests.
- Real UAT may encounter live Kiro 429 or model capacity pressure; reports must separate gateway behavior from external upstream exhaustion.

Reference inputs for planning:
- Claude Code LLM gateway configuration: https://code.claude.com/docs/en/llm-gateway
- Anthropic Messages streaming contract: https://docs.anthropic.com/claude/reference/messages-streaming
- Anthropic Count Message tokens API: https://docs.anthropic.com/en/api/messages-count-tokens
- Anthropic prompt caching behavior: https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching
- Open-source gateway comparison target: `jwadow/kiro-gateway`

## Acceptance Criteria

- [ ] Compatibility matrix exists and covers all CC-01 through CC-07 areas with evidence-backed statuses.
- [ ] Representative Claude model aliases resolve deterministically and readiness/logs expose requested, mapped, listed, and schedulable state.
- [ ] Non-stream and stream Claude Code tool loops complete `tool_use` -> `tool_result` -> `end_turn` without invalid emitted tool input or empty assistant turns.
- [ ] Anthropic SSE tests and UAT prove valid event ordering for text, thinking, tool use, and error paths.
- [ ] Large-context requests are trimmed or rejected by explicit policy, with original/final size and mutation decisions logged, and current user input preserved.
- [ ] Prompt cache, thinking, `max_tokens=0`, assistant prefill, and count-token behavior are either proven compatible or explicitly marked as estimated/emulated/partial.
- [ ] `go test ./... -count=1` passes after Phase 1 implementation.
- [ ] Latest Kiro-Go service is verified through real API calls and admin readiness/request-log evidence.
- [ ] `/www/sub2api` real downstream stream and non-stream calls through Kiro-Go succeed or any failure is attributed with evidence to external upstream exhaustion.
- [ ] Final UAT includes API/log/database/screenshot evidence and marks PASS only when evidence agrees.

## Ambiguity Report

| Dimension           | Score | Min   | Status | Notes |
|---------------------|-------|-------|--------|-------|
| Goal Clarity        | 0.93  | 0.75  | OK     | Goal locks Claude Code official surface plus honest partials. |
| Boundary Clarity    | 0.83  | 0.70  | OK     | In/out scope separates Phase 1 compatibility from Phase 2 HA and Phase 3 operations. |
| Constraint Clarity  | 0.84  | 0.65  | OK     | Official docs, open-source best practice, no secret leakage, and no false PASS are explicit. |
| Acceptance Criteria | 0.86  | 0.70  | OK     | Pass/fail checks include tests and real sub2api evidence. |
| **Ambiguity**       | 0.09  | <=0.20| OK     | Gate passed. |

Status: OK = met minimum, WARN = below minimum (planner treats as assumption)

## Interview Log

| Round | Perspective | Question summary | Decision locked |
|-------|-------------|------------------|-----------------|
| 1 | Researcher | What is the Phase 1 delivery scope? | User selected full official PASS ambition rather than only a matrix, while still requiring real UAT. |
| 1 | Researcher | How should upstream-unprovable official behavior be handled? | Mark as `PARTIAL` or `BLOCKED_BY_UPSTREAM` with clear evidence; do not hide differences. |
| 1 | Researcher | Must UAT include `/www/sub2api`? | Yes. Phase 1 PASS requires real downstream sub2api evidence. |
| 2 | Simplifier | Which official surface is in scope? | Scope is Claude Code's actual Anthropic Messages API surface, not every unrelated Anthropic API. |
| 2 | Simplifier | What is the minimum success version? | Kiro-Go-controlled matrix items must PASS; upstream-unprovable official items must be explicit partials rather than silent failures. |
| 2 | Simplifier | What large-context policy should apply? | Use official documentation and open-source gateway best practices; no silent corruption of current user input. |

---

*Phase: 01-a-claude-code-official-compatibility*
*Spec created: 2026-05-20*
*Next step: $gsd-discuss-phase 1 - implementation decisions (how to build what's specified above)*
