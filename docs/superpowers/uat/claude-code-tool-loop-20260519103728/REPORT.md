# Claude Code Tool Loop Optimization UAT

Date: 2026-05-19

## Scope

This UAT verifies the current optimization slice for the five Claude Code parity areas identified in the research note:

1. Context continuity
2. Tool calling
3. MCP/tool-reference compatibility
4. Model calling/readiness
5. Streaming and Responses/sub2api downstream continuity

Official/reference inputs used during the work:

- Claude Code environment variables: `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ENABLE_TOOL_SEARCH`, MCP output limits, gateway model discovery. Source: https://code.claude.com/docs/en/env-vars
- Claude Code LLM gateway behavior, including `/v1/models` discovery and gateway auth headers. Source: https://code.claude.com/docs/en/llm-gateway
- Anthropic tool-use contract: client tools produce `stop_reason: "tool_use"` and `tool_use` blocks; clients return `tool_result`. Source: https://platform.claude.com/docs/en/agents-and-tools/tool-use/overview
- Fine-grained tool streaming can emit partial/invalid JSON and must be handled by clients/gateways. Source: https://platform.claude.com/docs/en/agents-and-tools/tool-use/fine-grained-tool-streaming
- Compared against the open-source `jwadow/kiro-gateway` project for compatibility patterns. Source: https://github.com/jwadow/kiro-gateway

## Code Changes Verified

- Added Claude Code task lifecycle repair for the latest real `/gsd-autonomous` failure shape:
  - `TaskUpdate.status` emitted as an object such as `{"status":"in_progress"}` is flattened to the string enum expected by Claude Code.
  - `TaskOutput` aliases and scalar coercions are normalized: `task_id`/`taskID`/`id` -> `taskId`, string booleans for `block`, and string integers for `timeout`.
- Added Claude Code client-tool input repair for `Bash`, `Edit`, `MultiEdit`, `Glob`, `Grep`, `LS`, and `Write`.
- Added `LS {}` repair to `{"path":"."}`. This addresses the observed root cause where Kiro emitted a valid-looking `LS` tool call with empty input, but Claude Code's `LS` schema requires `path`.
- Added `TaskCreate tasks[]` repair. Kiro-Go now promotes the first task candidate into Claude Code's single-task `subject`, `description`, and `activeForm` fields, removes wrapper fields, and synthesizes a missing `description` from `content`, `activeForm`, or `subject`.
- Raised payload priority for Claude Code task lifecycle tools: `taskCreate`, `taskUpdate`, `taskOutput`, `taskGet`, `taskList`, and `taskStop`.
- Added boolean coercion for repaired tool inputs.
- Added structured suppressed-tool-use details to request logs, including tool id, name, reason, and bounded input summary.
- Exposed `recentSuppressedToolUses` and suppressed tool metadata through Claude Code readiness.
- Updated admin UI readiness/request log surface to show the `tool suppressed` signal.

Changed files:

- `proxy/kiro.go`
- `proxy/kiro_test.go`
- `proxy/payload_guard.go`
- `proxy/payload_guard_test.go`
- `proxy/request_log.go`
- `proxy/request_log_test.go`
- `proxy/handler.go`
- `web/index.html`

## Fresh Verification

### Tests

PASS

Command:

```sh
go test ./...
```

Result:

- `kiro-go`: ok
- `kiro-go/auth`: ok
- `kiro-go/config`: ok
- `kiro-go/pool`: ok
- `kiro-go/proxy`: ok

Regression coverage:

- `TestWrapToolUseRepairsClaudeCodeFilesystemAndShellAliases`
- `TestWrapToolUseRepairsClaudeCodeTaskCreateTasksArray`
- `TestWrapToolUseRepairsClaudeCodeTaskCreateTasksArrayWithoutDescription`
- `TestWrapToolUseRepairsClaudeCodeTaskUpdateStatusObject`
- `TestWrapToolUseRepairsClaudeCodeTaskOutputAliases`
- `TestGuardKiroPayloadPrioritizesTaskLifecycleToolsFromClaudeCodeLogShape`
- `TestRequestLogCapturesSuppressedToolUses`

### Docker

PASS

Command:

```sh
docker compose up -d --build kiro-go
```

Health evidence:

- `kiro-health-final2.json`: `{"status":"ok","version":"1.0.8"}`
- `sub2api-health-final2.json`: `{"status":"ok"}`
- `docker-ps-final2.txt`: `kiro-go-kiro-go-1` running, `sub2api` healthy, `sub2api-postgres` healthy, `sub2api-redis` healthy.
- Fresh follow-up evidence after the TaskCreate fix: `kiro-health-taskcreate-20260519123309.json`, `sub2api-health-taskcreate-20260519123309.json`, and `docker-ps-taskcreate-20260519123309.txt`.
- Latest follow-up after the TaskUpdate/TaskOutput fix:
  - `curl http://127.0.0.1:8080/health`: `{"status":"ok","version":"1.0.8"}`.
  - `curl http://127.0.0.1:18080/health`: `{"status":"ok"}`.
  - `docker ps`: `kiro-go-kiro-go-1` running, `sub2api` healthy, `sub2api-postgres` healthy, `sub2api-redis` healthy.

### Real sub2api -> Kiro-Go API Calls

PASS

Fresh non-stream request through sub2api:

- Request: `sub2api-message-request.json`
- Response: `sub2api-message-final.json`
- Evidence: returned exact marker `uat-tool-loop-20260519103728`
- Model returned: `claude-opus-4.7`
- Stop reason: `end_turn`

Fresh streaming request through sub2api:

- Request: `sub2api-stream-request.json`
- Response: `sub2api-stream-final.sse`
- Evidence: returned exact marker `uat-tool-loop-20260519103728-stream`
- SSE included `message_start`, `content_block_delta`, `message_delta`, and `message_stop`

Kiro-Go request logs:

- `kiro-request-logs-final2.json`
- Final non-stream `/v1/messages`: status `200`, outcome `success`, request id `2ed5f346-198a-412a-b665-7da56ee1fdc7`
- Final stream `/v1/messages`: status `200`, outcome `success`, request id `907c72a3-90e7-4b2d-b189-468e9fc5a127`
- Fresh follow-up through sub2api after the TaskCreate fix:
  - `sub2api-taskcreate-message.json`: returned exact marker `uat-taskcreate-20260519123309`, model `claude-opus-4.7`, `stop_reason=end_turn`.
  - `sub2api-taskcreate-stream.sse`: returned exact marker `uat-taskcreate-20260519123309-stream` with `message_start`, `content_block_delta`, `message_delta`, and `message_stop`.
  - `kiro-request-logs-taskcreate-20260519123309.json`: latest sub2api non-stream and stream requests both show `status=200`, `outcome=success`.
- Latest direct post-fix task lifecycle requests:
  - `direct-taskupdate-repair-auth-20260519125421.json`: HTTP 200, `stop_reason=tool_use`, `TaskUpdate` input delivered as `{"taskId":"1","status":"in_progress","activeForm":"执行 Phase 46 - discuss 阶段"}`.
  - `direct-taskoutput-repair-auth-20260519125421.json`: HTTP 200, `stop_reason=tool_use`, `TaskOutput` input delivered as `{"taskId":"aeea39766749e6cbe","block":false,"timeout":0}`.
  - `request-logs-after-task-fix-auth.json`: both direct task requests show `statusCode=200`, `outcome=success`, `toolUseCount=1`, and no suppressed tool names.
- Latest sub2api downstream check:
  - `sub2api-kiro-nonstream-20260519125523.json`: HTTP 200 through `/www/sub2api`, returned exact marker `SUB2API_KIRO_NONSTREAM_OK`.
  - `request-logs-after-sub2api-smoke.json`: Kiro-Go received the downstream non-stream `/v1/messages` request from sub2api and recorded `statusCode=200`, `outcome=success`.
  - `sub2api-kiro-stream-20260519125523.sse`: current fresh stream attempt returned HTTP 429 because the upstream Kiro account was temporarily rate-limited for suspicious activity. This is treated as an external account-state blocker for this single fresh stream retry, not as a tool-schema regression.

### Database Evidence

PASS

Fresh Postgres evidence:

- `db-after-final2-sub2api.txt`
- Usage row `58166`: `api_key_id=2`, `account_id=24`, `requested_model=claude-opus-4-7`, `inbound_endpoint=/v1/messages`, `upstream_endpoint=/v1/messages`, `stream=false`, `request_type=1`
- Usage row `58167`: `api_key_id=2`, `account_id=24`, `requested_model=claude-opus-4-7`, `inbound_endpoint=/v1/messages`, `upstream_endpoint=/v1/messages`, `stream=true`, `request_type=2`
- Fresh follow-up evidence: `db-after-taskcreate-sub2api-claude-20260519123309.txt`
  - Usage row `58334`: `api_key_id=2`, `account_id=24`, `/v1/messages -> /v1/messages`, `stream=false`, `request_type=1`, created `2026-05-19 12:33:59+08`.
  - Usage row `58336`: `api_key_id=2`, `account_id=24`, `/v1/messages -> /v1/messages`, `stream=true`, `request_type=2`, created `2026-05-19 12:34:17+08`.
- Latest non-stream downstream usage evidence:
  - Usage row `58484`: `api_key_id=2`, `account_id=24`, `model=requested_model=claude-sonnet-4-5-20250929`, `inbound_endpoint=/v1/messages`, `upstream_endpoint=/v1/messages`, `stream=false`, created `2026-05-19 12:55:26+08`.
  - The current stream retry was rejected by upstream before successful completion, so no successful stream usage row is expected for that retry.

This confirms `/www/sub2api` continues to perform real downstream calls through Kiro-Go and records billing/usage rows.

### Browser / Playwright Evidence

PASS with corrected routes

Fresh screenshots:

- `sub2api-admin-accounts-final.png`: real `Account Management` page, not a 404.
- `sub2api-admin-groups-final.png`: real `Group Management` page; includes `claude` group with Anthropic platform and account availability.
- `sub2api-admin-usage-final.png`: real admin `Usage Records` page; includes endpoint distribution and fresh usage rows.
- `final-browser-summary.json`: all five final screenshots matched expected page text; console, page errors, and local request failures are empty.
- Screenshot analysis follow-up: the initial fresh sub2api screenshot had a welcome modal overlay; `final-browser-uat.js` now closes that modal before capture. The final `sub2api-admin-usage-final.png` is clean and visibly shows latest `claude-opus-4-7` `/v1/messages` sync and stream rows.
- Latest screenshot analysis:
  - `kiro-admin-api-readiness-final.png` shows `toolSchemaValidation PASS`, `Last seen: 5/19/2026, 12:54:21 PM`, `recentSuppressedToolUses=false`, and schedulable accounts for `claude-sonnet-4.5`.
  - `sub2api-admin-usage-final.png` shows the fresh `claude / kiro_claude_01 / claude-sonnet-4-5-20250929` usage row with `Inbound: /v1/messages` and `Upstream: /v1/messages`.

Existing Kiro-Go screenshots remain valid:

- `kiro-admin-dashboard-final.png`: admin dashboard loads and account list renders.
- `kiro-admin-api-readiness-final.png`: readiness page shows Claude Code capability statuses, including `toolSchemaValidation PASS`, `fine-grained requested`, and `tool_result turns`.
- `kiro-admin-bottom-request-logs.png`: request log/readiness surface renders in the admin UI.

Invalid/superseded browser evidence:

- `sub2api-accounts.png` and `sub2api-groups.png` were direct visits to `/accounts` and `/groups`, which are not the admin routes and returned 404. They are not counted as PASS evidence.
- `sub2api-simple-accounts.png`, `sub2api-simple-groups.png`, and `sub2api-simple-usage.png` stayed on dashboard after ambiguous text clicks. They are superseded by `sub2api-admin-*.png`.

### Root Cause Evidence For Claude Code Auto-Pause

PASS

Observed tool stream:

- `direct-tool-auth-final.sse`
- `direct-tool-auth-final.summary.json`
- Kiro-Go returned SSE with `stopReason="tool_use"`
- Tool call: `name="LS"`
- `content_block_start` has `input={}` as the standard streamed placeholder.
- The following `input_json_delta` reconstructs to `{"path":"."}`.

Important correction:

- The earlier `direct-tool-auth.summary.json` only inspected `content_block_start.content_block.input`, so it incorrectly treated the placeholder `{}` as the final tool input.
- Re-parsing the same original SSE in `direct-tool-auth.reparsed-summary.json` reconstructs `{"path":"."}` from `input_json_delta`.
- Therefore the direct SSE stream itself is valid for Claude Code's streamed tool-use contract.

Fix retained and still useful:

- `repairLSInput` now defaults missing `path` to `"."`.
- Unit regression proves the exact malformed input is repaired before delivery to Claude Code: `toolu_ls_empty` becomes `path="."`.

Full loop proof:

- First request: `direct-tool-auth-final.sse` produced `LS {"path":"."}` and `stop_reason="tool_use"`.
- Second request: `direct-tool-result-final-request.json` returned the corresponding `tool_result`.
- Final response: `direct-tool-result-final.json` returned `stop_reason="end_turn"` and marker `uat-tool-loop-20260519103728`.
- Request log evidence: `kiro-request-logs-final2.json` shows both requests succeeded, with `payloadCurrentMessageShape="text"` for the tool-use request and `payloadCurrentMessageShape="tool_result"` for the follow-up.

Historical external blocker:

- `direct-tool-auth-postfix.summary.json` was blocked by a temporary Kiro upstream HTTP 429 during an earlier retry.
- The fresh retry is no longer blocked and supersedes that result.

### Follow-up Root Cause Evidence: TaskCreate Suppression

PASS

Observed real Claude Code request logs:

- `request-logs-debug-parallel.json`
- Several real Claude Code turns had `toolUseCount > 1`, which matches Anthropic's documented default parallel tool behavior.
- The same logs showed `suppressedToolUseNames=["TaskCreate"]`.
- Suppressed input used `TaskCreate` with a `tasks: [...]` array. Claude Code's `TaskCreate` client schema expects one task object with `subject`, `description`, and `activeForm`.
- Payload trimming also dropped `taskCreate`, `taskOutput`, and `taskUpdate` while retaining `taskGet/taskList/taskStop`.

Fix evidence:

- `direct-taskcreate.summary.json`: direct stream returned `stopReason="tool_use"`, `toolUseCount=1`, `name="TaskCreate"`, and reconstructed input `{"activeForm":"Running Phase 46 discuss","description":"Running Phase 46 discuss","subject":"Phase 46 discuss"}`.
- `kiro-request-logs-taskcreate-20260519123309.json`: direct request `direct-taskcreate-20260519123309` returned `status=200`, `outcome=success`, `toolUseCount=1`, `payloadKeptTools=["taskCreate"]`, with no suppressed tool names.

### Latest Follow-up Root Cause Evidence: TaskUpdate and TaskOutput Suppression

PASS for Kiro-Go fix; external blocker for one fresh sub2api stream retry

Observed real Claude Code request logs after the earlier TaskCreate fix:

- `request-logs-post-autonomous-debug.json`
- Request `40bc01ef-bce0-418d-88d0-eba6f5f5d5bd` suppressed `TaskUpdate` because the model emitted `status` as an object: `{"status":"in_progress"}`.
- Request `ce0936bc-b972-4b49-a482-a04e1e97996a` suppressed `TaskOutput` because the model emitted `{"task_id":"aeea39766749e6cbe","block":"false","timeout":"0"}` while Claude Code expected `taskId`, boolean `block`, and integer `timeout`.

Fix evidence:

- `TestWrapToolUseRepairsClaudeCodeTaskUpdateStatusObject` and `TestWrapToolUseRepairsClaudeCodeTaskOutputAliases` pass.
- `direct-taskupdate-repair-auth-20260519125421.json` and `direct-taskoutput-repair-auth-20260519125421.json` prove the live Docker service now delivers both task tools as valid `tool_use` blocks.
- `request-logs-after-task-fix-auth.json` shows both live requests succeeded with `toolUseCount=1` and no suppressed tool names.

Reference comparison:

- Anthropic's official Messages/tool-use contract requires `tool_use` blocks to be returned to the client and then continued with matching `tool_result` blocks; dropping invalid tool calls breaks that loop.
- Anthropic streaming can expose partial tool input through `input_json_delta`; gateways must reconstruct and validate the completed input, not treat the initial empty input placeholder as final.
- The latest `jwadow/kiro-gateway` codebase similarly keeps explicit Anthropic `tool_use`/`tool_result` models and a streaming adapter with `input_json_delta` events, plus parser tests for Kiro tool-call parsing/deduplication. That supports Kiro-Go's current direction: repair compatible client-schema drift when deterministic, suppress only genuinely unrecoverable tool input, and record suppression details for diagnosis.

## Five-Area Verdict

1. Context continuity: PASS for current message-shape logging and preserved request flow in live calls; broader preference/generalized compaction improvements remain future work.
2. Tool calling: PASS. Direct streamed `LS` reconstructs to `{"path":"."}`, the follow-up `tool_result` turn reaches final `end_turn`, `TaskCreate tasks[]` is repaired to Claude Code's single-task schema, `TaskUpdate.status` object output is flattened, `TaskOutput` aliases/types are normalized, and task lifecycle tools are prioritized during trimming.
3. MCP/tool-reference: PASS for existing readiness surfacing and Claude Code environment guidance; no new MCP screenshot-image payload UAT was added in this slice.
4. Model calling/readiness: PASS for `claude-opus-4-7` readiness and schedulable account evidence in `kiro-model-readiness-fresh.json`.
5. Streaming/downstream sub2api: PASS for the implemented Kiro-Go fix and current non-stream downstream call. Previous stream evidence remains valid, but the latest fresh stream retry is `BLOCKED_EXTERNAL_429` because upstream Kiro temporarily rate-limited the selected account.

## Final UAT Decision

PASS for the Kiro-Go tool-loop fix, with one external limitation recorded:

- The implemented optimization is correct for the identified Claude Code tool auto-pause root cause.
- The latest Docker service is running and healthy.
- `/www/sub2api` still performs real downstream non-stream calls to Kiro-Go and records Postgres usage rows.
- Browser screenshots, API responses, and database rows agree.
- Direct post-fix task lifecycle tool calls now pass for `TaskUpdate` and `TaskOutput` with no suppressed tool uses.
- The latest sub2api stream retry is blocked by upstream Kiro account HTTP 429. This is not marked as a fresh stream PASS, and it remains an account/quota state to recheck when upstream limits clear.
