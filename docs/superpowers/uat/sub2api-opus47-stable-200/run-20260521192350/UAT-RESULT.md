# UAT Result: sub2api -> Kiro-Go claude-opus-4-7 100/100

Verdict: BLOCKED_BY_UPSTREAM_CAPACITY, not PASS.

Date: 2026-05-21 Asia/Shanghai

## What Passed

- Kiro-Go Docker service rebuilt and restarted successfully.
- Kiro-Go `/health` returned `ok`, version `1.0.8`.
- sub2api `/health` returned `ok`.
- Local regression tests passed:
  - `go test ./pool -count=1`
  - `go test ./pool ./proxy -count=1`
  - `go test ./proxy -run TestEnsureValidTokenCoalescesConcurrentRefreshesPerAccount -count=20`
  - `node --check docs/superpowers/uat/sub2api-opus47-stable-200/run-stable-200-uat.js`
- Smoke through real sub2api path passed: 1/1 non-stream + 1/1 stream.
- The scheduler/token fix worked: before rebuild readiness was `blocked` with `safeConcurrency=0`; after rebuild readiness became `degraded` with `safeConcurrency=10`, removing `no_schedulable_accounts`.

## 100/100 Result

The 100 non-stream + 100 stream run started with `CONCURRENCY=4` through:

`sub2api http://127.0.0.1:18080/v1/messages -> Kiro-Go http://kiro-go:8080/v1/messages?beta=true -> upstream`

The runner stopped during non-stream, as designed, after detecting capacity fallback text in HTTP 200 bodies.

- Non-stream attempted: 9
- Non-stream real PASS: 5
- Non-stream capacity fallback: 4
- Stream attempted: 0
- Final verdict: `BLOCKED_BY_UPSTREAM_CAPACITY`

The failed bodies contained Kiro-Go capacity protection text, including `upstream capacity is temporarily unavailable` and `admission_pressure`. These are not real assistant model outputs, so they cannot pass this UAT even though HTTP status was 200.

## Evidence

- `summary.json`: blocked verdict and failed sample indexes.
- `non-stream.jsonl`: first 5 successful rows contain real assistant text `ok 0`, `ok 1`, `ok 2`, `ok 3`, `ok 7`; failed rows contain capacity fallback text.
- `evidence/readiness-after-block.json`: `status=degraded`, `safeConcurrency=1`, `reasonCodes=["admission_pressure","token_expired"]`, `recentStableFallbacks=4`, `contentSuccessRate=0.6363`.
- `evidence/sub2api-redacted.log`: `api_key_id=12`, `group_id=1`, account `kiro_claude_01`, `/v1/messages`, model `claude-opus-4-7`, HTTP 200 through Anthropic passthrough branch.
- `evidence/kiro-go-redacted.log`: upstream `INSUFFICIENT_MODEL_CAPACITY` warnings.
- `evidence/db-usage-api-key-12.txt`: DB usage rows for api_key_id 12, account_id 24, 10 non-stream + 1 stream records.
- `evidence/db-key-cleanup.txt`: temporary UAT API key id 12 soft-deleted.
- `evidence/kiro-opus47-blocked-admin-202605211925.png`: Playwright-MCP screenshot showing Kiro-Go admin running with counters updated.

## Conclusion

The local scheduler bug that caused refreshable expired-token accounts to be unschedulable is fixed and verified. The real 100/100 Opus 4.7 UAT still cannot PASS because upstream capacity/admission pressure returned gateway capacity fallback content during the run.
