# Kiro-Go Claude Code Context And Gateway Comparison

Date: 2026-05-18

## Sources Reviewed

- `hj01857655/kiro-account-manager` README and repository structure.
- `jwadow/kiro-gateway` README, `kiro/converters_anthropic.py`, `kiro/converters_core.py`, `kiro/payload_guards.py`, `kiro/truncation_recovery.py`, `kiro/mcp_tools.py`, and converter tests.
- Current Kiro-Go translator, payload guard, request log, admin UI, and Docker UAT evidence.

## Product Capability Comparison

### Kiro Account Manager

Strong areas:

- Desktop-first account operations: import/export, refresh, validation, tagging, grouping, remote logout.
- Kiro IDE integration: account switching, model/proxy/MCP/Steering/Skills/Hooks/Custom Agents/Powers sync.
- Automation: token refresh, quota-aware account switching, machine ID binding/reset.
- Productized gateway claims: Anthropic Messages, OpenAI Responses, Chat Completions, streaming, Claude Code/Codex/Cursor/Continue/Cline setup, prompt caching, token estimation, request logs, live observability.

Implication for Kiro-Go:

- Kiro-Go already has a strong web admin and multi-account runtime, but can improve user onboarding by adding a Claude Code readiness/setup page that explains the full capability matrix and exposes exact environment variables, tool-search status, model mappings, and recent Claude Code session health.
- Kiro-Go should add first-class management for Kiro-local ecosystem config only if it wants to compete with desktop account managers. For API gateway use, the higher value is reliability, context continuity, request logs, and UAT evidence.

### Kiro Gateway

Strong areas:

- Unified converter layer for OpenAI and Anthropic messages.
- Explicit support for Anthropic `/v1/messages`, OpenAI chat/responses, streaming, multi-account retry, model normalization, vision, web search, and function calling.
- Tool correctness tests for consecutive assistant tool calls, multiple tool results, orphaned tool result conversion, empty tool results, and MCP screenshot image extraction.
- Schema sanitizer removes empty `required` and all `additionalProperties`.
- Long tool descriptions are moved into system prompt with a short tool description reference.
- Truncation recovery inserts model-visible notices only when truncation occurs.
- MCP web search path calls Kiro MCP `/mcp` directly and emulates Anthropic/OpenAI streaming responses.

Implication for Kiro-Go:

- Kiro-Go already matches or exceeds parts of this surface: web admin, account health routing, request logs, `tool_reference`, Claude Code readiness, payload guard telemetry, OpenAI Responses session restore, and native Docker UAT artifacts.
- Kiro-Go should adopt the converter discipline: treat language/context reminders, tool results, images, and truncation notices as structured compatibility behavior with focused tests.
- Kiro-Go should expand MCP/tool tests to include screenshots/images inside `tool_result`, multiple consecutive assistant tool uses, and mixed text plus tool results.

## Current Context Bug Root Cause

Observed symptom in real Claude Code:

- User explicitly asks for Chinese.
- During Claude Code workflows, intermediate progress messages switch between Chinese and English after tool calls.
- Recent request logs show large Claude Code payloads: roughly 250KB to 320KB original, trimmed to 70KB to 146KB final, with 16 tools.

Root causes found:

- Kiro-Go previously carried top-level Anthropic `system` only as synthetic history. Kiro has no equivalent top-level system field, and long histories are trimmed.
- The first fix only injected system context into pure current `tool_result` turns.
- Real Claude Code workflows can preserve the language preference as an earlier user message, not a top-level `system`.
- Real current turns may be mixed text plus `tool_result`. The old branch used the text as current content and skipped the tool-result continuation/reminder path.

Fix implemented:

- Detect explicit Chinese response preference in user messages and preserve it as a short current-turn reminder for tool-result continuations.
- For current turns with both text and tool results, include both text and `Tool results:` continuation.
- Prepend safe `Operator instructions for this session:` reminders to current tool-result turns, avoiding spoofable wrappers such as `System:`, `<system>`, or `API system field`.
- Preserve current matching `tool_use` when trimming large history with current tool results.

## Kiro-Go Priority Improvements

1. Context continuity hardening:
   - Generalize language/style preference extraction beyond Chinese.
   - Add request-log fields for current message content class: normal text, pure tool result, mixed text/tool result, images, and reminder injected.
   - Add truncation recovery notes when history compaction removes semantically relevant turns.

2. Tool calling parity:
   - Add tests for multiple consecutive assistant tool_use messages followed by multiple tool_result blocks.
   - Add tests for mixed text plus tool_result Anthropic blocks.
   - Add tests for tool_result image blocks from Playwright/MCP screenshot tools.
   - Add tool-name length validation with clear client-facing errors.

3. MCP support:
   - Keep Claude Code as MCP host for local tools, but document this explicitly in UI.
   - Expand server-side Kiro MCP web_search support with request/response debug evidence and SSE parity tests.
   - Add request-log metadata showing server-side MCP calls versus client-hosted MCP tool calls.

4. Model calling:
   - Continue model normalization and account-health routing.
   - Surface model availability by account/group in admin API so Claude Code users can see why a model is unschedulable.
   - Add readiness checks for Opus/Sonnet/Haiku aliases and thinking variants.

5. Claude Code onboarding:
   - Add a dedicated Claude Code page or panel:
     - base URL and auth token setup;
     - `ENABLE_TOOL_SEARCH=true`;
     - supported endpoint matrix;
     - recent Claude Code session status;
     - tool_reference/materialized/deferred tool counts;
     - payload trimming and context reminder status;
     - common troubleshooting for 400 malformed, 503 no account, and context drift.

## Verdict

Kiro-Go is already competitive as a multi-account API gateway and has stronger web observability than `kiro-gateway`. The main gap for Claude Code is not endpoint coverage; it is protocol-faithful context continuity under long tool-heavy workflows. The current fix addresses the immediate Chinese/context drift bug. The next best improvement is to make context reminders and truncation recovery observable and generalized.
