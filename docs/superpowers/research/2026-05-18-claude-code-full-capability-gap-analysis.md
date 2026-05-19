# Claude Code Full Capability Gap Analysis For Kiro-Go

Date: 2026-05-18

## Goal

Make Kiro-Go feel like a complete Claude Code-compatible Anthropic API backend, while routing to Kiro accounts. The priority areas are context continuity, tool calling, model calling, MCP, streaming, and user-visible readiness/debugging.

## Official Behavior Baseline

Sources:

- Anthropic Messages API: https://docs.anthropic.com/en/api/messages
- Tool use / fine-grained tool streaming: https://docs.anthropic.com/en/docs/agents-and-tools/tool-use/fine-grained-tool-streaming
- Computer/tool agent loop: https://docs.anthropic.com/en/docs/agents-and-tools/tool-use/computer-use-tool
- Web search tool: https://docs.anthropic.com/en/docs/agents-and-tools/tool-use/web-search-tool
- Prompt caching: https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching
- Claude Code MCP: https://docs.anthropic.com/en/docs/claude-code/mcp
- Claude Code third-party gateways: https://docs.anthropic.com/en/docs/claude-code/third-party-integrations

Key requirements:

- Anthropic `system` is top-level, not a message.
- Tool use is embedded in message content blocks: assistant emits `tool_use`, user returns `tool_result`.
- Agent loops repeatedly append assistant tool_use and user tool_result until final text.
- Tool definitions live in top-level `tools`; names must be stable and valid, and prompt caching uses prefix order `tools -> system -> messages`.
- Fine-grained tool streaming uses beta `fine-grained-tool-streaming-2025-05-14`; clients may receive partial/invalid JSON if max tokens interrupts tool input.
- MCP in Claude Code is client-hosted. Claude Code manages MCP servers, scopes, OAuth, prompts, resources, and tool output limits. Default MCP output limit is 25,000 tokens; warnings appear over 10,000 tokens; `MAX_MCP_OUTPUT_TOKENS` can raise the cap.
- For non-official base URLs, Claude Code MCP Tool Search requires `ENABLE_TOOL_SEARCH=true` so `tool_reference` is sent.
- Claude Code uses `ANTHROPIC_AUTH_TOKEN` for gateway Authorization when configured.

## Kiro Gateway Comparison

Repository reviewed: https://github.com/jwadow/kiro-gateway

Strong compatibility patterns in `kiro-gateway`:

- Unified internal message model for Anthropic and OpenAI.
- Converts tool results to readable text when Kiro cannot accept structured toolResults, preserving context instead of dropping it.
- Converts orphaned `tool_result` blocks to text when the preceding assistant `tool_use` is missing.
- Merges adjacent same-role messages, including merging tool calls and tool results.
- Ensures first message is user and roles alternate before building Kiro payload.
- Sanitizes JSON Schema by removing empty `required` and all `additionalProperties` recursively.
- Validates Kiro tool-name length before upstream calls.
- Moves long tool descriptions into the system prompt and leaves a short reference in the tool definition.
- Extracts images from `tool_result` content blocks for MCP screenshot/browser tools.
- Adds truncation recovery notices only when upstream/API truncation is detected.
- Implements server-side Kiro MCP web_search by calling `/mcp` and emulating Anthropic/OpenAI server-tool response blocks.
- Has dense unit tests for consecutive assistant tool calls, multiple tool results, empty tool results, orphaned tool results, schema sanitization, images in tool output, and Responses-style session flows.

Kiro-Go already stronger than `kiro-gateway` in these areas:

- Web admin UI and account lifecycle management.
- Multi-account health-aware routing, admission gates, request logs, payload telemetry, and frontend readiness checks.
- Claude Code `tool_reference` support with deferred/materialized metadata and restored original tool names in streams.
- OpenAI Responses session restore for downstream sub2api-like callers.
- Docker/UAT artifacts with real Kiro/sub2api/Postgres evidence.
- Kiro MCP native web_search route with Anthropic server tool response support.

## Kiro-Go Gaps And Optimization Plan

### 1. Context Continuity

Current state:

- Kiro-Go now preserves top-level system and prior Chinese language preference in current tool-result turns.
- Current mixed `text + tool_result` turns now preserve both text and tool results.
- Request logs show payload sizes/trimming but not why context reminders were injected.

Gaps:

- Language/style preference detection is Chinese-specific.
- History compaction does not emit a model-visible recovery note unless `applyTruncationRecoveryNote` is triggered in narrow guard paths.
- No request-log field exposes current message shape or reminder injection.
- No durable summary of trimmed early user preferences except language-specific heuristics.

Recommended work:

1. Add `contextContinuity` metadata to request logs:
   - current message shape: `text`, `tool_result`, `text+tool_result`, `image`, `image+tool_result`;
   - reminder kind: `system`, `language`, `truncation`, `none`;
   - history pairs compacted and tool results compacted.
2. Generalize language/style reminders:
   - Chinese, English, Japanese, Korean, concise/verbose, code-comment language;
   - extract only explicit stable preferences, never arbitrary task text.
3. Add compaction recovery for trimmed high-value user instructions:
   - system prompt;
   - language/style preference;
   - current task objective;
   - tool-result adjacency note.
4. Add tests for long Claude Code sessions where early preferences are outside top-level system.

### 2. Tool Calling

Current state:

- Anthropic tools, tool_use, tool_result, tool_reference are accepted.
- Tool schema cleanup removes `additionalProperties` and empty `required`.
- Current matching tool_use is preserved during history trimming.
- Streaming restores original tool names for tool_reference.

Gaps:

- Need broader tests for multiple consecutive assistant tool_use messages followed by multiple current tool_result blocks.
- Need fallback behavior when toolResults arrive but the tool definition or preceding assistant toolUse was trimmed or absent.
- Need explicit 64-char tool name validation and user-facing error before Kiro returns vague 400.
- Need long tool description relocation into system/current context to preserve MCP tool docs while keeping Kiro schema safe.
- Need image extraction from `tool_result` content blocks for Playwright/browser MCP screenshots.

Recommended work:

1. Port/adapt `kiro-gateway` cases:
   - no tools defined but tool history present -> convert tool content to text;
   - orphaned tool_result -> preserve as text;
   - adjacent same-role messages -> merge content/tool results safely;
   - consecutive assistant tool_use -> preserve all matching tool uses.
2. Add tool-name validation with actionable 400 response:
   - max 64 chars;
   - sanitized collision diagnostics;
   - original name restoration evidence.
3. Move oversized tool descriptions into Kiro system/history/current context with a reference description.
4. Add fixture tests from real Claude Code tool_reference requests and MCP-heavy sessions.

### 3. MCP

Current state:

- Kiro-Go correctly treats Claude Code as the local MCP host.
- Kiro-Go accepts `tool_reference` when `ENABLE_TOOL_SEARCH=true` makes Claude Code send it.
- Kiro-Go can call Kiro MCP web_search for Anthropic server-side web search tools.
- Admin readiness shows recent MCP/tool_reference signals.

Gaps:

- User onboarding does not fully explain MCP scopes, resources, prompts, OAuth, and output limits.
- Request logs do not clearly distinguish client-hosted MCP tool calls from server-side Kiro MCP web_search.
- Tool-result image/screenshot support needs more complete extraction and UAT.
- MCP Tool Search deferred loading needs richer visibility: deferred count, materialized count, prompt-mentioned tools, trimmed tool names.

Recommended work:

1. Add Claude Code MCP guide in admin UI:
   - `ENABLE_TOOL_SEARCH=true`;
   - MCP remains configured in Claude Code;
   - `MAX_MCP_OUTPUT_TOKENS` guidance;
   - how to verify `tool_reference` in request logs.
2. Add request-log fields:
   - `mcpMode=client_hosted|kiro_server_web_search|none`;
   - tool reference deferred/materialized counts;
   - image tool result count;
   - large MCP output truncation flags.
3. Add Playwright/browser MCP screenshot UAT with image payload inspection.
4. Expand Kiro MCP web_search parity:
   - non-stream and stream event snapshots;
   - error-as-200 behavior compatible with Anthropic web_search;
   - regional account q-host evidence.

### 4. Model Calling

Current state:

- Model aliases and mappings exist.
- Multi-account routing, account health, admission gates, Retry-After handling, and request logs exist.
- Opus/Sonnet/Haiku and thinking suffixes are supported in code paths.

Gaps:

- Admin UI does not fully explain which accounts can serve which requested Claude Code model.
- Sub2api UAT found a real mismatch: selected API key/group could not schedule Sonnet but could schedule Opus.
- Need better model readiness by account/group and gateway alias.
- Need clearer model resolver behavior compared with `kiro-gateway` smart model resolution.

Recommended work:

1. Add model readiness matrix:
   - requested model -> mapped Kiro model -> eligible accounts -> current health/quota/admission.
2. Add `/admin/api/claude-code/model-readiness`.
3. Add request-log reason when no accounts are available:
   - model unavailable;
   - group/API-key restriction;
   - quota exhausted;
   - unhealthy;
   - admission pressure.
4. Add smoke tests for common Claude Code model names:
   - `claude-sonnet-4.5`, `claude-opus-4-7`, thinking variants, versioned aliases.

### 5. Streaming And Responses

Current state:

- Anthropic SSE streaming is supported.
- Tool-only streams and multi-tool streams have tests.
- OpenAI Responses exists and restores previous response sessions.

Gaps:

- Fine-grained tool streaming semantics are only partially emulated because Kiro upstream emits its own stream shape.
- Need explicit handling/testing for partial/invalid JSON tool input if upstream truncates tool arguments.
- Need more evidence that prompt cache accounting remains stable with tool_reference and system filtering.

Recommended work:

1. Add explicit beta flag telemetry for fine-grained tool streaming.
2. Add tests for tool input chunks, max-token interruption, and invalid JSON wrapping guidance.
3. Expand prompt cache tracker tests using official hierarchy `tools -> system -> messages`.
4. Add Responses UAT for chained function-call output under Claude Code-like tool loops.

## Priority Roadmap

P0 - Fix ongoing context drift:

- Done: current tool-result turns now preserve system and prior Chinese preference, including mixed text+tool_result.
- Next: add request-log visibility for reminder injection and current message shape.

P1 - Tool/MCP parity:

- Add orphaned tool_result fallback to text.
- Add multiple-consecutive tool_use/tool_result tests.
- Add tool_result image extraction and UAT.
- Add tool-name validation and long-description relocation.

P2 - Claude Code onboarding:

- Build an admin Claude Code capability page with setup commands, readiness checks, tool_reference visibility, MCP guidance, model readiness, and troubleshooting.

P3 - Model/readiness observability:

- Add account/model eligibility matrix and no-account failure explanations.

P4 - Advanced streaming/cache parity:

- Add fine-grained tool streaming telemetry and prompt-cache hierarchy tests.

## Acceptance Criteria For Full Claude Code Capability

- Claude Code can run long tool-heavy workflows without losing explicit language/style preference.
- Claude Code local MCP tools work through Kiro-Go with `tool_use`, `tool_result`, and `tool_reference` preserved or safely degraded to text.
- Server-side web_search works in non-stream and stream Anthropic formats.
- Large tool/MCP outputs are truncated with model-visible recovery notices, not silent context loss.
- Model routing failures are explainable from admin UI without reading server logs.
- Request logs expose enough metadata to answer: what model, what account, what tools, what was trimmed, what context reminder was injected, and whether MCP was client-hosted or Kiro-server-side.
