# Claude Code Complete Kiro-Go Optimization Design

Date: 2026-05-18

## Goal

Make Kiro-Go a complete, practical Claude Code downstream target while keeping the local `/www/sub2api` deployment working as a real downstream after rebuild.

The primary verified route remains:

```text
sub2api /v1/messages -> Kiro-Go /v1/messages -> Kiro upstream
```

This phase modifies only `/www/Kiro-Go`. The `/www/sub2api` project is used as a rebuild and real-call compatibility gate, not as an implementation target.

## Scope

In scope:

- Claude Code user setup documentation for Kiro-Go.
- Claude Code MCP / Tool Search usage guidance.
- OpenAI Responses `previous_response_id` context restoration hardening.
- Tool call continuation and tool definition inheritance.
- Tool and `tool_reference` diagnostics for Claude Code and MCP-heavy requests.
- Request-log/admin evidence needed to diagnose Claude Code turns.
- Kiro-Go Docker rebuild and local sub2api rebuild verification.
- Real sub2api stream and non-stream `/v1/messages` smoke calls.

Out of scope:

- Modifying `/www/sub2api` source code.
- Rewriting Kiro-Go account scheduling or implementing half-open probe logic.
- Adding TLS fingerprinting, billing, or user-management features.
- Turning Kiro-Go into an MCP server.
- Large admin UI redesign.

## Hard Acceptance Criteria

- `go test ./...` passes in `/www/Kiro-Go`.
- Kiro-Go Docker image is rebuilt and the service health check passes at `http://127.0.0.1:8080/health`.
- `/www/sub2api` is rebuilt/restarted without source changes and health check passes at `http://127.0.0.1:18080/health`.
- A real non-stream sub2api `/v1/messages` request succeeds through Kiro-Go.
- A real stream sub2api `/v1/messages` request succeeds through Kiro-Go.
- Kiro-Go request logs expose enough information to correlate those calls: model, endpoint, account, attempts, first-token timing, payload/tool trimming, request id, and Claude Code/session metadata where present.
- Verification output must not print API keys or account secrets.

## Recommended Approach

Use a compatibility-closure approach for this phase.

Kiro-Go already has the critical base: Anthropic `/v1/messages`, OpenAI `/v1/responses`, `tool_reference`, tool schema repair, prompt cache tracking, payload guards, account health routing, request logs, and sub2api-oriented UAT artifacts. This phase should tighten the user-facing and protocol continuity gaps rather than mix in high-risk scheduling rewrites.

## Architecture

### 1. Claude Code Access Layer

Kiro-Go should explicitly document the supported Claude Code setup:

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
export ANTHROPIC_AUTH_TOKEN=any
export ANTHROPIC_MODEL=claude-sonnet-4.5
export ANTHROPIC_SMALL_FAST_MODEL=claude-haiku-4.5
export ENABLE_TOOL_SEARCH=true
```

The documentation must state that:

- Claude Code remains the MCP host.
- Kiro-Go receives the tools and `tool_reference` shapes emitted by Claude Code.
- MCP Tool Search is disabled by Claude Code for non-Anthropic base URLs unless `ENABLE_TOOL_SEARCH=true` is set.
- Kiro-Go does not execute local MCP servers itself.

This reduces misconfiguration and sets a clear support boundary.

### 2. Anthropic Messages Path

The `/v1/messages` path should continue to process requests in this order:

1. Parse the Anthropic envelope: `anthropic-version`, `anthropic-beta`, request IDs, Claude Code session/agent headers, user agent, and unknown top-level fields.
2. Validate request shape with Claude Code-compatible tolerance.
3. Convert Claude messages, tools, `tool_reference`, images, and tool results into a Kiro payload.
4. Run payload guarding before account health can be penalized.
5. Record original and final payload sizes, tool counts, retained/trimmed tool names, and `tool_reference` state.
6. Select an account and call Kiro.
7. Emit Anthropic-compatible JSON or SSE.
8. Update usage, cache, request log, and account statistics.

Payload, schema, and malformed-client-request failures must be classified before account selection or before account health penalties. Client-side payload issues must not make a Kiro account look unhealthy.

### 3. OpenAI Responses Continuation

The `/v1/responses` path should be hardened around stateful continuation:

- Restore context through a `previous_response_id` chain, not only a single previous response.
- Preserve the request messages for each stored response.
- When the current request contains tool outputs, restore only the previous assistant tool calls that correspond to those tool output call IDs.
- Inherit historical tools and `tool_choice` when the current request omits them.
- Prune response sessions by TTL and capacity.
- Expose session restoration decisions in request logs, without logging full message bodies or schemas.

The goal is to support multi-turn Responses/Codex-style tool loops without duplicating stale tool calls or dropping tool definitions.

### 4. Tool And MCP Diagnostics

Kiro-Go remains a protocol adapter, not an MCP runtime.

The tool/MCP phase should focus on carrying and explaining the tool surface:

- Keep support for `tools`, `tool_choice`, `tool_use`, `tool_result`, and top-level `tool_reference`.
- Preserve outward Claude Code tool names and IDs across Kiro-safe internal names.
- Prefer core Claude Code tools when payload limits force trimming:
  - `agent`, `task`
  - `todoRead`, `todoWrite`
  - `bash`, `read`, `write`, `edit`, `multiEdit`, `glob`, `grep`, `ls`
  - `webFetch`, `webSearch`
- Log retained and trimmed tool names with a capped, names-only representation.
- Log materialized and deferred `tool_reference` names/counts.
- Add a lightweight Claude Code readiness diagnostic in the admin surface or stats API. It should indicate recent evidence such as `claude-cli` user agent, Claude Code session headers, `anthropic-beta`, `tool_reference`, MCP-style tool names, and payload trimming.

This gives users a direct answer to "is Claude Code actually sending Kiro-Go the full tool/MCP surface?"

### 5. Model And Error Compatibility

This phase should not rewrite model admission or scheduling. It should keep low-risk compatibility behavior:

- Document recommended Claude Code model variables.
- Keep `/v1/models` shape compatible with sub2api.
- Preserve Anthropic/OpenAI-compatible error types.
- Preserve request IDs on success and failure paths.
- Keep existing retry/failover behavior intact.

If an error occurs during verification, classify it as one of:

- sub2api authentication, routing, or scheduling problem;
- Kiro-Go protocol or payload problem;
- Kiro-Go account/token problem;
- Kiro upstream capacity or transient network problem.

## Data Flow

### Claude Code Direct Flow

```text
Claude Code
  -> Kiro-Go /v1/messages
  -> Anthropic envelope parser
  -> Claude-to-Kiro converter
  -> payload guard and diagnostics
  -> account selection
  -> Kiro upstream
  -> Claude-compatible response or SSE
```

### Responses Continuation Flow

```text
Responses request
  -> parse payload
  -> convert to internal OpenAI request
  -> restore previous_response_id chain
  -> filter restored tool calls by current tool outputs
  -> inherit tools/tool_choice
  -> convert to Kiro payload
  -> shared guard/account/response path
  -> save response session
```

### sub2api Verification Flow

```text
smoke request
  -> sub2api:18080 /v1/messages
  -> Kiro-Go:8080 /v1/messages
  -> Kiro upstream
  -> Kiro-Go Anthropic response/SSE
  -> sub2api usage/log path
```

The smoke scripts should print status, model, request ID when available, elapsed time, and a short response excerpt. They must not print API keys.

## Testing Strategy

### Unit Tests

Add or harden tests for:

- Responses session chain restoration.
- Tool output matching to restored assistant tool calls.
- Historical tools and `tool_choice` inheritance.
- Response session TTL and capacity pruning.
- Claude Code core tool retention under payload trimming.
- Request log fields for retained/trimmed tools and `tool_reference` state.
- Request log fields for session restoration metadata.

### Local Integration Tests

Run:

```bash
go test ./...
```

Then verify Kiro-Go locally:

- Kiro-Go health at `http://127.0.0.1:8080/health`.
- `/v1/models` response shape.
- A direct Kiro-Go `/v1/messages` smoke call where credentials are available.

### Downstream Real Verification

Use `/www/sub2api` as the compatibility gate:

- Rebuild and restart sub2api from `/www/sub2api`.
- Check `http://127.0.0.1:18080/health`.
- Run a real non-stream `/v1/messages` request through sub2api.
- Run a real stream `/v1/messages` request through sub2api.
- Confirm Kiro-Go request logs include the two requests.
- Record any failures with layer classification.

## Implementation Slices

### Slice 1: Documentation And Access Experience

Files likely affected:

- `README.md`
- `README_CN.md`
- optionally `web/index.html` for a small admin help/readiness block

Deliverables:

- Claude Code setup section.
- MCP / Tool Search guidance.
- sub2api downstream compatibility note.

### Slice 2: Responses Session Continuation

Files likely affected:

- `proxy/handler.go`
- `proxy/handler_test.go`
- possibly `proxy/request_log.go`
- possibly `proxy/request_log_test.go`

Deliverables:

- Chain restoration.
- Tool-call filtering by current tool outputs.
- Tools/tool_choice inheritance preserved.
- TTL/capacity pruning.
- Request-log evidence.

### Slice 3: Tool And MCP Diagnostics

Files likely affected:

- `proxy/payload_guard.go`
- `proxy/payload_guard_test.go`
- `proxy/request_log.go`
- `proxy/request_log_test.go`
- optionally `web/index.html`

Deliverables:

- Core Claude Code tools verified as high-priority during trimming.
- Tool and `tool_reference` diagnostics visible in logs/stats/admin.
- No full tool schemas or secret payloads logged.

### Slice 4: Verification And UAT Evidence

Files likely affected:

- `docs/superpowers/uat/2026-05-18-claude-code-complete-kiro-go-optimization-uat.md`
- optional reusable smoke script under `docs/superpowers/uat/`

Deliverables:

- `go test ./...` result.
- Kiro-Go rebuild and health result.
- sub2api rebuild and health result.
- Real sub2api non-stream smoke result.
- Real sub2api stream smoke result.
- Failure classification if any step fails.

## Rollout Notes

- The worktree is already dirty. Implementation must avoid reverting unrelated changes.
- The first implementation step should inspect current diffs in touched files before editing.
- sub2api source code must remain unchanged in this phase.
- Docker rebuild and restart should happen only after unit tests pass.
- If real upstream capacity is unavailable during UAT, record the exact upstream-compatible error and show that Kiro-Go/sub2api protocol shape remains valid.

## Risks

- Preserving core Claude Code tools under payload pressure may trim less relevant MCP tools. This is acceptable for this phase because losing edit/shell/read tools is more damaging to Claude Code UX.
- Responses chain restoration can increase payload size. Payload guard remains the hard limit, and logs must explain trimming decisions.
- Real sub2api verification depends on available credentials and upstream Kiro capacity. If capacity fails, verification must still classify the layer accurately.
- Adding admin diagnostics should stay small. Large UI changes are deferred.

## Success Definition

This phase is successful when a user can configure Claude Code against Kiro-Go, understand the MCP/Tool Search requirements, run tool-heavy requests with observable tool handling, and rebuild local sub2api without breaking real calls through Kiro-Go.
