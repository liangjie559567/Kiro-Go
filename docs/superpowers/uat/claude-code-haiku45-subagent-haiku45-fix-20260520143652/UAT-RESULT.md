# UAT Result: Claude Code Haiku 4.5 Subagent / Explore Fix

Date: 2026-05-20 14:41 Asia/Shanghai

## Verdict

PASS

The `Explore(...) Done (0 tool uses · 0 tokens ...)` failure path was traced to Kiro-Go not normalizing Claude Code's versioned Haiku model name:

`claude-haiku-4-5-20251001 -> claude-haiku-4.5`

Before the fix, sub2api forwarded the Claude Code subagent request to Kiro-Go and received `503 No available accounts` because all 21 Kiro-Go accounts were filtered as `model not listed`. After the fix, Kiro-Go readiness maps the model correctly and all 21 accounts are schedulable.

## Root Cause Evidence

- sub2api historical error rows show repeated failures for `claude-haiku-4-5-20251001` before the fix:
  - `status_code=502`
  - `upstream_status_code=503`
  - `error_message=Upstream service temporarily unavailable`
- Kiro-Go pre-fix readiness for the same model reported:
  - `mappedModel=claude-haiku-4-5-20251001`
  - `listedByGateway=false`
  - all accounts: `model not listed`
- `kiro-gateway` implements this exact normalization in `kiro/model_resolver.py`:
  - `claude-haiku-4-5-20251001 -> claude-haiku-4.5`
  - `claude-sonnet-4-5-latest -> claude-sonnet-4.5`

## Fix

Updated Kiro-Go model normalization in:

- `proxy/translator.go`
- `proxy/translator_test.go`
- `proxy/handler_test.go`

The fix adds generic Claude dashed-minor/date/latest normalization before account model-list filtering.

## API Evidence

Artifacts:

- `api/playwright-fullstack-summary.json`
- `kiro_direct_nonstream.body`
- `sub2api_claude_nonstream.body`
- `sub2api_claude_stream.body`
- `claude-cli-subagent-debug.log`

Post-fix Kiro-Go readiness:

- requested: `claude-haiku-4-5-20251001`
- mapped: `claude-haiku-4.5`
- `listedByGateway=true`
- `routingReason=schedulable accounts available`
- `accountsEvaluated=21`
- `locallySchedulable=21`
- `riskGroupCoolingDown=0`

Real sub2api probe:

- `/v1/messages`
- model: `claude-haiku-4-5-20251001`
- HTTP 200
- upstream response model: `claude-haiku-4.5`
- content marker matched

Claude CLI subagent reproduction:

- Claude Code 2.1.143
- local `ANTHROPIC_BASE_URL=http://127.0.0.1:18080`
- subagent/tool path executed successfully
- debug log includes `tool_dispatch_start tool=Bash` and `tool_dispatch_end tool=Bash outcome=ok`

## Database Evidence

`db/playwright-sub2api-db.json`:

- recent successful usage rows for `claude-haiku-4-5-20251001`
- `api_key_id=2`
- `account_id=24`
- `group_id=1`
- `input_tokens` and `output_tokens` populated
- no Haiku errors since this UAT run started
- historical Haiku errors are preserved as pre-fix evidence

## Screenshot / Page Evidence

Screenshots:

- `screenshots/kiro-admin-haiku-readiness.png`
- `screenshots/kiro-admin-request-logs.png`
- `screenshots/sub2api-admin-accounts.png`
- `screenshots/sub2api-admin-usage.png`

Playwright checks passed:

- Kiro-Go admin API page shows Haiku/readiness content.
- Kiro-Go request logs page renders.
- sub2api Accounts page shows `kiro_claude_01`.
- sub2api Usage page shows Haiku usage.
- No page errors.
- Screenshot files exist and are non-empty.

## Test Evidence

Commands passed:

- `go test ./proxy -run 'TestParseModelAndThinkingNormalizesOfficial|TestAdminClaudeCodeModelReadiness' -count=1`
- `go test ./pool -count=1`
- `go test ./proxy -run 'Tool|Task|Fine|SSE|ClaudeCode|WrapToolUse|StreamedTool|TaskCreate|TaskUpdate|TaskOutput' -count=1`
- `node docs/superpowers/uat/claude-code-haiku45-subagent-haiku45-fix-20260520143652/playwright/fullstack-uat.js`

## Open Notes

- The shell environment originally had `ANTHROPIC_BASE_URL=https://code.newcli.com/claude/aws`; that can bypass local sub2api/Kiro-Go. The successful Claude CLI reproduction explicitly used the local sub2api URL.
- Kiro official account temporary-limit 429s are real and still appear in logs for some accounts. They are not the cause of this Haiku subagent failure, which was a model-name readiness bug.
