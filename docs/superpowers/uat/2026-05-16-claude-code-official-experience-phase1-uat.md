# Claude Code Official Experience Phase 1 UAT

Date: 2026-05-16

## Scope

- Kiro-Go verification-first phase for Claude Code official API experience.
- Hard compatibility gate: `/www/sub2api -> Kiro-Go -> Kiro` must remain genuinely callable through the local sub2api route.
- Focus areas: token estimator coverage, Claude SSE compatibility, Claude Code envelope/log metadata, and real downstream sub2api smoke coverage.
- This UAT does not change routing, service wiring, or local sub2api/Kiro configuration.

## Focused Go Verification

Run:

```bash
go test ./proxy -run 'TestEstimateClaudeRequestInputTokensIncludesToolReferencesAndToolCacheControl|TestEstimateClaudeRequestInputTokensIncludesThinkingBudget|TestClaudeSSEWriter|TestClaudeCode2143WireFixtureParsesAndPreservesCompatibilityFields|TestClaudeCodeToolReferenceFixtureParses|TestRequestLogCapturesClaudeCodeCompatibilityMetadata' -count=1 -v
```

Expected:

- Token estimator tests pass.
- Claude SSE writer golden tests pass.
- Claude Code fixture parsing tests pass.
- Request log compatibility metadata tests pass.

## Full Go Verification

Run:

```bash
go test ./...
```

Expected:

- All packages pass.
- No regression breaks existing Kiro-Go proxy, admin, config, or translator behavior.

## sub2api Real Downstream Smoke

Preconditions:

- Kiro-Go is reachable through the existing local service path.
- sub2api is reachable at `http://127.0.0.1:18080` unless `SUB2API_BASE` is set.
- `/tmp/sub2api_claude_key` contains the local sub2api Claude API key unless `SUB2API_KEY_FILE` is set.
- The selected model is available through sub2api and Kiro. Default: `claude-sonnet-4.5`.
- The real route `/www/sub2api -> Kiro-Go -> Kiro` must be used; do not substitute a mock or alternate endpoint for this gate.

Run:

```bash
node docs/superpowers/uat/claude-code-sub2api-smoke.js
```

Optional overrides:

```bash
SUB2API_BASE=http://127.0.0.1:18080 \
SUB2API_MODEL=claude-sonnet-4.5 \
SUB2API_KEY_FILE=/tmp/sub2api_claude_key \
SUB2API_SMOKE_OUT=/www/Kiro-Go/docs/superpowers/uat/sub2api-smoke \
node docs/superpowers/uat/claude-code-sub2api-smoke.js
```

Expected:

- `/v1/models` returns HTTP 200 through sub2api.
- `/v1/messages/count_tokens` returns HTTP 200 with positive `input_tokens`.
- Non-stream `/v1/messages` returns the exact generated marker.
- Stream `/v1/messages` returns the exact generated marker and includes `message_stop`.
- The script writes a JSON artifact under `SUB2API_SMOKE_OUT`.
- The script exits nonzero if models or count_tokens fail, if sync or stream output differs from the marker, or if stream output lacks `message_stop`.

## Optional sub2api 100x10 Regression

Run this for scheduler, admission, error-mapping, or stream behavior changes:

```bash
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js sync 100 10 claude-sonnet-4.5 phase1-sync-$(date +%Y%m%d%H%M%S)
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js stream 100 10 claude-sonnet-4.5 phase1-stream-$(date +%Y%m%d%H%M%S)
```

Expected:

- No protocol errors.
- Content correctness is preserved.
- Stream responses include `message_stop`.
- Any 429/529/concurrency failure is attributable to explicit sub2api or Kiro-Go admission limits, not malformed Anthropic-compatible responses.

## Results

Executed against the real local downstream path:

```text
/www/sub2api at http://127.0.0.1:18080 -> Kiro-Go -> Kiro
client api key: sub2api api_keys.name='claude' / id=2 / group_id=1
upstream account: accounts.name='kiro_claude_01' / id=24 / platform=anthropic / type=apikey / status=active / schedulable=true / concurrency=12
model: claude-sonnet-4.5
```

### Real downstream smoke

Command:

```bash
node docs/superpowers/uat/claude-code-sub2api-smoke.js
SUB2API_SMOKE_RUN_ID=post-100x10-callable-$(date +%Y%m%d%H%M%S) node docs/superpowers/uat/claude-code-sub2api-smoke.js
```

Artifacts:

- Initial account smoke: `docs/superpowers/uat/sub2api-smoke/kiro-claude-01-real-smoke.json`
- Post-load callable smoke: `docs/superpowers/uat/sub2api-smoke/post-100x10-callable-20260517010141.json`

Result:

- `/v1/models`: HTTP 200, 31 models.
- `/v1/messages/count_tokens`: HTTP 200, positive input tokens.
- Non-stream `/v1/messages`: HTTP 200, exact marker returned.
- Stream `/v1/messages`: HTTP 200, exact marker returned, `message_stop` present, `parseErrorCount=0`.
- Post-load callable smoke also passed after the 200-request load test, confirming the local `/www/sub2api` deployment remained callable.

### Real 100x10 content and latency load

Commands:

```bash
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js sync 100 10 claude-sonnet-4.5 phase1-real-sync-20260517005259
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js stream 100 10 claude-sonnet-4.5 phase1-real-stream-20260517005448
```

Artifacts:

- Sync summary: `docs/superpowers/uat/sub2api-100x10-2026-05-16/phase1-real-sync-20260517005259-summary.json`
- Sync per-request JSONL: `docs/superpowers/uat/sub2api-100x10-2026-05-16/phase1-real-sync-20260517005259.jsonl`
- Stream summary: `docs/superpowers/uat/sub2api-100x10-2026-05-16/phase1-real-stream-20260517005448-summary.json`
- Stream per-request JSONL: `docs/superpowers/uat/sub2api-100x10-2026-05-16/phase1-real-stream-20260517005448.jsonl`

Summary:

| Mode | Total | Concurrency | HTTP 200 | Correct marker | Warnings | Failed | Avg | P50 | P90 | P95 | P99 | Max |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| sync | 100 | 10 | 100 | 100 | 0 | 0 | 8987 ms | 3002 ms | 19902 ms | 20009 ms | 20503 ms | 20721 ms |
| stream | 100 | 10 | 100 | 100 | 0 | 0 | 8990 ms | 2669 ms | 20106 ms | 20852 ms | 21042 ms | 21054 ms |

Per-request validation:

- Both JSONL files contain exactly 100 rows.
- Every request returned the exact generated marker.
- Stream requests all completed with `message_stop`.
- No response content matched the warning patterns for prompt leakage, Kiro/system-prompt text, or Claude Code artifacts.

### Database verification

Commands:

```bash
docker exec sub2api-postgres psql -U sub2api -d sub2api -c \
"select id,name,platform,type,status,schedulable,concurrency from accounts where name='kiro_claude_01';"

docker exec sub2api-postgres psql -U sub2api -d sub2api -c \
"select id,name,group_id,status from api_keys where name='claude';"

docker exec sub2api-postgres psql -U sub2api -d sub2api -c \
"select stream, count(*) total, min(duration_ms) min_ms, round(avg(duration_ms)) avg_ms, max(duration_ms) max_ms, sum(input_tokens) input_tokens, sum(output_tokens) output_tokens from usage_logs where api_key_id=2 and account_id=24 and requested_model='claude-sonnet-4.5' and created_at between timestamptz '2026-05-17 00:52:50+08' and timestamptz '2026-05-17 00:56:30+08' group by stream order by stream;"
```

Results:

| stream | total | min_ms | avg_ms | max_ms | input_tokens | output_tokens |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| false | 100 | 1668 | 8975 | 20714 | 413500 | 1500 |
| true | 100 | 1742 | 8936 | 21044 | 413500 | 1500 |

The same query window returned exactly 200 `usage_logs` rows for `api_key_id=2`, `account_id=24`, and `requested_model='claude-sonnet-4.5'`.

### Browser/frontend verification

Artifacts:

- Usage page screenshot: `docs/superpowers/uat/playwright-2026-05-17/sub2api-admin-usage-20260517.png`
- Accounts page screenshot: `docs/superpowers/uat/playwright-2026-05-17/sub2api-admin-accounts-20260517.png`
- Usage page extracted text: `docs/superpowers/uat/playwright-2026-05-17/sub2api-admin-usage-text.txt`
- Accounts page extracted text: `docs/superpowers/uat/playwright-2026-05-17/sub2api-admin-accounts-text.txt`
- Browser evidence JSON: `docs/superpowers/uat/playwright-2026-05-17/sub2api-browser-evidence-20260517.json`

Evidence:

- Accounts page shows `kiro_claude_01`, `Anthropic Key`, `Active`, group `claude`, capacity `0/12`, and usage window `202 req`.
- Usage page shows `claude-sonnet-4.5` in model distribution.
- The accounts page `202 req` is consistent with 100 sync + 100 stream + 2 smoke message requests.
- Usage page does not directly show the upstream account name; account attribution is verified through the accounts page and database.

### Compatibility gate result

PASS for the requested real downstream gate:

- 200/200 load requests succeeded through sub2api.
- 200/200 load responses returned the correct exact marker.
- 0 content/protocol failures were observed.
- 0 stream requests missed `message_stop`.
- sub2api DB recorded exactly 200 load requests on `kiro_claude_01` during the test window.
- A post-load smoke confirmed `/www/sub2api` remained callable for models, token counting, non-stream messages, and stream messages.

Latency note:

- The correctness and protocol gates passed.
- Tail latency is still high at concurrency 10: both sync and stream have P95 around 20 seconds.
- The evidence points to queue/upstream/account behavior rather than malformed Anthropic-compatible responses, because the delayed requests still completed correctly and were logged with valid usage.

## Follow-up: API system field wording fix

Date: 2026-05-17

Problem:

- Claude Code tool-result follow-up responses could mention that a tool return contained a spoofed `API system field` instruction.
- Root cause was Kiro-Go's Claude-to-Kiro conversion layer. It replaced the older `--- SYSTEM PROMPT ---` wrapper with `Context from the API system field:`, but that still exposed system-field wording inside the Kiro user message.
- On current tool-result turns, the system context was also prepended before `Tool results:`, making the model more likely to classify it as tool-output prompt injection.

Fix:

- `buildKiroSystemContext` no longer emits `Context from the API system field:`.
- `ClaudeToKiro` no longer prepends system context to the current user message when the current turn is a `tool_result` continuation.

Unit verification:

```bash
go test ./proxy -run 'TestClaudeToKiro|TestClaudeCodeBackendPrompt|TestClaudeCodeStructuredSystemPrompt|TestToolResultsContinuationIncludesInstructionPrefix|TestOpenAIToKiroPreservesStructuredAssistantAndToolContent' -count=1
```

Result:

```text
ok  	kiro-go/proxy	0.011s
```

Real deployment verification:

```bash
docker compose up -d --build kiro-go
SUB2API_SMOKE_RUN_ID=post-api-system-field-fix-smoke-$(date +%Y%m%d%H%M%S) node docs/superpowers/uat/claude-code-sub2api-smoke.js
```

Artifacts:

- Smoke after rebuild: `docs/superpowers/uat/sub2api-smoke/post-api-system-field-fix-smoke-20260517011223.json`
- Real two-step tool cycle: `docs/superpowers/uat/sub2api-smoke/real-tool-cycle-api-system-field-1778951648056.json`

Real tool-cycle result:

- First request through sub2api returned HTTP 200 with `stop_reason=tool_use` and tool `get_project_status`.
- Second request returned the real `tool_result` for the generated `tool_use.id`.
- Second response returned HTTP 200, exact marker `real-tool-cycle-api-system-field-1778951648056-marker`, `stop_reason=end_turn`.
- `forbiddenMention=false` for `API system field`, `system field`, `伪装`, `夹带`, `SYSTEM PROMPT`, and `x-anthropic-billing-header`.

DB verification:

```text
api_key_id=2 account_id=24 requested_model=claude-sonnet-4.5
01:12:26 sync smoke: HTTP/logged, duration 2200 ms
01:12:27 stream smoke: HTTP/logged, duration 1680 ms
01:14:09 tool cycle first call: duration 1830 ms
01:14:11 tool cycle second call: duration 1989 ms
```

Note:

- One synthetic direct `tool_result` request that did not first use a real Kiro-generated `tool_use.id` returned 502 with upstream `Improperly formed request`.
- The valid verification path is the two-step real tool cycle above, because Claude Code tool result turns are tied to a prior assistant `tool_use`.

## Follow-up: Full Claude Code feature matrix

Date: 2026-05-17

Scope:

- Verify the rebuilt local deployment through sub2api, using `api_keys.name='claude'` and upstream account `kiro_claude_01`.
- Cover the high-value Claude Code paths that can be verified reliably against the real upstream: discovery, token count, sync, stream, system prompt handling, tool use, tool result continuation, and `tool_reference` acceptance.

Artifact:

- `docs/superpowers/uat/sub2api-smoke/full-feature-matrix-1778952052474.json`

Results:

| Check | Status | Duration | Result |
| --- | ---: | ---: | --- |
| `/v1/models` | 200 | 38 ms | 31 models |
| `/v1/messages/count_tokens` | 200 | 12 ms | `input_tokens=5` |
| sync exact marker | 200 | 1850 ms | exact marker, `stop_reason=end_turn`, no forbidden wording |
| stream exact marker | 200 | 1939 ms | exact marker, `message_stop`, no parse errors, no forbidden wording |
| system prompt without wrapper | 200 | 1755 ms | exact marker, no `API system field` wording |
| tool cycle first call | 200 | 2005 ms | real `tool_use`, tool `get_project_status` |
| tool cycle second call | 200 | 1603 ms | returned marker from real `tool_result`, no forbidden wording |
| `tool_reference` accept | 200 | 1871 ms | accepted and completed, no forbidden wording |

Forbidden wording check:

- The matrix checked responses for `API system field`, `system field`, `伪装`, `夹带`, `SYSTEM PROMPT`, `x-anthropic-billing-header`, `Anthropic's official CLI`, and `Claude Code`.
- All real response checks had `forbiddenMention=false`.

DB verification:

```text
created_at >= 2026-05-17 01:20:40+08
api_key_id=2
account_id=24
requested_model=claude-sonnet-4.5
total /v1/messages rows: 6
stream_count: 1
nonstream_count: 5
input_tokens: 26598
output_tokens: 96
```

Core Go verification:

```bash
go test ./proxy -run 'TestClaudeToKiro|TestClaudeCode|TestClaudeSSEWriter|TestEstimateClaudeRequestInputTokensIncludesToolReferencesAndToolCacheControl|TestEstimateClaudeRequestInputTokensIncludesThinkingBudget|TestHandleClaudeStreamToolUseStartsWithMessageStart|TestHandleClaudeStreamToolReferenceRestoresOriginalToolName|TestToolResultsContinuationIncludesInstructionPrefix|TestOpenAIToKiroPreservesStructuredAssistantAndToolContent|TestClaudeUpstreamErrorsMapToAnthropicErrorTypes|TestClaudeErrorSetsRequestIDAndRetryAfter|TestBuildAnthropicModelsResponseGeneratesThinkingVariants|TestAnthropicModelsResponseIncludesAliasesWithoutExtraFields' -count=1
```

Result:

```text
ok  	kiro-go/proxy	0.045s
```

Full Go test caveat:

- `go test ./...` still fails in the current dirty worktree on `TestHandleClaudeNativeWebSearchAccepts20260209ToolType` with `TempDir RemoveAll cleanup: unlinkat ... directory not empty`.
- That failure is the pre-existing unrelated TempDir cleanup issue observed before this fix; it is not a failure of the `API system field` fix or the real sub2api feature matrix.

Coverage boundary:

- Real upstream verification covered end-to-end callable behavior and a real two-step tool cycle.
- Exact SSE event ordering, tool input JSON delta chunking, `tool_reference` sanitized-name restoration, error mappings, retry headers, and payload trimming are better covered by deterministic Go tests because the live upstream cannot reliably force those edge cases.

## Follow-up: Web search TempDir cleanup fix

Date: 2026-05-17

Problem:

- `go test ./...` intermittently failed on `TestHandleClaudeNativeWebSearchAccepts20260209ToolType` with:

```text
TempDir RemoveAll cleanup: unlinkat ... directory not empty
```

Root cause:

- The test used `config.Init(filepath.Join(t.TempDir(), "config.json"))`.
- The web-search request path records account usage through `pool.UpdateStats`.
- `pool.UpdateStats` persists account stats asynchronously with `go config.UpdateAccountStats(...)`.
- The test returned immediately after response assertions, so Go could start removing `t.TempDir()` while the async config write was still creating/writing `config.json`.
- The neighboring `TestHandleClaudeNativeWebSearchUsesKiroMCP` already waited for the same async stats write; this test was missing that synchronization.

Fix:

- `TestHandleClaudeNativeWebSearchAccepts20260209ToolType` now waits until `config.GetAccounts()[0].RequestCount > 0`, with a one-second timeout, before returning.

Verification:

```bash
go test ./proxy -run '^TestHandleClaudeNativeWebSearchAccepts20260209ToolType$' -count=20 -v
go test ./proxy
go test ./...
```

Results:

```text
ok  	kiro-go/proxy	0.211s   # target test repeated 20 times
ok  	kiro-go/proxy	0.934s
ok  	kiro-go/proxy	0.880s   # from go test ./...
```

The previous full-test caveat above is now resolved.

## Follow-up: P0-A system history carrier and Claude Code prompt preservation

Date: 2026-05-17

Scope:

- Stop placing Anthropic `system` content directly in the current Kiro user message.
- Carry system instructions as a stable synthetic Kiro history pair:
  - user: `Operator instructions for this session:`
  - assistant: `Understood. Following these instructions for the rest of the conversation.`
- Keep Claude Code durable guidance such as `# Tone and style`, `# Doing tasks`, `# Using your tools`, and project memory/CLAUDE.md text.
- Strip volatile transport/noise fields such as `x-anthropic-billing-header`, `<thinking_mode>`, `gitStatus:`, `Recent commits:`, and the pure Claude Code transport identity line.
- Preserve `/www/sub2api -> Kiro-Go -> Kiro` real downstream callability.

Changed files:

- `proxy/translator.go`
- `proxy/translator_test.go`

Regression tests added:

- `TestClaudeToKiroCarriesSystemPromptAsSyntheticHistoryPair`
- `TestClaudeCodePromptPreservesToolGuidanceAndStripsVolatileNoise`
- `TestClaudeCodeStructuredSystemPromptPreservesUsefulSections`

TDD red check:

```text
go test ./proxy -run 'TestClaudeToKiroCarriesSystemPromptAsSyntheticHistoryPair|TestClaudeCodePromptPreservesToolGuidanceAndStripsVolatileNoise|TestClaudeCodeStructuredSystemPromptPreservesUsefulSections' -count=1

# initial result:
proxy/translator_test.go:241:32: undefined: kiroSystemAcknowledgement
FAIL	kiro-go/proxy [build failed]
```

Go verification:

```text
go test ./proxy -run 'TestClaudeToKiroCarriesSystemPromptAsSyntheticHistoryPair|TestClaudeCodePromptPreservesToolGuidanceAndStripsVolatileNoise|TestClaudeCodeStructuredSystemPromptPreservesUsefulSections' -count=1
ok  	kiro-go/proxy	0.020s

go test ./proxy -count=1
ok  	kiro-go/proxy	1.761s

go test ./... -count=1
?   	kiro-go	[no test files]
?   	kiro-go/logger	[no test files]
ok  	kiro-go/auth	0.012s
ok  	kiro-go/config	0.018s
ok  	kiro-go/pool	0.009s
ok  	kiro-go/proxy	1.785s
```

Deployment:

```text
docker compose up -d --build kiro-go
Kiro-Go health: HTTP 200 {"status":"ok","uptime":10,"version":"1.0.8"}
sub2api health: HTTP 200 {"status":"ok"}
```

Real sub2api smoke:

Artifact:

- `docs/superpowers/uat/sub2api-smoke/p0a-system-history-smoke-20260517020307.json`

Results:

| Check | Status | Duration | Result |
| --- | ---: | ---: | --- |
| `/v1/models` | 200 | 39 ms | 31 models |
| `/v1/messages/count_tokens` | 200 | 16 ms | `input_tokens=30` |
| sync exact marker | 200 | 2198 ms | exact marker, `stop_reason=end_turn` |
| stream exact marker | 200 | 2545 ms | exact marker, `message_start` through `message_stop`, no parse errors |

Real system/Claude Code prompt smoke:

Artifact:

- `docs/superpowers/uat/sub2api-smoke/p0a-system-real-1778954608903.json`

Result:

```text
HTTP 200
durationMs=1800
text=p0a-system-real-1778954608903-ok
correct=true
forbidden=[]
usage.input_tokens=4202
usage.output_tokens=13
```

Forbidden wording checked:

- `API system field`
- `SYSTEM PROMPT`
- `END SYSTEM PROMPT`
- `x-anthropic-billing-header`
- `Anthropic's official CLI`
- `gitStatus`
- `Recent commits`

sub2api routing evidence:

```text
sub2api logs:
sticky.account_selected selected_account_id=24 account_name=kiro_claude_01
path=/v1/messages model=claude-sonnet-4.5 stream=false status_code=200 latency_ms=1769
path=/v1/messages model=claude-sonnet-4.5 stream=true status_code=200 latency_ms=2541
```

Database evidence:

```text
Recent usage_logs rows after deployment:
id=40944 api_key_id=2 account_id=24 model=claude-sonnet-4.5 requested_model=claude-sonnet-4.5 inbound=/v1/messages upstream=/v1/messages stream=false duration_ms=1765
id=40939 api_key_id=2 account_id=24 model=claude-sonnet-4.5 requested_model=claude-sonnet-4.5 inbound=/v1/messages upstream=/v1/messages stream=true  duration_ms=2535
id=40936 api_key_id=2 account_id=24 model=claude-sonnet-4.5 requested_model=claude-sonnet-4.5 inbound=/v1/messages upstream=/v1/messages stream=false duration_ms=2187
```

Browser evidence:

- `docs/superpowers/uat/playwright-2026-05-17/sub2api-post-p0a-headless-20260517.png`

Verdict:

- PASS for the P0-A translator slice.
- The local deployment remains callable through `/www/sub2api` using account `kiro_claude_01`.
- Current Kiro-Go request construction no longer emits the old `--- SYSTEM PROMPT ---` wrapper and no longer places system text into the current user turn.
- Claude Code system guidance is preserved after stripping volatile transport metadata.

## Follow-up: P0-B Kiro HTTP transport concurrency tuning

Date: 2026-05-17

Scope:

- Increase Kiro upstream HTTP connection pool headroom for concurrent Claude Code sessions.
- Add explicit dial timeout/keepalive and response header timeout so upstream stalls fail deterministically instead of waiting on default transport behavior.
- Preserve existing proxy behavior and HTTP/2 attempts for direct upstream connections.

Changed files:

- `proxy/kiro.go`
- `proxy/kiro_test.go`

Transport configuration after change:

```text
DialContext:           net.Dialer{Timeout: 10s, KeepAlive: 30s}
MaxIdleConns:          200
MaxIdleConnsPerHost:   50
MaxConnsPerHost:       100
IdleConnTimeout:       90s
ResponseHeaderTimeout: 60s
ExpectContinueTimeout: 1s
ForceAttemptHTTP2:     true for direct upstream; false for explicit proxy
Proxy:                 explicit proxy URL or environment proxy fallback
```

Regression test added:

- `TestBuildKiroTransportConcurrencyAndTimeouts`

TDD red check:

```text
go test ./proxy -run '^TestBuildKiroTransportConcurrencyAndTimeouts$' -count=1

--- FAIL: TestBuildKiroTransportConcurrencyAndTimeouts (0.00s)
    kiro_test.go:79: expected global idle pool of 200, got 100
FAIL
```

Go verification:

```text
go test ./proxy -run 'TestBuildKiroTransport|TestInitKiroHttpClientKeepsShortRestTimeout' -count=1
ok  	kiro-go/proxy	0.005s

go test ./proxy -count=1
ok  	kiro-go/proxy	0.871s

go test ./... -count=1
?   	kiro-go	[no test files]
?   	kiro-go/logger	[no test files]
ok  	kiro-go/auth	0.006s
ok  	kiro-go/config	0.012s
ok  	kiro-go/pool	0.006s
ok  	kiro-go/proxy	0.901s
```

Deployment:

```text
docker compose up -d --build kiro-go
Kiro-Go health: HTTP 200 {"status":"ok","uptime":10,"version":"1.0.8"}
sub2api health: HTTP 200 {"status":"ok"}
```

Real sub2api smoke:

Artifact:

- `docs/superpowers/uat/sub2api-smoke/p0b-transport-smoke-20260517021146.json`

Results:

| Check | Status | Duration | Result |
| --- | ---: | ---: | --- |
| `/v1/models` | 200 | 38 ms | 31 models |
| `/v1/messages/count_tokens` | 200 | 9 ms | `input_tokens=28` |
| sync exact marker | 200 | 2202 ms | exact marker, `stop_reason=end_turn` |
| stream exact marker | 200 | 1717 ms | exact marker, `message_start` through `message_stop`, no parse errors |

sub2api routing evidence:

```text
sub2api logs:
sticky.account_selected selected_account_id=24 account_name=kiro_claude_01 stream=false status_code=200 latency_ms=2199
sticky.account_selected selected_account_id=24 account_name=kiro_claude_01 stream=true  status_code=200 latency_ms=1715
```

Database evidence:

```text
id=41018 api_key_id=2 account_id=24 model=claude-sonnet-4.5 requested_model=claude-sonnet-4.5 inbound=/v1/messages upstream=/v1/messages stream=true  duration_ms=1710
id=41016 api_key_id=2 account_id=24 model=claude-sonnet-4.5 requested_model=claude-sonnet-4.5 inbound=/v1/messages upstream=/v1/messages stream=false duration_ms=2194
```

Verdict:

- PASS for the P0-B transport tuning slice.
- The deployed Kiro-Go remains callable through `/www/sub2api` using `kiro_claude_01`.
- This change does not by itself prove lower P95 under 100x10 load; it removes an avoidable connection-pool bottleneck and adds deterministic timeout behavior. A fresh 100x10 comparison run is still needed to quantify latency change.

## Follow-up: P0-B post-transport 100x10 latency comparison

Date: 2026-05-17

Scope:

- Re-run 100 non-stream and 100 stream requests at concurrency 10 after the Kiro HTTP transport tuning.
- Verify every request response content is correct.
- Compare latency against the pre-transport-tuning `phase1-real-*` 100x10 run.
- Confirm `/www/sub2api` usage logs route through `kiro_claude_01`.

Commands:

```bash
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js sync 100 10 claude-sonnet-4.5 p0b-post-transport-sync-202605170215
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js stream 100 10 claude-sonnet-4.5 p0b-post-transport-stream-202605170218
```

Artifacts:

- `docs/superpowers/uat/sub2api-100x10-2026-05-16/p0b-post-transport-sync-202605170215-summary.json`
- `docs/superpowers/uat/sub2api-100x10-2026-05-16/p0b-post-transport-sync-202605170215.jsonl`
- `docs/superpowers/uat/sub2api-100x10-2026-05-16/p0b-post-transport-stream-202605170218-summary.json`
- `docs/superpowers/uat/sub2api-100x10-2026-05-16/p0b-post-transport-stream-202605170218.jsonl`

Correctness:

| Mode | Total | Concurrency | HTTP 200 | Correct marker | Contains marker | Warnings | Failed | Stream `message_start` missing | Stream `message_stop` missing |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| sync | 100 | 10 | 100 | 100 | 100 | 0 | 0 | n/a | n/a |
| stream | 100 | 10 | 100 | 100 | 100 | 0 | 0 | 0 | 0 |

Latency comparison:

| Mode | Metric | Before transport tuning | After transport tuning | Change |
| --- | --- | ---: | ---: | ---: |
| sync | avg | 8987 ms | 8999 ms | +12 ms (+0.13%) |
| sync | p50 | 3002 ms | 3232 ms | +230 ms |
| sync | p90 | 19902 ms | 18312 ms | -1590 ms |
| sync | p95 | 20009 ms | 18481 ms | -1528 ms (-7.64%) |
| sync | p99 | 20503 ms | 19155 ms | -1348 ms (-6.57%) |
| sync | max | 20721 ms | 19218 ms | -1503 ms |
| stream | avg | 8990 ms | 6529 ms | -2461 ms (-27.37%) |
| stream | p50 | 2669 ms | 1915 ms | -754 ms |
| stream | p90 | 20106 ms | 18168 ms | -1938 ms |
| stream | p95 | 20852 ms | 19075 ms | -1777 ms (-8.52%) |
| stream | p99 | 21042 ms | 25795 ms | +4753 ms (+22.59%) |
| stream | max | 21054 ms | 27509 ms | +6455 ms |

Database verification:

```text
Window: 2026-05-17 02:14:00+08 to 02:23:00+08
api_key_id=2
account_id=24
requested_model=claude-sonnet-4.5

stream=false total=100 min=1636 ms avg=8972 ms max=19210 ms input_tokens=413800 output_tokens=1600
stream=true  total=100 min=1662 ms avg=6520 ms max=27503 ms input_tokens=413800 output_tokens=1600
total rows=200
```

sub2api log evidence:

```text
sticky.account_selected selected_account_id=24 account_name=kiro_claude_01 path=/v1/messages model=claude-sonnet-4.5 stream=false
sticky.account_selected selected_account_id=24 account_name=kiro_claude_01 path=/v1/messages model=claude-sonnet-4.5 stream=true
```

Verdict:

- PASS for completeness and correctness: 200/200 HTTP 200, 200/200 exact marker, 0 warning responses, 0 stream protocol misses.
- P95 improved in both modes:
  - sync p95 improved by 1528 ms, from 20009 ms to 18481 ms.
  - stream p95 improved by 1777 ms, from 20852 ms to 19075 ms.
- Stream average and p50 improved materially.
- Tail latency is not fully solved: stream p99/max worsened due to a small number of very slow but successful requests, with max 27509 ms. This points to remaining upstream/account scheduling tail behavior rather than response correctness or SSE framing errors.

## Follow-up: P0-C stream admission gate for Sonnet 4.5 tail latency

Date: 2026-05-17

Problem:

- P0-B improved p95 but stream p99/max worsened.
- The 100x10 traces showed a small number of slow-but-successful stream requests. Protocol correctness was intact; the issue was upstream/account scheduling tail latency.
- Existing admission logic let stream requests bypass the model gate until pressure had already been observed. That meant the first wave of a high-concurrency stream batch could still hit the upstream account too hard.

Fix:

- Added `modelAdmission.streamBypass` as an explicit configuration flag.
- Default behavior is now `streamBypass=false`, so stream requests participate in model admission immediately.
- `streamBypass=true` remains available for deployments that prefer no stream gate until pressure appears.
- Local deployment config was updated after backup:
  - Backup: `/tmp/kiro-go-config-before-p0c-20260517022322.json`
  - Active `claude-sonnet-4.5`: `maxConcurrent=8`, `maxWaiting=300`

Changed files:

- `config/config.go`
- `config/config_test.go`
- `proxy/opus_gate.go`
- `proxy/handler.go`
- `proxy/handler_test.go`

Go verification:

```text
go test ./config -run '^TestGetModelAdmissionConfigDefaultsFromOpus47AndPersistsValues$' -count=1
ok  	kiro-go/config	0.005s

go test ./proxy -run 'TestAcquireAdmissionGatesStreamByDefault|TestAcquireAdmissionCanBypassStreamWhenConfigured' -count=1
ok  	kiro-go/proxy	0.027s

go test ./proxy -count=1
ok  	kiro-go/proxy	0.919s

go test ./... -count=1
?   	kiro-go	[no test files]
?   	kiro-go/logger	[no test files]
ok  	kiro-go/auth	0.003s
ok  	kiro-go/config	0.005s
ok  	kiro-go/pool	0.003s
ok  	kiro-go/proxy	0.918s
```

Deployment:

```text
docker compose up -d --build kiro-go
docker compose stop kiro-go
jq '.modelAdmission.streamBypass=false | .modelAdmission.models["claude-sonnet-4.5"]={"maxConcurrent":8,"maxWaiting":300}' data/config.json
docker compose up -d kiro-go

Kiro-Go startup log:
[ModelAdmission] default=16/300 models=2 legacyOpus47=10/300
```

Smoke after deployment:

Artifact:

- `docs/superpowers/uat/sub2api-smoke/p0c-sonnet8-smoke-20260517022414.json`

Result:

```text
/v1/models: HTTP 200, 31 models
/v1/messages/count_tokens: HTTP 200, input_tokens=28
sync: HTTP 200, correct marker, 1860 ms
stream: HTTP 200, correct marker, message_start + message_stop, 1844 ms
```

20x10 stream canary:

Artifact:

- `docs/superpowers/uat/sub2api-100x10-2026-05-16/p0c-sonnet8-stream20-202605170224-summary.json`

Result:

```text
total=20
concurrency=10
HTTP 200=20
correct=20
warnings=0
failed=0
message_start missing=0
message_stop missing=0
p95=2730 ms
p99=3003 ms
max=3003 ms
```

Full 100x10 stream verification:

Artifact:

- `docs/superpowers/uat/sub2api-100x10-2026-05-16/p0c-sonnet8-stream100-202605170225-summary.json`
- `docs/superpowers/uat/sub2api-100x10-2026-05-16/p0c-sonnet8-stream100-202605170225.jsonl`

Result:

```text
total=100
concurrency=10
HTTP 200=100
correct=100
containsMarker=100
warnings=0
failed=0
message_start missing=0
message_stop missing=0

min=1305 ms
avg=7385 ms
p50=2873 ms
p90=15031 ms
p95=15308 ms
p99=15899 ms
max=16096 ms
```

Comparison against P0-B stream 100x10:

| Metric | P0-B transport only | P0-C stream admission + Sonnet 8 | Change |
| --- | ---: | ---: | ---: |
| avg | 6529 ms | 7385 ms | +856 ms |
| p50 | 1915 ms | 2873 ms | +958 ms |
| p90 | 18168 ms | 15031 ms | -3137 ms |
| p95 | 19075 ms | 15308 ms | -3767 ms (-19.75%) |
| p99 | 25795 ms | 15899 ms | -9896 ms (-38.36%) |
| max | 27509 ms | 16096 ms | -11413 ms |

Database verification:

```text
Window: 2026-05-17 02:24:00+08 to 02:30:00+08
api_key_id=2
account_id=24
requested_model=claude-sonnet-4.5

stream=false total=1   min=1851 ms avg=1851 ms max=1851 ms
stream=true  total=121 min=1298 ms avg=6401 ms max=16091 ms
```

Note:

- `stream=true total=121` includes the smoke stream request, the 20x10 canary, and the 100x10 verification.
- JSONL per-request validation confirmed 120/120 canary + verification stream requests were correct and had both `message_start` and `message_stop`.

Verdict:

- PASS for P0-C stream admission tail-latency mitigation.
- Correctness stayed at 100%.
- Stream p95, p99, and max improved substantially compared with P0-B.
- Tradeoff: average and p50 increased because the local gate intentionally queues more work before the upstream account. This is the expected exchange: smoother tail latency and fewer pathological slow requests at the cost of modest median latency.

## Follow-up: P0-C non-stream 100x10 regression after Sonnet admission

Date: 2026-05-17

Scope:

- Re-run non-stream 100x10 through the real local sub2api route after enabling `claude-sonnet-4.5` model admission at `maxConcurrent=8`.
- Verify every response is HTTP 200 and returns the exact generated marker.
- Compare latency against the P0-B post-transport non-stream baseline.
- Confirm usage logs route to `kiro_claude_01`.

Command:

```bash
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js sync 100 10 claude-sonnet-4.5 p0c-sonnet8-sync100-202605170229
```

Artifacts:

- `docs/superpowers/uat/sub2api-100x10-2026-05-16/p0c-sonnet8-sync100-202605170229-summary.json`
- `docs/superpowers/uat/sub2api-100x10-2026-05-16/p0c-sonnet8-sync100-202605170229.jsonl`

Correctness:

```text
total=100
concurrency=10
HTTP 200=100
correct=100
containsMarker=100
warnings=0
failed=0
```

Latency:

```text
min=1312 ms
avg=8153 ms
p50=4761 ms
p90=15268 ms
p95=16126 ms
p99=18347 ms
max=18396 ms
```

Comparison against P0-B non-stream 100x10:

| Metric | P0-B transport only | P0-C Sonnet 8 admission | Change |
| --- | ---: | ---: | ---: |
| avg | 8999 ms | 8153 ms | -846 ms |
| p50 | 3232 ms | 4761 ms | +1529 ms |
| p90 | 18312 ms | 15268 ms | -3044 ms |
| p95 | 18481 ms | 16126 ms | -2355 ms (-12.74%) |
| p99 | 19155 ms | 18347 ms | -808 ms (-4.22%) |
| max | 19218 ms | 18396 ms | -822 ms |

Database verification:

```text
Window: 2026-05-17 02:29:00+08 to 02:32:00+08
api_key_id=2
account_id=24
requested_model=claude-sonnet-4.5

account: id=24 name=kiro_claude_01 platform=anthropic type=apikey status=active schedulable=true concurrency=12
api key: id=2 name=claude status=active

stream=false total=100 min=1302 ms avg=8142 ms max=18390 ms input_tokens=413800 output_tokens=1600
```

Verdict:

- PASS for non-stream correctness and routing after P0-C admission.
- P95 improved from 18481 ms to 16126 ms compared with P0-B.
- P99 and max also improved slightly.
- P50 increased because the local model gate queues more requests before sending them upstream; the tradeoff is lower tail latency and no observed correctness failures.

## Follow-up: claude-opus-4-7 real downstream 100x10

Date: 2026-05-17

Scope:

- Verify `claude-opus-4-7` through the same real local path:
  `/www/sub2api -> Kiro-Go -> Kiro`.
- Confirm the model alias is visible from `/v1/models` and accepted by `/v1/messages`.
- Run 100x10 non-stream and stream checks.
- Separate protocol health from model-answer correctness, because Opus 4.7 treats arbitrary marker echo prompts as suspicious.

Preload smoke:

```bash
SUB2API_MODEL=claude-opus-4-7 SUB2API_SMOKE_RUN_ID=opus47-preload-smoke-20260517023312 node docs/superpowers/uat/claude-code-sub2api-smoke.js
```

Artifact:

- `docs/superpowers/uat/sub2api-smoke/opus47-preload-smoke-20260517023312.json`

Result:

```text
/v1/models: HTTP 200, includes claude-opus-4-7
/v1/messages/count_tokens: HTTP 200, input_tokens=28
sync: HTTP 200, exact marker returned, 4745 ms
stream: HTTP 200, exact marker returned, message_start + message_stop, 6389 ms
```

Marker-probe 100x10:

```bash
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js sync 100 10 claude-opus-4-7 opus47-sync100-202605170233
```

Artifacts:

- `docs/superpowers/uat/sub2api-100x10-2026-05-16/opus47-sync100-202605170233-summary.json`
- `docs/superpowers/uat/sub2api-100x10-2026-05-16/opus47-sync100-202605170233.jsonl`

Result:

```text
HTTP 200=100
correct marker=65
failed content check=35
warnings=0
p95=43664 ms
p99=51658 ms
```

Failure pattern:

- Failed responses were normal Anthropic-compatible HTTP 200 message responses from `claude-opus-4.7`.
- The common response text was `I can't return that marker`, `I can't discuss that`, or similar.
- This points to Opus 4.7 refusing the synthetic marker-echo probe as a suspicious prompt, not a sub2api/Kiro-Go protocol failure.

Benign deterministic task mode:

- The load script now accepts an optional seventh argument:

```bash
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js <sync|stream> <total> <concurrency> <model> <runId> math
```

- `math` mode asks the model to compute `index + 1000` and reply with only the decimal integer.
- The default marker mode is unchanged.

Math canary:

```text
sync 10x5:   HTTP 200=10, correct=10, failed=0
stream 10x5: HTTP 200=10, correct=10, failed=0, message_stop missing=0
```

Math 100x10 commands:

```bash
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js sync 100 10 claude-opus-4-7 opus47-math-sync100-202605170237 math
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js stream 100 10 claude-opus-4-7 opus47-math-stream100-202605170238 math
```

Artifacts:

- `docs/superpowers/uat/sub2api-100x10-2026-05-16/opus47-math-sync100-202605170237-summary.json`
- `docs/superpowers/uat/sub2api-100x10-2026-05-16/opus47-math-sync100-202605170237.jsonl`
- `docs/superpowers/uat/sub2api-100x10-2026-05-16/opus47-math-stream100-202605170238-summary.json`
- `docs/superpowers/uat/sub2api-100x10-2026-05-16/opus47-math-stream100-202605170238.jsonl`

Math 100x10 summary:

| Mode | Total | Concurrency | HTTP 200 | Correct answer | Failed content check | Warnings | Missing `message_start` | Missing `message_stop` | Avg | P50 | P90 | P95 | P99 | Max |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| sync | 100 | 10 | 100 | 99 | 1 | 0 | n/a | n/a | 8394 ms | 1387 ms | 26294 ms | 32991 ms | 33535 ms | 33645 ms |
| stream | 100 | 10 | 100 | 99 | 1 | 0 | 0 | 0 | 10665 ms | 5720 ms | 32374 ms | 36905 ms | 37556 ms | 38600 ms |

The single failed content check in both sync and stream was request index 100:

```text
prompt: compute 100 + 1000
expected: 1100
actual: 100
HTTP: 200
stream protocol: message_start present, message_stop present
```

Database verification:

```text
Math sync window:   2026-05-17 02:37:00+08 to 02:38:41+08
Math stream window: 2026-05-17 02:38:41+08 to 02:40:40+08
api_key_id=2
account_id=24
requested_model=claude-opus-4-7

stream=false total=100 min=1013 ms avg=8384 ms  max=33638 ms input_tokens=590300 output_tokens=199
stream=true  total=100 min=991 ms  avg=10655 ms max=38592 ms input_tokens=590300 output_tokens=199
```

Verdict:

- PASS for local route callability and protocol health for `claude-opus-4-7`.
- PASS for HTTP success: both math runs were 100/100 HTTP 200.
- PASS for SSE framing: stream run had 0 missing `message_start` and 0 missing `message_stop`.
- PARTIAL for model-answer correctness: math sync and stream each had 99/100 correct; both failures were the same model arithmetic mistake on index 100.
- FAIL for using marker echo as an Opus 4.7 correctness probe: 35/100 marker requests were refused by the model despite HTTP/protocol success.
- Opus 4.7 tail latency is materially higher than Sonnet 4.5 under the same 10-way client concurrency on this single account; P95 was about 33-37 seconds for the benign math task.

## Follow-up: Opus 4.7 admission tuning experiment and cache usage fix

Date: 2026-05-17

Scope:

- Try lower static Opus 4.7 admission limits to reduce 10-way stream tail latency.
- Keep `/www/sub2api -> Kiro-Go -> Kiro` callable during and after the experiment.
- Fix a deterministic prompt-cache accounting inconsistency found during code audit.

Live config backup:

```text
/tmp/kiro-go-config-before-opus47-admission-20260517024828.json
```

Admission experiment:

| Config | Run | Correct | Missing `message_stop` | Avg | P50 | P95 | P99 | Max | Decision |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| gate10 baseline | `opus47-math-stream100-202605170238` | 99/100 | 0 | 10665 ms | 5720 ms | 36905 ms | 37556 ms | 38600 ms | Baseline |
| gate2 | `opus47-gate2-math-stream100-202605170252` | 99/100 | 0 | 19156 ms | 13988 ms | 61507 ms | 72495 ms | 75107 ms | Rejected |
| gate4 | `opus47-gate4-math-stream100-202605170256` | 99/100 | 0 | 13646 ms | 6446 ms | 43738 ms | 50670 ms | 51894 ms | Rejected |

Result:

- Lowering Opus 4.7 static concurrency from 10 to 4 or 2 did not improve the full 100x10 stream tail.
- It reduced early burst pressure in small canaries but increased full-run queueing delay.
- The live config was restored to the measured-best baseline:

```json
{
  "modelAdmission": {
    "default": {"maxConcurrent": 16, "maxWaiting": 300},
    "models": {
      "claude-opus-4.7": {"maxConcurrent": 10, "maxWaiting": 300},
      "claude-sonnet-4.5": {"maxConcurrent": 8, "maxWaiting": 300}
    }
  }
}
```

Cache usage bug:

- Local prompt-cache usage simulation could report `cache_creation.ephemeral_*_input_tokens` for below-threshold prompts even when `cache_creation_input_tokens=0`.
- If an account already had unrelated cache entries, a later below-threshold prompt could also report nonzero `cache_creation_input_tokens`.
- This made Claude Code usage/cost display less trustworthy for small prompts.

Fix:

- `promptCacheTracker.Compute` now returns zero cache usage and zero TTL breakdown for below-threshold prompts.
- The same threshold is enforced whether the account has no cache entries or unrelated existing entries.
- Typed tool `cache_control` test data was lengthened so it still validates 1h TTL behavior above the Sonnet cache threshold.

Changed files:

- `proxy/cache_tracker.go`
- `proxy/cache_tracker_test.go`

Focused TDD evidence:

```text
Initial red test:
TestPromptCacheBelowThresholdReportsNoCreationBreakdownOnFirstRequest
  expected no cache creation TTL breakdown below threshold, got CacheCreation1hInputTokens:85
TestPromptCacheBelowThresholdReportsNoCreationWhenAccountHasOtherEntries
  expected no cache usage below threshold with existing unrelated entries, got CacheCreationInputTokens:85

After fix:
go test ./proxy -run 'TestPromptCacheBelowThresholdReportsNoCreation|TestPromptCacheTracker|TestBuildClaudeUsageMapIncludesCacheFields|TestPromptCacheStable|TestPromptCacheImplicitBreakpointAtMessageEnd' -count=1 -v
PASS
```

Regression verification:

```text
go test ./proxy -count=1
ok  	kiro-go/proxy	0.902s

go test ./config -count=1
ok  	kiro-go/config	0.013s

go test ./... -count=1
ok  	kiro-go/auth
ok  	kiro-go/config
ok  	kiro-go/pool
ok  	kiro-go/proxy
```

Real sub2api smoke after restoring Opus gate10:

Artifacts:

- `docs/superpowers/uat/sub2api-smoke/post-cache-threshold-fix-smoke-20260517030043.json`
- `docs/superpowers/uat/sub2api-smoke/post-opus-gate-restore-smoke-20260517030047.json`

Result:

```text
claude-sonnet-4.5:
  /v1/models HTTP 200
  /v1/messages/count_tokens HTTP 200
  sync exact marker HTTP 200 correct
  stream exact marker HTTP 200 correct, message_stop present

claude-opus-4-7:
  /v1/models HTTP 200
  /v1/messages/count_tokens HTTP 200
  sync exact marker HTTP 200 correct
  stream exact marker HTTP 200 correct, message_stop present
```

Next implementation priorities from code audit:

1. Add handler-level SSE regression for two tool-use blocks: exact event order, indexes `0`/`1`, reconstructed `input_json_delta.partial_json`, `message_delta.stop_reason="tool_use"`, and `message_stop`.
2. Implement real `/v1/responses` tool/function-call compatibility. Current Responses support is enough for simple text but does not convert Responses `tools`, `tool_choice`, `function_call`, or `function_call_output`.
3. Replace static Opus admission guesses with latency/429-aware dynamic pressure control. The gate2/gate4 experiments show static lower concurrency can worsen end-to-end queue latency.

## Follow-up: Handler-level two-tool SSE regression

Date: 2026-05-17

Scope:

- Lock down the Claude Code stream contract for multiple tool calls at the handler level, not only in the standalone SSE writer.
- Simulate a Kiro upstream event stream that returns two completed `toolUseEvent` blocks.
- Assert the emitted Anthropic-compatible SSE sequence, content-block indexes, reconstructed `input_json_delta.partial_json`, `tool_use` stop reason, and final `message_stop`.

Changed file:

- `proxy/handler_test.go`

Regression added:

- `TestHandleClaudeStreamMultipleToolUsesEmitsIndexedInputJSONDeltas`

Coverage:

```text
message_start
content_block_start index=0 type=tool_use id=toolu_read_1 name=read_file
content_block_delta index=0 delta.type=input_json_delta
content_block_stop index=0
content_block_start index=1 type=tool_use id=toolu_write_2 name=write_file
content_block_delta index=1 delta.type=input_json_delta
content_block_stop index=1
message_delta stop_reason=tool_use
message_stop
```

The test reconstructs each tool's `partial_json` chunks and validates the final JSON input:

```text
toolu_read_1:  {"path":"/tmp/a.go","encoding":"utf-8"}
toolu_write_2: {"path":"/tmp/b.go","content":"package main\n"}
```

Verification:

```text
go test ./proxy -run 'TestClaudeSSEWriter|TestHandleClaudeStreamToolUseStartsWithMessageStart|TestHandleClaudeStreamMultipleToolUsesEmitsIndexedInputJSONDeltas|TestHandleClaudeStreamToolReferenceRestoresOriginalToolName|TestClaudeStreamErrorEventUsesMappedAnthropicErrorType' -count=1 -v
PASS

go test ./proxy -count=1
ok  	kiro-go/proxy	0.934s

go test ./... -count=1
ok  	kiro-go/auth
ok  	kiro-go/config
ok  	kiro-go/pool
ok  	kiro-go/proxy
```

Real sub2api smoke:

Artifact:

- `docs/superpowers/uat/sub2api-smoke/post-two-tool-sse-test-smoke-20260517030347.json`

Result:

```text
/v1/models HTTP 200
/v1/messages/count_tokens HTTP 200
sync exact marker HTTP 200 correct
stream exact marker HTTP 200 correct
stream events: message_start, content_block_start, content_block_delta, content_block_stop, message_delta, message_stop
```

Verdict:

- PASS for handler-level multi-tool SSE regression coverage.
- No runtime code change was needed; the current handler already emits correct completed multi-tool block order for the simulated upstream event stream.
- Remaining tool-streaming limitation: Kiro partial tool-input fragments are still emitted only once a completed `toolUseEvent` is received, not as true upstream fine-grained fragments.
