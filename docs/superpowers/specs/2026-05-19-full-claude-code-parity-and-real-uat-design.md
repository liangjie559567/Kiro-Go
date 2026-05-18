# Full Claude Code Parity And Real UAT Design

Date: 2026-05-19

## Goal

Complete the full Claude Code/Anthropic API parity optimization track for Kiro-Go while preserving the real downstream `/www/sub2api` integration.

The user-selected scope is the full option set:

- implement remaining high-value official Anthropic Messages API compatibility behavior;
- improve Claude Code tool/MCP reliability, especially cases that can pause Claude Code after invalid tool parameters;
- expose routing, model, tool, and degraded-capability decisions in admin observability;
- rebuild and run the latest Kiro-Go code in Docker;
- verify `/www/sub2api` can still make real calls through Kiro-Go;
- use real browser Playwright-MCP evidence, API evidence, database evidence, and screenshot analysis before marking UAT as pass.

## Non-Negotiable Constraints

- Modify only `/www/Kiro-Go`.
- Do not edit `/www/sub2api` source files.
- Do not run `docker compose down -v` or otherwise clear downstream database/Redis volumes.
- Preserve unrelated existing local changes.
- Do not commit unrelated untracked UAT/research artifacts unless they are produced for this phase.
- Do not print API keys, access tokens, refresh tokens, admin passwords, or account secrets in logs, screenshots, or UAT artifacts.
- Use tests before behavior changes where feasible.
- A capability that Kiro upstream cannot truly support must be labeled `PARTIAL`, not advertised as full support.

## Current Context

Kiro-Go already has substantial Claude Code compatibility:

- `/v1/messages`, `/v1/messages/count_tokens`, `/v1/models`, `/v1/chat/completions`, and `/v1/responses`;
- multi-account routing, health-aware account pool, Retry-After handling, and request logs;
- Claude Code readiness APIs and admin UI;
- `tool_reference` support and Kiro-safe tool-name mapping;
- same-role merge, orphaned `tool_result` preservation, tool-result image extraction, long tool description relocation, parent-agent metadata, official extra-field telemetry, and context reminder metadata in the current worktree;
- a recent fix path for invalid Claude Code tool parameters by validating tool-use input before sending it back to strict clients.

The current worktree also has existing modified files and untracked research/UAT artifacts. Implementation must inspect diffs before touching files and keep phase changes scoped.

## Design Direction

Use a layered full-parity program rather than a single large patch. Each layer should be independently testable and should feed the final Docker/sub2api/browser UAT.

### Layer 1: Protocol Core

Bring Anthropic Messages request handling closer to official behavior where Kiro can support or safely degrade it.

Required behavior:

- Preserve top-level `system` semantics and existing Claude Code system/context reminders.
- Merge adjacent same-role turns before Kiro conversion, preserving text, images, tool uses, and tool results.
- Convert orphaned `tool_result` blocks to stable text instead of dropping context.
- Extract images nested in `tool_result.content` and promote them to Kiro user images.
- Convert unsupported text-bearing blocks to text.
- Record unsupported official/non-text blocks in request logs.
- Accept and log official extra fields such as `container`, `context_management`, `mcp_servers`, `service_tier`, `metadata`, `stop_sequences`, and `cache_control`.
- Define assistant prefill compatibility behavior:
  - support safe final text prefill by converting it into an explicit continuation instruction when reliable;
  - otherwise return a documented compatibility error and expose readiness as `PARTIAL`.
- Define `max_tokens=0` behavior:
  - either return a compatible zero-output response with clear cache-warmup limitation, or perform a safe upstream cache-only request if Kiro supports it;
  - request logs must reveal the chosen behavior.
- Improve count-token coverage and disclosure:
  - include tools, tool references, system blocks, images, documents, thinking config, and unsupported blocks in estimates where feasible;
  - mark counts as estimated, not official exact counts, unless backed by a true upstream token counter.

### Layer 2: Tools, MCP, And Streaming

Prevent Claude Code tool loops from pausing because Kiro-Go emitted invalid tool calls or misleading stream metadata.

Required behavior:

- Validate model-emitted tool-use input against the original client tool schema before returning it to Claude Code.
- Cover key JSON Schema constraints used by Claude Code tools:
  - `required`, `type`, `enum`, `anyOf`, `oneOf`, `allOf`;
  - `additionalProperties`;
  - arrays: `minItems`, `maxItems`, item schemas;
  - strings: `minLength`, `maxLength`, `pattern`;
  - numbers: `minimum`, `maximum`, `exclusiveMinimum`, `exclusiveMaximum`.
- Repair safe known Claude Code aliases where existing code already has strong intent, such as Read, TaskCreate, TaskUpdate, and TodoWrite.
- Drop unrepairable invalid tool calls before sending them to Claude Code.
- If no valid tool call is emitted in a stream, return `stop_reason:"end_turn"`, not `tool_use`.
- Preserve original client tool names in responses after Kiro-safe internal name mapping.
- Validate and diagnose tool-name length and sanitized-name collisions before upstream calls.
- Relocate long tool descriptions into session context before truncating them from Kiro tool definitions.
- Support `tool_reference` visibility:
  - deferred count;
  - materialized count;
  - kept/trimmed tools;
  - materialized names restored in emitted tool uses.
- Preserve MCP screenshot/image tool results.
- Distinguish client-hosted MCP tools from Kiro server-side web search.
- Split fine-grained tool streaming capability into:
  - beta/header accepted;
  - `eager_input_streaming` accepted;
  - official `input_json_delta.partial_json` emitted;
  - partial/invalid JSON recovery behavior.

### Layer 3: Model And Routing Readiness

Add operator-visible explanations for model availability and routing behavior without replacing the existing account scheduler.

Required behavior:

- Add or complete a model readiness matrix that shows:
  - requested model alias;
  - mapped Kiro model;
  - thinking variant;
  - eligible accounts;
  - healthy, cooldown, rate-limited, quota-exhausted, disabled, or missing-model state;
  - whether the account currently lists that model;
  - support evidence for tools, images, thinking, and web search when known.
- Expose clear no-account/no-model reasons in request logs and admin APIs.
- Keep routing behavior conservative and compatible with current account pool logic.

### Layer 4: Admin Observability

Expose parity decisions and degradation states in the existing Kiro-Go admin UI.

Required surfaces:

- Claude Code readiness panel:
  - setup environment variables;
  - session, agent, and parent-agent evidence;
  - tool-search/tool-reference evidence;
  - fine-grained streaming state;
  - estimated token-count disclosure;
  - unsupported or partial capability markers.
- Request log rows/detail:
  - current message shape;
  - context reminder kinds;
  - tool references/deferred/materialized/trimmed;
  - orphaned tool results converted;
  - tool-result images;
  - relocated tool descriptions;
  - unsupported content blocks;
  - unknown official fields;
  - invalid tool-use suppression if implemented in request-log metadata.
- Model readiness UI:
  - account/model matrix;
  - routing reason;
  - failures grouped by no account, no model, health, quota, rate limit, or admission pressure.

The UI must remain dense and operational, not marketing-oriented.

### Layer 5: Real Docker, sub2api, Database, And Playwright-MCP UAT

Final validation must use the latest built code and real services.

Required sequence:

1. Rebuild and restart Kiro-Go Docker service from `/www/Kiro-Go`.
2. Verify Kiro-Go health endpoint and admin endpoint.
3. Verify `/www/sub2api` stack remains healthy.
4. Configure or confirm sub2api still routes to Kiro-Go without changing `/www/sub2api` source.
5. Execute real downstream calls:
   - sub2api non-stream `/v1/messages`;
   - sub2api stream `/v1/messages`;
   - at least one tool-capable or tool-schema request where safe;
   - model readiness/API checks.
6. Query database evidence from sub2api/PostgreSQL:
   - usage or request counters before and after;
   - account/channel/group evidence where available;
   - no destructive DB operations.
7. Use Playwright-MCP in a real browser to inspect:
   - Kiro-Go admin dashboard;
   - Kiro-Go Claude Code readiness/API/model/request-log surfaces;
   - sub2api dashboard;
   - sub2api accounts;
   - sub2api usage/logs;
   - any relevant group/channel screens.
8. Capture screenshots and analyze them.
9. Write UAT artifacts under `docs/superpowers/uat/<timestamp>/`:
   - screenshots;
   - API response JSON/status files;
   - sanitized database query output;
   - Playwright summary;
   - final UAT report with per-capability `PASS`, `PARTIAL`, or `FAIL`.

## Acceptance Criteria

Code and tests:

- `go test ./... -count=1` passes.
- Targeted regression tests cover invalid tool parameters, schema constraints, stream stop reason, protocol normalization, count-token behavior, and readiness metadata.

Runtime:

- Latest Docker-built Kiro-Go service is running and healthy.
- `/www/sub2api` remains running and healthy.
- sub2api can make real non-stream and stream calls through Kiro-Go.
- No source files under `/www/sub2api` are modified.

Evidence:

- API evidence proves the relevant endpoints work.
- Database evidence proves downstream calls are recorded or usage changes as expected.
- Playwright-MCP screenshots prove admin/readiness/log/model and sub2api pages render correctly.
- Screenshot analysis is included and must match the actual screenshots.
- Final UAT status is only `PASS` when screenshots, API responses, and database evidence agree.

## Risks And Mitigations

- **Scope risk:** Full parity is large. Mitigation: implement by layers and keep each layer testable.
- **Upstream Kiro limitation risk:** Some official Anthropic behavior may not be possible. Mitigation: mark `PARTIAL`, provide exact reason, and avoid false claims.
- **Claude Code strict tool validation risk:** Invalid tool calls can pause the client. Mitigation: validate/repair/drop before response emission and use `end_turn` when no valid tool call remains.
- **Downstream disruption risk:** sub2api must keep working. Mitigation: use it only as a validation target, do not edit its source, do not clear volumes, capture before/after health and DB evidence.
- **Secret leakage risk:** Screenshots/logs may reveal credentials. Mitigation: sanitize outputs, avoid dumping env files, redact headers/tokens, and keep screenshots focused on operational status.

## Implementation Planning Notes

The implementation plan should not bundle all work into one commit. It should split at minimum into:

1. protocol and request-log parity;
2. tool schema validation and stream stop-reason behavior;
3. assistant prefill and `max_tokens=0` behavior;
4. model readiness API/UI;
5. admin observability polish;
6. Docker/sub2api/API/database/Playwright-MCP UAT.

Each slice should include targeted tests and a clear verification command.
