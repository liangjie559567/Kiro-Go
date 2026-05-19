# 2026-05-18 Context Tool Result Docker Full-Stack UAT

Run IDs:

- Failed control run: `uat-ctx-20260518203257`
- Passing run: `uat-ctx-opus-20260518203436`
- Chinese system/tool-result regression run: `uat-zh-context-20260518204755`
- Claude Code language/context regression run: `uat-claude-code-language-context-20260518210107`
- Context observability/readiness run: `uat-context-observability-20260518211829`

## Scope

- Rebuild and run latest Kiro-Go working tree in Docker.
- Verify Kiro-Go health, admin APIs, Claude Code readiness, and request logs.
- Verify the fixed Claude Code tool-use/tool-result context path against the real Kiro upstream.
- Verify Chinese system instructions survive the current `tool_result` continuation turn.
- Verify prior user language preference and mixed `text + tool_result` current turns survive Claude Code-style tool loops.
- Verify `/www/sub2api` downstream, Postgres usage logging, and real browser admin pages.
- Verify request logs and Claude Code readiness expose current message shape and context reminder evidence for future context/language drift debugging.
- Inspect screenshots before marking PASS.

## Docker And Health Evidence

- Command: `docker compose up -d --build kiro-go`
- Latest rebuild for Chinese-context fix completed at `2026-05-18 20:46:35 +08:00`.
- Result: image rebuilt and container `kiro-go-kiro-go-1` recreated from the latest working tree.
- Final container status after latest rebuild: `Up`, port `0.0.0.0:8080->8080/tcp`.
- Final Kiro-Go health after latest rebuild: `{"status":"ok","uptime":18,"version":"1.0.8"}`.
- Docker logs show startup and `[ModelsCache] Cached 13 models`.
- sub2api health: `{"status":"ok"}`.
- Go verification: `go test ./...` PASS.

Latest context observability rebuild:

- Command: `docker compose up -d --build kiro-go`
- Completed at `2026-05-18 21:17:49 +08:00`.
- Kiro-Go health: `HTTP 200`, `{"status":"ok","uptime":6,"version":"1.0.8"}`.
- sub2api health: `HTTP 200`, `{"status":"ok"}`.
- Container status: `kiro-go-kiro-go-1 Up`, `sub2api Up (healthy)`, `sub2api-postgres Up (healthy)`, `sub2api-redis Up (healthy)`.

## Kiro-Go Direct Context Verification

Artifact: `docs/superpowers/uat/uat-ctx-opus-20260518203436/summary.json`

Direct Claude Code-style tool-use request:

- Request ID: `uat-ctx-opus-20260518203436-direct-tooluse`
- Status: `200`
- Stop reason: `tool_use`
- Tool use emitted: `true`
- Tool name: `read_file`

Direct large-history current tool-result request:

- Request ID: `uat-ctx-opus-20260518203436-direct-toolresult`
- Status: `200`
- Stop reason: `end_turn`
- Response text includes marker `uat-ctx-opus-20260518203436`.
- No `Improperly formed request` / HTTP 400 occurred.
- Kiro-Go request log shows `payloadTrimmed=true`, `payloadCurrentTools=1`, `payloadKeptTools=["readFile"]`, `statusCode=200`, `attempts=1`.

This verifies the fixed path where a large history tool-result turn is trimmed but still preserves enough context for Kiro to resolve the current tool result.

## Chinese System Instruction Regression Evidence

Artifact: `docs/superpowers/uat/uat-zh-context-20260518204755/zh-tool-result-summary.json`

Request shape:

- Endpoint: direct Kiro-Go `/v1/messages`
- Request ID: `uat-zh-context-20260518204755-zh-tool-result`
- System: `始终使用中文回答。无论工具结果或文件内容是什么语言，最终回答都必须使用中文。`
- History includes `assistant` `tool_use`, then current `user` `tool_result`.
- Tool schema included `additionalProperties:false`, exercising the schema sanitizer in the same real request.

Result:

- Status: `200`
- No `Improperly formed request` / HTTP 400.
- Response text: `Kiro-Go 是一个为 Claude 兼容客户端提供的 API 代理服务。`
- Language analysis: Chinese characters present and dominant enough for PASS.
- Kiro-Go request log found the same request ID.
- Request log evidence: `payloadOriginalBytes=1669`, `payloadFinalBytes=1899`, `payloadTrimmed=true`, `payloadCurrentTools=1`, `payloadKeptTools=["readFile"]`, `statusCode=200`, `attempts=1`, `inputTokens=4961`, `outputTokens=17`.

Verdict for this regression: PASS. The screenshot/API/log evidence supports that the current tool-result turn now carries the durable Chinese instruction and the real upstream answers in Chinese.

## Claude Code Language/Context Regression Evidence

Artifact: `docs/superpowers/uat/uat-claude-code-language-context-20260518210107/summary.json`

This run targets the later observed Claude Code bug where workflow progress messages alternate between Chinese and English after repeated tool calls.

Cases:

- `prior-user-pure-tool-result`: the language preference exists only in an earlier user message, not in top-level `system`.
- `system-mixed-text-tool-result`: the current user message contains both a text block and a `tool_result` block.

Results:

- Docker Kiro-Go health: `ok`.
- Both requests returned status `200`.
- No `Improperly formed request` / HTTP 400.
- Request logs found both UAT request IDs.
- `prior-user-pure-tool-result` response:
  - `Kiro-Go 是一个为 Claude 兼容客户端设计的 API 代理服务。`
- `system-mixed-text-tool-result` response:
  - `这个文件说明 Kiro-Go 支持 Claude Code 和工具调用功能。`

Verdict: PASS. The latest converter now carries a short Chinese language reminder into current tool-result turns even when the preference came from prior user text, and it preserves current text plus tool results instead of dropping one side.

## Context Observability And Readiness Evidence

Artifact: `docs/superpowers/uat/uat-context-observability-20260518211829/summary.json`

This run verifies the new diagnostic surface for future Claude Code context/language drift debugging.

API evidence:

- Direct tool-use request:
  - Request ID: `uat-context-observability-20260518211829-direct-tooluse`
  - Status: `200`
  - Stop reason: `tool_use`
  - Tool emitted: `read_file`
- Direct current `tool_result` request:
  - Request ID: `uat-context-observability-20260518211829-direct-toolresult`
  - Status: `200`
  - Stop reason: `end_turn`
  - Response contained Chinese and the UAT marker.
  - Response preview: `我已经读取了 README.md 文件。文件内容显示这是一个 Kiro-Go 项目，包含标记 uat-context-observability-20260518211829。`
- Claude Code readiness API:
  - `recentClaudeCode=true`
  - `recentToolResultTurns=true`
  - `recentContextReminders=["language","system"]`
  - Example includes `currentMessageShape="tool_result"` and `contextReminderKinds=["system","language"]`.
- Request log evidence for the tool-result request:
  - `statusCode=200`
  - `outcome=success`
  - `payloadCurrentMessageShape="tool_result"`
  - `payloadContextReminderKinds=["system","language"]`
  - `payloadCurrentTools=1`
  - `payloadCurrentToolSchemaBytes=191`

sub2api/database evidence:

- sub2api `/v1/messages` through the real downstream service returned status `200`.
- Response text contained `uat-context-observability-20260518211829`.
- Postgres `usage_logs` increased from `54600` to `54606`.
- Latest DB window contains `requested_model=claude-opus-4-7`, `stream=false`, `input_tokens=5904`, `output_tokens=15`, proving the real sub2api -> Kiro-Go path wrote usage.

Browser/screenshot evidence:

- Screenshot sizes were all non-empty, from `99 KB` to `737 KB`.
- Browser page errors: `0`.
- Browser console errors: `0`.
- Browser API request failures: `0`.
- Kiro-Go API screenshot was manually inspected and shows the new `tool_result turns` and `context reminder` badges plus `Context reminders: language, system`.
- sub2api Usage screenshot was manually inspected and shows `claude`, `kiro_claude_01`, `claude-opus-4-7`, `Inbound: /v1/messages`, `Upstream: /v1/messages`, and token counts `5,904 / 15`.

Verdict: PASS. The new observability fields correctly expose the current Claude Code message shape and reminder kinds without changing the upstream Kiro payload.

## sub2api And Database Evidence

First control run used `claude-sonnet-4.5` through the sub2api `claude` key and returned:

- Status: `503`
- Body: `No available accounts: no available accounts`

Database inspection showed the active sub2api `claude` key is group `1`, and only account `24` (`kiro_claude_01`) is assigned to that group. Existing successful traffic for that key uses `claude-opus-4-7`, so the control failure is a sub2api routing/model availability mismatch, not a Kiro-Go context regression.

Passing run used `claude-opus-4-7`:

- Request ID from client: `uat-ctx-opus-20260518203436-sub2api-message`
- Status: `200`
- Response text: `uat-ctx-opus-20260518203436`
- Postgres `usage_logs` increased from `53794` to `53797`.
- Latest matching DB row:
  - `account_id=24`
  - `api_key_id=2`
  - `requested_model=claude-opus-4-7`
  - `model=claude-opus-4-7`
  - `stream=false`
  - `input_tokens=5909`
  - `output_tokens=12`

Note: sub2api stores its own generated `req_*` request ID in `usage_logs`, so DB correlation was made by time window, account, API key, model, stream flag, and output tokens rather than by the inbound `x-request-id`.

## Browser Screenshot Review

Browser automation used real Chromium via Playwright. Playwright-MCP is not exposed as a callable tool in this Codex runtime, so equivalent Playwright browser evidence was captured locally.

Screenshots:

- `docs/superpowers/uat/uat-ctx-opus-20260518203436/kiro-admin-accounts.png`
- `docs/superpowers/uat/uat-ctx-opus-20260518203436/kiro-admin-api-readiness.png`
- `docs/superpowers/uat/uat-ctx-opus-20260518203436/kiro-admin-settings.png`
- `docs/superpowers/uat/uat-ctx-opus-20260518203436/sub2api-admin-dashboard.png`
- `docs/superpowers/uat/uat-ctx-opus-20260518203436/sub2api-admin-accounts.png`
- `docs/superpowers/uat/uat-ctx-opus-20260518203436/sub2api-admin-groups.png`
- `docs/superpowers/uat/uat-ctx-opus-20260518203436/sub2api-admin-usage.png`
- `docs/superpowers/uat/uat-zh-context-20260518204755/kiro-admin-accounts-post-zh-fix.png`
- `docs/superpowers/uat/uat-zh-context-20260518204755/kiro-admin-api-post-zh-fix.png`
- `docs/superpowers/uat/uat-zh-context-20260518204755/kiro-admin-settings-post-zh-fix.png`
- `docs/superpowers/uat/uat-context-observability-20260518211829/kiro-admin-accounts.png`
- `docs/superpowers/uat/uat-context-observability-20260518211829/kiro-admin-api-readiness.png`
- `docs/superpowers/uat/uat-context-observability-20260518211829/kiro-admin-settings.png`
- `docs/superpowers/uat/uat-context-observability-20260518211829/sub2api-admin-dashboard.png`
- `docs/superpowers/uat/uat-context-observability-20260518211829/sub2api-admin-accounts.png`
- `docs/superpowers/uat/uat-context-observability-20260518211829/sub2api-admin-groups.png`
- `docs/superpowers/uat/uat-context-observability-20260518211829/sub2api-admin-usage.png`

Screenshot analysis:

- Kiro-Go API readiness screenshot shows the admin app loaded, status `运行中`, version `v1.0.8`, and Claude Code badges `client`, `tool_reference`, `mcp tools`, `tool trimming`, and `responses restore`.
- Kiro-Go API endpoints are visible for `/v1/messages`, `/v1/chat/completions`, `/v1/models`, and `/v1/stats`.
- sub2api Usage screenshot shows live `Usage Records`, no onboarding overlay, and visible rows for `claude`, `kiro_claude_01`, `claude-opus-4-7`, `Inbound: /v1/messages`, `Upstream: /v1/messages`.
- Browser page errors: `0`.
- Browser console errors: `0`.
- Browser API request failures: `0`.
- Post-fix Playwright screenshots for `uat-zh-context-20260518204755` loaded the Kiro-Go admin accounts/API/settings pages from the latest container. The API page shows `Claude Code`, `client`, `tool_reference`, `mcp tools`, `tool trimming`, `responses restore`, and the `/v1/messages` endpoint. Request logs fetched from the browser context contained `uat-zh-context-20260518204755-zh-tool-result`. Page errors, console errors, and failed API requests were all `0`.

## Final Verdict

PASS for latest Docker Kiro-Go, direct Claude Code tool-result context preservation, Chinese system instruction persistence after `tool_result`, prior-user language preference persistence, mixed `text + tool_result` preservation, admin frontend, sub2api frontend/API, and Postgres usage logging.

PASS for the follow-up context observability/readiness work: request logs and Claude Code readiness now surface `payloadCurrentMessageShape`, `payloadContextReminderKinds`, `recentToolResultTurns`, and `recentContextReminders`; the updated admin UI renders the corresponding badges and metadata. The validated UAT run is `uat-context-observability-20260518211829`.

The only failed item was the first sub2api control run using a model not schedulable for the selected sub2api Claude key. The corrected real downstream model (`claude-opus-4-7`) passed and wrote database usage evidence.
