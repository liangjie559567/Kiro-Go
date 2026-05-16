# Claude Code Gateway Reliability UAT

Date: 2026-05-16

## Deployment

- Kiro-Go Docker service rebuilt with current workspace code.
- Container: `kiro-go-kiro-go-1`
- Health check: `GET http://127.0.0.1:8080/health` returned HTTP 200.
- Runtime log: `Opus47Admission maxConcurrent=10 maxWaiting=300`.

## Fixes Verified

- Account selection and active-connection reservation are atomic in the Kiro-Go account pool.
- Claude/OpenAI/Responses retry loops reserve account slots before upstream calls.
- Request logs now include Claude Code session ID, queue wait, first-token latency, attempts, and tool-use count.
- Claude upstream errors map to Anthropic-compatible error types.
- Kiro-Go admin request logs expose reliability metrics in the latency column.

## Automated Verification

- `go test ./...`: PASS.
- `go build ./...`: PASS.
- Targeted tests cover:
  - atomic account reservation;
  - request-log reliability metadata;
  - Opus 4.7 admission behavior;
  - Anthropic-style error mapping.

## Production sub2api 100x10 Tests

Initial run through sub2api showed 99/100 correctness for both sync and stream. Kiro-Go recorded 198 successful requests and 0 failures, proving the two failures did not enter Kiro-Go. Root cause was sub2api concurrency slot pressure:

- Redis had residual slots: `concurrency:user:1` and `concurrency:account:24`.
- sub2api user/account concurrency was exactly 10 while other local calls were active.
- Failure body: `Concurrency limit exceeded for user, please retry later`.

Mitigation for validation:

- Cleared stale sub2api concurrency keys.
- Raised local sub2api test user/account concurrency from 10 to 12 to leave headroom for concurrent background/local calls.

Retest results:

| Mode | Total | Concurrency | HTTP 200 | Correct | Exact | Injection Leak | Failures | Max Latency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Non-stream | 100 | 10 | 100 | 100 | 100 | 0 | 0 | 13.880s |
| Stream | 100 | 10 | 100 | 100 | 100 | 0 | 0 | 13.366s |

Both retests meet the requested 10-concurrency / 100-request correctness target and the max latency below 30 seconds.

## Evidence Files

- `docs/superpowers/uat/2026-05-16-sub2api-sync-reliability-100x10.json`
- `docs/superpowers/uat/2026-05-16-sub2api-stream-reliability-100x10.json`
- `docs/superpowers/uat/2026-05-16-kiro-admin-request-log-evidence.json`
- `docs/superpowers/uat/2026-05-16-kiro-admin-reliability-logs.png`
- `docs/superpowers/uat/2026-05-16-sub2api-home-reliability.png`
- `docs/superpowers/uat/2026-05-16-browser-reliability-evidence.json`

## Verdict

PASS for Kiro-Go production Docker deployment and sub2api real-call validation after clearing stale downstream slots and setting downstream test concurrency headroom to 12.

Important boundary: the tested path is now stable at 10 concurrent requests with max latency under 30 seconds, but sub2api can still reject calls before they reach Kiro-Go if user/account concurrency is set exactly to the test concurrency and other live traffic consumes slots.
