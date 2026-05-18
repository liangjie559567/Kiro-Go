# Claude Code Full Official Parity Optimization Design

Date: 2026-05-18

## Goal

Bring Kiro-Go closer to the complete official Anthropic API experience when used from Claude Code, while preserving the existing real downstream `/www/sub2api` integration.

This phase covers P0, P1, and P2 from the deep parity research:

- protocol fidelity for Claude Code tool-heavy sessions;
- official Messages API field/content tolerance;
- request-log/admin observability;
- model capability/readiness visibility;
- Docker production rebuild;
- real `sub2api -> Kiro-Go -> Kiro` non-stream and stream calls;
- browser and database UAT evidence.

## Scope

In scope:

- Modify only `/www/Kiro-Go`.
- Keep `/www/sub2api` source untouched; use it only as a real downstream validation target.
- Preserve existing Kiro-Go uncommitted work by reading current diffs before editing touched files.
- Add tests before behavior changes.
- Produce UAT artifacts with screenshots, API responses, database counts, and analysis.

Out of scope:

- Changing `/www/sub2api` source files.
- Resetting or cleaning sub2api PostgreSQL/Redis data.
- Replacing account scheduling with a new architecture.
- Implementing Kiro-Go as an MCP server.
- Claiming full official parity for upstream features Kiro cannot actually support; unsupported features must degrade explicitly and visibly.

## Current Constraints

Current runtime state observed before planning:

- `kiro-go-kiro-go-1` is running on `8080`.
- `sub2api` is running on `18080`.
- `sub2api-postgres` and `sub2api-redis` are healthy.
- `/www/Kiro-Go` and `/www/sub2api` both contain existing uncommitted changes.

Implementation must not revert unrelated work.

## Architecture

### 1. Claude Protocol Normalization Layer

Add a pre-Kiro normalization stage for Claude Messages requests.

Responsibilities:

- Merge adjacent same-role messages according to official Messages behavior.
- Preserve text, images, tool uses, and tool results during merges.
- Track assistant `tool_use` IDs seen before each user `tool_result`.
- Convert orphaned `tool_result` blocks to readable text instead of dropping context.
- Extract images nested inside `tool_result.content` and promote them to Kiro user images.
- Preserve current system/language/context reminders already implemented.

This stage should run before `ClaudeToKiro` builds Kiro history/current message structures, so Kiro payload guarding does not need to recover lost semantics later.

### 2. Tool Definition Compatibility Layer

Improve handling of tool definitions before payload guarding.

Responsibilities:

- Validate raw client tool names before upstream calls.
- Report actionable 400 errors for names that cannot be represented safely.
- Detect sanitized-name collisions between explicit tools and `tool_reference` materializations.
- Relocate long tool descriptions to session context before truncation.
- Replace relocated descriptions with short stable references in the Kiro tool spec.
- Keep payload-guard truncation as a fallback when relocation still leaves payload too large.

This should preserve MCP tool semantics better than direct truncation.

### 3. Official Field And Content Tolerance

Kiro-Go should accept and log more official Anthropic fields even when Kiro cannot implement them natively.

Fields to tolerate and log:

- `container`
- `context_management`
- `mcp_servers`
- `service_tier`
- `metadata`
- `stop_sequences`
- top-level or block-level `cache_control`
- beta/server-tool fields

Content blocks to tolerate:

- `document`
- `search_result`
- `citations`
- server-tool result blocks
- unknown text-bearing blocks

Behavior:

- Convert text-bearing unsupported blocks to text.
- Add explicit compact notices for unsupported non-text blocks.
- Preserve unknown top-level keys in existing `Extra` handling.
- Add capped request-log metadata for unknown official keys and unsupported content blocks.
- Do not silently drop user-visible content.

### 4. Claude Code Observability

Extend request logs and readiness APIs.

New request-log metadata:

- `claudeCodeParentAgentId`
- `payloadOrphanedToolResultsConverted`
- `payloadToolResultImages`
- `payloadUnsupportedContentBlocks`
- `payloadUnknownOfficialFields`
- `fineGrainedToolStreamingRequested`
- `fineGrainedToolStreamingMode`
- `modelReadinessReason`

Admin UI should surface the new fields in:

- Claude Code readiness panel;
- request log compact rows when relevant;
- request log detail view.

Readiness examples should include session, agent, parent agent, current message shape, context reminder kinds, tool-reference state, and unsupported/degraded feature markers.

### 5. Model Capability Matrix

Add a model readiness/capability API for admin use.

It should show:

- requested model alias;
- mapped Kiro model;
- generated thinking variant if applicable;
- available accounts;
- healthy/cooldown/rate-limited/quota-exhausted state;
- whether each account currently lists the model;
- whether image/tool/thinking/web-search support is known or inferred;
- reason when a model is listed by Kiro-Go but not currently schedulable.

This matrix is diagnostic; it must not rewrite routing.

### 6. Fine-Grained Tool Streaming Truthfulness

Kiro-Go should distinguish accepted configuration from true official event parity.

Capabilities should be split into:

- beta/header accepted;
- `eager_input_streaming` accepted;
- official `input_json_delta.partial_json` emitted;
- invalid/partial JSON recovery behavior.

If Kiro-Go cannot produce true official partial JSON chunks from upstream, readiness should mark this as partial rather than fully supported.

## Data Flow

Claude Code direct:

```text
Claude Code
  -> /v1/messages
  -> parse envelope and headers
  -> normalize Claude messages
  -> tolerate/log official fields and unsupported blocks
  -> convert to Kiro payload
  -> relocate tool docs
  -> payload guard
  -> account routing
  -> Kiro upstream
  -> Claude-compatible JSON/SSE
  -> request logs/readiness
```

sub2api downstream:

```text
sub2api /v1/messages
  -> Kiro-Go /v1/messages
  -> Kiro upstream
  -> Kiro-Go response/SSE
  -> sub2api response/logs/database
```

Browser/admin UAT:

```text
Playwright browser
  -> Kiro-Go admin
  -> Claude Code readiness/request logs/model matrix
  -> sub2api admin dashboard/accounts/groups/usage
  -> screenshots + console/page error capture
```

## Implementation Slices

### Slice 1: Protocol Metadata

Files likely affected:

- `proxy/anthropic_envelope.go`
- `proxy/request_log.go`
- `proxy/request_log_test.go`
- `proxy/handler.go`
- `web/index.html`

Deliverables:

- Capture `X-Claude-Code-Parent-Agent-Id`.
- Record fine-grained tool streaming request state.
- Record unknown official field keys.
- Surface these fields in admin readiness/logs.

### Slice 2: Claude Message Normalization

Files likely affected:

- `proxy/translator.go`
- `proxy/translator_test.go`
- `proxy/payload_guard.go`
- `proxy/payload_guard_test.go`

Deliverables:

- Adjacent same-role merge with text/image/tool preservation.
- Orphaned `tool_result` conversion to text.
- Tool-result image extraction.
- Metrics for converted orphan results and extracted tool-result images.

### Slice 3: Tool Definition Semantics

Files likely affected:

- `proxy/translator.go`
- `proxy/translator_test.go`
- `proxy/payload_guard.go`
- `proxy/payload_guard_test.go`
- `proxy/handler.go`

Deliverables:

- Tool-name validation and sanitized collision diagnostics.
- Long description relocation into session context.
- Request-log evidence for relocated/truncated tool docs.

### Slice 4: Official Field And Content Tolerance

Files likely affected:

- `proxy/translator.go`
- `proxy/translator_test.go`
- `proxy/anthropic_envelope.go`
- `proxy/token_estimator.go`
- `proxy/token_estimator_test.go`

Deliverables:

- Document/search/citation/server-tool/unknown block tolerance.
- Token estimator coverage for tolerated fields/content.
- Request-log unsupported block summaries.

### Slice 5: Model Capability Matrix

Files likely affected:

- `proxy/handler.go`
- `proxy/handler_test.go`
- `web/index.html`

Deliverables:

- Admin API for Claude/model readiness.
- UI display integrated into the existing Claude Code readiness area.
- Tests for available, unavailable, unhealthy, and alias/thinking cases.

### Slice 6: Real Environment Verification

Files likely affected:

- `docs/superpowers/uat/...`
- optional UAT helper scripts under `docs/superpowers/uat/`

Deliverables:

- `go test ./...` result.
- Kiro-Go Docker rebuild and health evidence.
- sub2api rebuild/restart evidence without source edits or database reset.
- Direct Kiro-Go smoke.
- sub2api non-stream and stream real calls.
- Playwright browser evidence for Kiro-Go and sub2api admin pages.
- Database counts and key API/log evidence.
- Screenshot analysis section that explains why screenshots are correct before marking PASS.

## Testing Strategy

Use TDD for behavior changes:

1. Add a focused failing test.
2. Run it and confirm the expected failure.
3. Implement the minimal code.
4. Run the focused test.
5. Run related package tests.
6. Run `go test ./...` before Docker/UAT.

Required regression tests:

- Parent-agent ID captured from headers.
- Unknown official fields are preserved/logged.
- Adjacent user messages merge text/images/tool results.
- Adjacent assistant messages merge text/tool uses.
- Orphaned `tool_result` becomes text and is not dropped.
- Nested `tool_result.content` image becomes Kiro image.
- Long tool description is relocated and referenced.
- Tool-name limit and collision errors are actionable.
- Fine-grained streaming request is logged as partial/full accurately.
- Model readiness explains no-account/no-model/unhealthy states.

## UAT Requirements

UAT must create a new artifact directory under:

```text
docs/superpowers/uat/uat-full-official-parity-YYYYMMDDHHMMSS/
```

Required evidence:

- `summary.json`
- `fullstack-uat.js`
- Kiro-Go health JSON
- Kiro-Go `/v1/models` JSON
- Kiro-Go request-log JSON
- sub2api health JSON
- sub2api model/count-token/message smoke JSON
- database pre/post counts
- Playwright screenshots:
  - Kiro-Go Claude Code readiness
  - Kiro-Go request logs/detail
  - Kiro-Go model readiness matrix
  - sub2api dashboard
  - sub2api accounts
  - sub2api groups
  - sub2api usage/logs

Screenshots must be analyzed before PASS:

- no blank pages;
- no visible JavaScript error state;
- expected live data is visible;
- readiness/log fields show the new metadata after smoke requests;
- sub2api pages still show existing data;
- database counts remain stable unless requests legitimately add log/usage records.

## Verification Commands

Expected local verification sequence:

```bash
go test ./...
docker compose up -d --build kiro-go
curl -fsS http://127.0.0.1:8080/health
cd /www/sub2api/deploy
docker compose -f docker-compose.yml -f docker-compose.current.yml up -d --build sub2api
curl -fsS http://127.0.0.1:18080/health
```

Then run real smoke calls and Playwright browser UAT. API keys and account secrets must not be printed in logs or artifacts.

## Risks And Mitigations

- Risk: preserving unsupported content increases payload size.
  - Mitigation: cap converted notices and keep existing payload guard.

- Risk: tool description relocation duplicates large text.
  - Mitigation: relocate once, use short references in tool specs, then let guard enforce hard limits.

- Risk: sub2api rebuild accidentally resets data.
  - Mitigation: use the known two-file compose command, record database counts before and after, do not run `down -v`.

- Risk: claiming unsupported official features as full support.
  - Mitigation: split readiness into exact sub-capabilities and mark partial support explicitly.

- Risk: browser screenshots look superficially correct but hide data/API failures.
  - Mitigation: pair screenshot analysis with API JSON, console/page errors, and database evidence.

## Acceptance Criteria

- All new behavior has regression tests.
- `go test ./...` passes.
- Kiro-Go Docker rebuild succeeds and health is OK.
- sub2api rebuild/restart succeeds and health is OK.
- Real sub2api non-stream and stream `/v1/messages` calls succeed through Kiro-Go.
- Playwright browser UAT captures and analyzes Kiro-Go and sub2api pages.
- Database pre/post counts are recorded and explained.
- UAT artifact is updated with PASS only after screenshots/API/database evidence agree.

