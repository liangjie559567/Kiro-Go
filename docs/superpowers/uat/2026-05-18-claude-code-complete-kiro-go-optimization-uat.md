# Claude Code Complete Kiro-Go Optimization UAT

Date: 2026-05-18

## Scope

- Kiro-Go source changes only.
- `/www/sub2api` is rebuild and real-call verification target only.
- Secrets and API keys are not printed.

## Go Tests

- Command: `go test ./...`
- Result: PASS
- Evidence time: `2026-05-18 03:42:21 CST`
- Notes: Packages reported PASS for `kiro-go`, `kiro-go/auth`, `kiro-go/config`, `kiro-go/pool`, and `kiro-go/proxy`; `kiro-go/logger` has no test files.

## Kiro-Go Local Verification

- Build/restart command: `docker compose up -d --build kiro-go`
- Build/restart result: PASS; container `kiro-go-kiro-go-1` recreated and started.
- Health URL: `http://127.0.0.1:8080/health`
- Health result before rebuild: `{"status":"ok","uptime":4619,"version":"1.0.8"}`
- Health result after rebuild: `{"status":"ok","uptime":5,"version":"1.0.8"}`
- `/v1/models` result before rebuild: returned JSON model list including `auto`, `auto-thinking`, `claude-opus-4.7`, `claude-opus-4.7-thinking`, `claude-opus-4.6`, `claude-sonnet-4.6`, and `claude-opus-4.5`.
- `/v1/models` result after rebuild: returned JSON model list.
- Direct `/v1/messages` non-stream smoke: PASS, `task7-direct-20260518100108-sync`, status 200, exact marker returned.
- Direct `/v1/messages` stream smoke: PASS, `task7-direct-20260518100108-stream`, status 200, exact marker returned, `message_stop` observed.
- Direct smoke artifact: `docs/superpowers/uat/sub2api-smoke/task7-direct-20260518100108/summary.json`.

## sub2api Downstream Verification

- Data safety precheck: PASS; before rebuild, database counts were `users=3`, `groups=4`, `accounts=72`, `api_keys=4`.
- Backup before rebuild: PASS; `pg_dump -Fc` created `/tmp/sub2api-before-task7-20260518095836.dump` inside `sub2api-postgres`.
- Initial rebuild command issue: `docker compose -f docker-compose.current.yml up -d --build` failed because the override file references `sub2api-network` without defining it.
- Correct rebuild/restart command: `docker compose -f docker-compose.yml -f docker-compose.current.yml up -d --build sub2api` from `/www/sub2api/deploy`.
- Rebuild/restart result: PASS; only `sub2api` was recreated, while `sub2api-postgres` and `sub2api-redis` stayed running and healthy.
- Data safety postcheck: PASS; after rebuild, database counts remained `users=3`, `groups=4`, `accounts=72`, `api_keys=4`.
- Health URL: `http://127.0.0.1:18080/health`
- Health result: `{"status":"ok"}`.
- `/v1/models` result: PASS, status 200, 33 models.
- `/v1/messages/count_tokens` result: PASS, status 200, `input_tokens=27`.
- Non-stream `/v1/messages` result: PASS, status 200, exact marker returned, duration 2650 ms.
- Stream `/v1/messages` result: PASS, status 200, exact marker returned, `message_start` and `message_stop` observed, duration 2144 ms.
- sub2api smoke artifact: `docs/superpowers/uat/sub2api-smoke/task7-sub2api-20260518100028.json`.

## Request Log Evidence

- sub2api request IDs: `task7-sub2api-20260518100028-v1-models`, `task7-sub2api-20260518100028-v1-messages-count-tokens`, `task7-sub2api-20260518100028-v1-messages`.
- sub2api selected account: account `24`, name `kiro_claude_01`, group `1`, API key name `claude`, model `claude-sonnet-4.5`.
- sub2api attempts/classification: request logs show account selected, sticky binding honored, Anthropic API-key passthrough branch used, and final status 200 for count_tokens, non-stream, and stream.
- Kiro-Go request IDs: `task7-direct-20260518100108-sync`, `task7-direct-20260518100108-stream`, plus two latest upstream requests from sub2api at `2026-05-18T02:00:28Z` and `2026-05-18T02:00:31Z`.
- Models: all Task 7 message smokes used `claude-sonnet-4.5`.
- Accounts: Kiro-Go selected account `93fb7e70-3ccf-4330-9d8d-b25c89322ae4`, region `us-east-1`.
- Attempts: Kiro-Go request logs show `attempts=1` for direct and sub2api-routed message requests.
- First-token timings: direct sync 2536 ms, direct stream 1763 ms, sub2api-routed sync 2548 ms, sub2api-routed stream 2042 ms.
- Payload/tool trimming: request logs captured payload final bytes; no trim was needed for this smoke payload.
- Responses restore/readiness: readiness telemetry is implemented; this Task 7 smoke did not exercise Responses restore because it used `/v1/messages`.
- Kiro-Go request-log artifacts: `docs/superpowers/uat/sub2api-smoke/task7-kiro-request-logs-20260518100202.json` and `docs/superpowers/uat/sub2api-smoke/task7-kiro-latest-message-logs-20260518100215.json`.

## Failure Classification

- sub2api layer: PASS after using the correct two-file compose command; single override file is not a valid standalone compose project.
- Kiro-Go protocol/payload layer: PASS for direct and sub2api-routed `/v1/messages` non-stream and stream.
- Kiro-Go account/token layer: PASS for selected Kiro-Go account and sub2api account `kiro_claude_01`.
- Kiro upstream capacity/network layer: PASS for this smoke; all message requests returned status 200 with one attempt.
