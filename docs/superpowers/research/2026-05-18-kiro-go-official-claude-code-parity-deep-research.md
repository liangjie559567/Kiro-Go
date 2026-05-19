# Kiro-Go Official Claude Code API Parity Deep Research

Date: 2026-05-18

## Goal

Compare current Kiro-Go with `jwadow/kiro-gateway` and current official Claude/Claude Code documentation, then identify the remaining core capabilities needed so a user can point Claude Code at Kiro-Go and get the closest practical equivalent of the official Anthropic API experience.

This research updates and narrows earlier 2026-05-18 notes. Several items that were previously recommendations are already implemented in the current Kiro-Go worktree, including `/v1/messages/count_tokens`, `/v1/models`, Claude Code compatibility/readiness APIs, request-log current-message-shape fields, context-reminder metadata, tool-reference metadata, and model discovery env guidance.

## Sources Reviewed

Official sources:

- Claude Code LLM gateway requirements: https://code.claude.com/docs/en/llm-gateway
- Claude Code environment variables: https://code.claude.com/docs/en/env-vars
- Claude Code MCP guide: https://code.claude.com/docs/en/mcp
- Claude Code tool search: https://code.claude.com/docs/en/agent-sdk/tool-search
- Claude Messages API: https://platform.claude.com/docs/en/api/messages/create
- Claude Count Tokens API: https://platform.claude.com/docs/en/api/messages-count-tokens
- Claude beta Count Tokens API: https://platform.claude.com/docs/en/api/beta/messages/count_tokens
- Fine-grained tool streaming: https://platform.claude.com/docs/en/docs/agents-and-tools/tool-use/fine-grained-tool-streaming
- Prompt caching: https://platform.claude.com/docs/en/build-with-claude/prompt-caching
- Claude Code Week 16 2026 release notes for Opus 4.7/xhigh: https://code.claude.com/docs/en/whats-new/2026-w16

Open-source source:

- `jwadow/kiro-gateway`, cloned at `/tmp/kiro-gateway-research` from https://github.com/jwadow/kiro-gateway

Local Kiro-Go files reviewed:

- `README.md`, `README_CN.md`
- `proxy/handler.go`
- `proxy/translator.go`
- `proxy/payload_guard.go`
- `proxy/request_log.go`
- `proxy/anthropic_envelope.go`
- `proxy/token_estimator.go`
- `proxy/kiro.go`
- existing research/spec/UAT artifacts under `docs/superpowers/`

## Official Claude Code Gateway Baseline

A Claude Code Anthropic-format gateway must expose at least:

- `POST /v1/messages`
- `POST /v1/messages/count_tokens`
- it must preserve or understand `anthropic-beta` and `anthropic-version`

For Claude Code model picker parity, Claude Code v2.1.129+ can query `/v1/models` when:

- `ANTHROPIC_BASE_URL` points to a gateway;
- `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1` is set;
- returned model IDs begin with `claude` or `anthropic`.

Claude Code sends gateway-observable headers:

- `X-Claude-Code-Session-Id`
- `X-Claude-Code-Agent-Id`
- `X-Claude-Code-Parent-Agent-Id`

Kiro-Go currently captures session and agent IDs, but not parent-agent ID.

For MCP/tool search:

- `ANTHROPIC_BASE_URL` pointing to a non-first-party host disables MCP Tool Search by default.
- `ENABLE_TOOL_SEARCH=true` or `auto:N` is needed when a proxy supports `tool_reference`.
- Tool search dynamically withholds tool definitions, then loads relevant tools with `tool_reference`.
- Tool search supports Sonnet 4+ and Opus 4+, not Haiku.
- `MAX_MCP_OUTPUT_TOKENS` controls large MCP output limits; image content can still be subject to the same setting.

For API-level Messages parity:

- `system` is top-level. There is no `system` role in `messages`.
- consecutive same-role user/assistant turns are valid in the official API and combined server-side.
- final assistant prefill is officially valid, but Kiro-Go currently rejects it.
- content blocks can include text, image, document, thinking, tool use, tool result, citations, search results, and beta/server-tool-specific blocks.
- `max_tokens=0` is official and used to warm prompt cache without generating content.
- count-tokens includes tools, system, messages, images, and documents.
- prompt caching order is `tools -> system -> messages`; changing tool definitions invalidates all downstream cache.

For fine-grained tool streaming:

- tools can set `eager_input_streaming=true`;
- streams emit `input_json_delta.partial_json`;
- partial or invalid JSON is expected if generation stops mid-tool-input.

## What Kiro-Go Already Does Well

Kiro-Go is stronger than `kiro-gateway` as an operator-facing gateway:

- Web admin UI, account import/export, usage tracking, request logs.
- Multi-account pool, health-aware routing, admission gates, Retry-After handling.
- `/v1/messages`, `/v1/messages/count_tokens`, `/v1/models`, `/v1/chat/completions`, `/v1/responses`.
- Claude Code compatibility/readiness endpoints that expose setup env and recent evidence.
- Request logs include model, account, retries, first-token latency, tool-reference counts, kept/trimmed tools, current message shape, and context reminder kinds.
- `tool_reference` support with deferred/materialized tool metadata and Kiro-safe internal name mapping.
- Prompt-cache estimation/tracking fields and count-token estimation.
- Native Kiro MCP web search route for Anthropic server-side web search tools.
- OpenAI Responses session restore for sub2api-like callers.
- Real local UAT artifacts for Kiro-Go/sub2api production-style flows.

## What `kiro-gateway` Still Has Worth Porting

`kiro-gateway` has less admin/runtime depth but a clean converter discipline. The most useful remaining patterns:

1. Orphaned `tool_result` fallback:
   - If a current or historical `tool_result` has no preceding assistant `tool_use`, convert it to text rather than dropping it or sending an invalid Kiro payload.
   - Kiro-Go currently drops orphaned Kiro tool messages in guard/history paths. That is safe for Kiro, but loses context. The better behavior is text preservation before payload construction.

2. No-tools-defined fallback:
   - If a client sends tool history but no current `tools`, convert tool calls/results to readable text.
   - This avoids Kiro rejection while preserving context for summarization, compaction, and resumed sessions.

3. Adjacent same-role merge:
   - Official Messages accepts consecutive same-role turns. `kiro-gateway` merges them, including tool calls and tool results.
   - Kiro-Go currently handles some Kiro alternation issues but should preserve all tool calls/results in an explicit same-role normalization step before Kiro conversion.

4. Tool-result image extraction:
   - `kiro-gateway` extracts images nested inside Anthropic `tool_result.content`, which is important for Playwright/browser MCP screenshot tools.
   - Kiro-Go extracts top-level user images, but `extractToolResultContent` only returns text. Nested images in tool results are currently not promoted to Kiro images.

5. Long tool description relocation:
   - `kiro-gateway` moves long descriptions into system prompt/tool documentation and leaves a short reference in the tool definition.
   - Kiro-Go currently truncates tool descriptions/schema descriptions for payload safety. That protects Kiro but loses tool semantics. Relocation is better than truncation where payload budget allows.

6. Tool-name validation diagnostics:
   - `kiro-gateway` explicitly validates Kiro's 64-character tool-name limit and returns actionable 400 errors.
   - Kiro-Go has Kiro-safe mapping for tool references, but should still validate externally supplied raw `tools` and expose collision/length diagnostics before upstream 400s.

## Remaining Kiro-Go Gaps For "Official API Feeling"

### P0 - Claude Code Protocol Observability Gaps

Kiro-Go should capture `X-Claude-Code-Parent-Agent-Id`.

Why it matters:

- Official Claude Code documents parent-agent attribution for nested agents.
- Request logs currently capture session and agent IDs, but nested subagent/team workflows cannot be attributed cleanly.

Recommended implementation:

- Add `ClaudeCodeParentAgentID` to `RequestLogEntry`.
- Parse `X-Claude-Code-Parent-Agent-Id` in `beginRequestLog` and `parseAnthropicEnvelope`.
- Surface it in admin log detail and readiness examples.

### P0 - Context Preservation For Orphaned Tool Results

Kiro-Go should not only drop orphaned tool result structures. It should preserve their textual content.

Why it matters:

- Claude Code long sessions and compaction can remove the preceding assistant `tool_use`.
- Official API clients often send tool result continuation turns where prior context may be pruned.
- Dropping tool results prevents the model from seeing command output, test output, screenshots notes, or MCP results.

Recommended implementation:

- Before Kiro payload build, detect `tool_result` blocks without matching prior assistant `tool_use`.
- Convert those results to text with stable labels like `[Tool Result (<id>)]`.
- Preserve images nested inside those tool results as images when supported.
- Log `payloadOrphanedToolResultsConverted`.

### P0 - Tool-Result Image/Screenshot Parity

Kiro-Go should extract images inside `tool_result.content`.

Why it matters:

- Browser/Playwright/MCP screenshot tools often return mixed text and images inside a tool result block.
- Official Messages can pass image blocks in user turns; Kiro supports images on user input.
- Current Kiro-Go text-only extraction discards the visual evidence.

Recommended implementation:

- Extend `extractClaudeUserContent` so `tool_result.content` lists are scanned for `image`/`image_url`/`input_image`.
- Append those images to current/historical user image lists.
- Add request-log field `payloadToolResultImages`.
- Add tests for Anthropic tool_result image blocks and OpenAI tool message image blocks.

### P1 - Tool Definition Semantics Under Payload Pressure

Kiro-Go should relocate long tool descriptions to system/current context before truncation.

Why it matters:

- Claude Code and MCP tool descriptions carry important usage constraints.
- Prompt caching invalidates heavily on tool definition changes; stable short tool specs plus stable system tool docs are easier to reason about.
- Blind truncation can make tool selection less accurate.

Recommended implementation:

- Set a relocation threshold before current truncation.
- Move full description to `Operator tool documentation for this session`.
- Replace tool description with `[Full documentation provided in session context under Tool: <name>]`.
- Prefer this for materialized tool references and MCP tools; keep hard truncation as a last resort.

### P1 - Official Same-Role Turn Semantics

Kiro-Go should normalize consecutive same-role turns by merging rather than relying only on Kiro history trimming.

Why it matters:

- Official Messages says consecutive same-role turns are combined.
- Claude Code/subagents/tool loops can produce adjacent assistant tool-use turns or adjacent user tool-result turns.
- Kiro requires alternating history, so the compatibility layer must preserve semantics before generating Kiro payload.

Recommended implementation:

- Add a `normalizeClaudeMessagesForKiro` stage:
  - merge adjacent user turns, preserving text, images, and all tool results;
  - merge adjacent assistant turns, preserving text and all tool uses;
  - if the first turn is assistant, either convert to prefill behavior if possible or add a minimal user anchor;
  - avoid inserting placeholders until after semantic merge.

### P1 - Assistant Prefill Compatibility

Kiro-Go currently rejects final assistant messages. Official Messages allows assistant prefill.

Why it matters:

- Some SDKs and tools use assistant prefill to constrain output.
- Claude Code may not rely heavily on it in normal coding loops, but "complete official API feeling" includes this behavior.

Recommended implementation:

- For final assistant text prefill, convert it into current user instruction: "Continue from this assistant prefill exactly..." only if this can be made reliable.
- If not reliable with Kiro upstream, return a documented compatibility error and mark capability as partial, not full.

### P1 - Count Tokens Accuracy And Cache Warmup Semantics

Kiro-Go has `/v1/messages/count_tokens`, but it is estimator-based.

Why it matters:

- Claude Code compaction depends on token estimates.
- Official count-tokens includes tools, images, documents, and beta/server-tool blocks.
- Official `max_tokens=0` can prefill prompt cache; Kiro-Go thinking validation currently allows count-token separately but `/v1/messages` needs explicit max_tokens=0 behavior.

Recommended implementation:

- Document count-tokens as estimated when routed to Kiro.
- Add per-request estimation metadata in request logs.
- Add explicit tests for images, tool_reference, documents, system list blocks, thinking config, and top-level cache_control.
- Decide whether `/v1/messages` with `max_tokens=0` should do cache-only upstream call, return a compatible zero-output response, or reject as unsupported with an accurate message.

### P1 - Fine-Grained Tool Streaming Compatibility

Kiro-Go advertises fine-grained tool streaming in compatibility output, but Kiro upstream may not truly emit official `input_json_delta.partial_json` semantics.

Why it matters:

- Official fine-grained streaming can deliver invalid/partial JSON.
- Claude Code has `CLAUDE_CODE_ENABLE_FINE_GRAINED_TOOL_STREAMING`.
- Advertising full support without event-level parity can mislead debugging.

Recommended implementation:

- Split capability into:
  - accepts `eager_input_streaming`;
  - forwards beta/header metadata;
  - emits official `input_json_delta`;
  - supports invalid/partial JSON recovery.
- Add request-log field `fineGrainedToolStreamingRequested`.
- Add SSE fixture tests for a multi-chunk tool input stream.

### P2 - New/Beta Official API Surface

Kiro-Go should tolerate and log more official fields even if it cannot implement them natively:

- `container`
- `context_management`
- `mcp_servers`
- `service_tier`
- `metadata`
- top-level `cache_control`
- `stop_sequences`
- citations/search result content blocks
- document/PDF content blocks
- server-tool usage fields
- beta flags such as `context-management-2025-06-27`, `mcp-client-*`, `code-execution-*`, `files-api-*`, `managed-agents-*`, `fast-mode-*`

Recommended behavior:

- Preserve unknown top-level fields in `Extra`.
- Log their keys in a capped request-log field.
- For unsupported content blocks, convert text-bearing blocks to text and mark unsupported visual/file blocks explicitly.
- Do not silently drop user-visible content.

### P2 - Model Capability Matrix

Kiro-Go has `/v1/models` and readiness APIs. The next step is capability correctness:

- show which Kiro account can serve each model;
- expose image/tool/thinking/web-search support per model when known;
- distinguish "listed by gateway" from "verified callable right now";
- map official names like `claude-opus-4-7`, `claude-sonnet-4-6`, `claude-haiku-4-5`, versioned variants, and thinking/effort variants.

This is especially important because Claude Code model discovery only imports IDs beginning with `claude` or `anthropic`.

### P2 - Prompt Cache Parity

Kiro-Go tracks/estimates prompt cache usage, but official cache behavior is richer:

- top-level automatic `cache_control`;
- block-level `cache_control`;
- TTL `5m` and `1h`;
- four breakpoint limit;
- hierarchy `tools -> system -> messages`;
- thinking blocks cannot be explicitly cache-controlled but can be cached as prior assistant content;
- usage semantics distinguish cache read/create/input tokens.

Recommended implementation:

- Keep current compatibility as "cache metadata accepted and estimated".
- Add warning/observability when users expect official cache writes that Kiro cannot guarantee.
- Strengthen tracker tests around hierarchy invalidation when tool definitions or web-search toggles change.

## Updated Priority Roadmap

P0:

- Add parent-agent ID capture.
- Convert orphaned `tool_result` to text before Kiro payload build.
- Extract images from `tool_result.content`.

P1:

- Relocate long tool descriptions instead of truncating first.
- Merge adjacent same-role turns with tool/images preservation.
- Validate tool names and collisions before upstream calls.
- Clarify `max_tokens=0` and count-token estimator semantics.
- Split fine-grained tool streaming capability into honest sub-capabilities.

P2:

- Expand unknown official field/content-block tolerance and logging.
- Build model readiness/capability matrix by account and requested model.
- Deepen prompt cache parity tests and UI messaging.

## Acceptance Criteria For "Complete Claude Code On Kiro-Go" Practical Parity

- Claude Code can use Kiro-Go with:
  - `ANTHROPIC_BASE_URL`
  - `ANTHROPIC_AUTH_TOKEN` or `ANTHROPIC_API_KEY`
  - `ANTHROPIC_MODEL`
  - `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1`
  - `ENABLE_TOOL_SEARCH=true` or `auto:N`
- `/v1/messages`, `/v1/messages/count_tokens`, `/v1/models`, streaming, request IDs, and Claude Code headers work.
- Tool-heavy sessions preserve tool outputs even when prior tool-use messages were compacted.
- Browser/MCP screenshots returned in tool results remain visible to the model.
- Tool search/deferred tools preserve original client-facing names in both request handling and response streams.
- The admin UI can answer:
  - which Claude Code session/agent/parent-agent sent the request;
  - what model was requested and what Kiro model/account served it;
  - which tools were kept, trimmed, deferred, or materialized;
  - whether tool results/images/context reminders were preserved;
  - whether failures were client-payload, routing/admission, account/token, quota, or upstream capacity.
- Unsupported official features degrade explicitly, not silently.

## Bottom Line

Kiro-Go is already ahead of `kiro-gateway` in gateway operations, multi-account routing, admin observability, Claude Code setup/readiness, request logs, and downstream UAT. The remaining parity work is concentrated in converter fidelity: preserve every meaningful official Messages content shape that Kiro cannot natively represent, especially orphaned tool results, nested tool-result images, same-role turn merging, long tool documentation, and beta/unknown-field visibility.

