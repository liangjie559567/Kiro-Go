# Opus 4.7 Capacity Retry UAT - 2026-05-15

## Scope

- Environment: real Docker service `kiro-go-kiro-go-1` on `127.0.0.1:8080`.
- Model: `claude-opus-4.7` only.
- Constraint: no model downgrade.
- Downstream latency budget: 90 seconds.

## Implementation Under Test

- 429 response bodies are preserved and classified from upstream evidence.
- `INSUFFICIENT_MODEL_CAPACITY` is treated as Opus 4.7 model capacity pressure, not account quota exhaustion.
- Opus 4.7 non-stream requests retry within a 90 second request budget.
- Accounts returning model capacity 429 enter short cooldown and re-enter routing after cooldown.
- Opus 4.7 requests use model-level admission control to cap concurrent upstream pressure and queue excess requests within the 90 second downstream budget.
- Opus 4.7 429 handling does not fan out the same account attempt across Kiro IDE, CodeWhisperer, and AmazonQ; it returns the 429 to account routing so another account can be tried first.
- Expired cooldown state is cleared when accounts become schedulable again, so accounts are immediately available after rate-limit recovery and the admin UI does not retain stale `rate_limited` health.
- Health checks validate token plus model listing and no longer consume Opus inference capacity.

## Verification Evidence

- Unit/integration tests: `go test ./... -count=1` passed.
- Service deployment: `docker compose build kiro-go && docker compose up -d kiro-go`.
- Health/status:
  - `/health`: `status=ok`.
  - `/admin/api/status`: `accounts=13`, `available=13`.
  - `data/config.json`: `enabled=13`, no active cooldowns after verification.
- Admin HTML:
  - `GET /admin` returned `200 OK`, `Content-Length: 161180`.
  - Playwright-MCP browser navigation to local admin was blocked by browser backend with `ERR_BLOCKED_BY_CLIENT`, so browser screenshot verification is not marked PASS.

## Real Opus 4.7 Results

### Preferred endpoint only

- Config: `preferredEndpoint=kiro`, `endpointFallback=false`.
- Run: `/tmp/kiro-uat/opus47-20-20260515-143705.jsonl`.
- Result: 19 success / 20 total = 95%.
- Max latency: 91 seconds.
- Failure:
  - HTTP 503 after waiting the 90 second budget.
  - Upstream reason: `INSUFFICIENT_MODEL_CAPACITY`.

Verdict: FAIL for 99% target.

### Same-model endpoint fallback enabled

- Config: `preferredEndpoint=kiro`, `endpointFallback=true`.
- Run: `/tmp/kiro-uat/opus47-fallback-10-20260515-144720.jsonl`.
- Result: 10 success / 10 total = 100%.
- Max latency: 17 seconds.
- Docker logs confirm fallback attempted the same `claude-opus-4.7` request across Kiro IDE, CodeWhisperer, and AmazonQ when capacity 429 occurred.

Verdict: PASS for this 10-call smoke sample, but insufficient sample size to certify 99% long-run SLA.

### Production auto endpoint with fallback enabled

- Config: `preferredEndpoint=auto`, `endpointFallback=true`.
- Deployment: latest working tree rebuilt and restarted with `docker compose build kiro-go && docker compose up -d kiro-go`.
- Run: `/tmp/kiro-uat/opus47-prod-auto-fallback-100-20260515-145638.jsonl`.
- Result: 100 success / 100 total = 100%.
- Max latency: 49 seconds.
- P95 latency: 28 seconds.
- P99 latency: 44 seconds.
- Failures: none.
- Post-run status:
  - `/admin/api/status`: `accounts=13`, `available=13`.
  - `data/config.json`: 13 enabled accounts, no active cooldowns, no persisted failure reasons.
- Docker logs still show upstream `INSUFFICIENT_MODEL_CAPACITY` from Kiro IDE, CodeWhisperer, and AmazonQ during the run, but those upstream 429s were absorbed by same-model endpoint fallback, account rotation, and the 90 second retry budget.

Verdict: PASS for the 100-call sequential production sample. This satisfies the requested 99%+ success-rate target for this test run without model downgrade.

### Production concurrent pressure test

- Config: `preferredEndpoint=auto`, `endpointFallback=true`.
- Environment: real production Docker service, no sandbox container.
- Model: `claude-opus-4.7`.
- Run: `/tmp/kiro-uat/opus47-concurrency10-total100-20260515-160042.jsonl`.
- Shape: concurrency 10, total 100 requests.
- Result from recorded HTTP status counts:
  - HTTP 200: 90
  - HTTP 500: 10
  - Success rate: 90%
- Production status delta corroborates the result:
  - Before: `successRequests=732`, `failedRequests=213`.
  - After: `successRequests=822`, `failedRequests=224`.
- Docker logs during the run show heavy upstream pressure:
  - `INSUFFICIENT_MODEL_CAPACITY`
  - `Too many requests, please wait before trying again.`
  - 429s across Kiro IDE, CodeWhisperer, and AmazonQ.
- The result JSON rows have malformed `model` / `reason` fields due to a test harness quoting bug, but the HTTP status counts are intact and match server-side counters.

Verdict: FAIL for high-concurrency 99%+ target at concurrency 10.

### Production concurrent pressure retest after admission-control and routing fixes

- Config: `preferredEndpoint=auto`, `endpointFallback=true`.
- Deployment: latest working tree rebuilt and restarted with `docker compose build kiro-go && docker compose up -d kiro-go`.
- Run: `/tmp/kiro-uat/opus47-concurrency10-total30-20260515-162723.jsonl`.
- Shape: concurrency 10, total 30 requests.
- Result:
  - HTTP 200: 30
  - Failures: 0
  - Success rate: 100%
  - Max latency: 76.23 seconds
  - P50 latency: 47.503 seconds
  - P95 latency: 65.42 seconds
  - P99 latency: 67.751 seconds
- Docker logs still show upstream `INSUFFICIENT_MODEL_CAPACITY`, but the log lines are scoped to `Endpoint Kiro IDE returned Opus 4.7 429`; there is no same-account three-endpoint fan-out for each 429.
- Post-run status after final deploy:
  - `/admin/api/status`: `accounts=13`, `available=13`.
  - `data/config.json`: `activeCooldowns=[]`, `failures=[]`.

Verdict: PASS for this 30-call concurrency-10 production retest. It demonstrates that the routing/admission fixes keep the observed requests under the 90 second budget without model downgrade. It is not large enough by itself to certify a 99% long-run SLA.

### Production concurrency-10 total-100 certification retest

- Config: `preferredEndpoint=auto`, `endpointFallback=true`.
- Environment: real production Docker service `kiro-go-kiro-go-1`, no sandbox container.
- Run: `/tmp/kiro-uat/opus47-concurrency10-total100-20260515-163648.jsonl`.
- Shape: concurrency 10, total 100 requests.
- Result:
  - HTTP 200: 100
  - Failures: 0
  - Success rate: 100%
  - Max latency: 83.479 seconds
  - P50 latency: 32.368 seconds
  - P95 latency: 60.558 seconds
  - P99 latency: 71.778 seconds
- Post-run status:
  - `/admin/api/status`: `accounts=13`, `available=13`.
  - `data/config.json`: `activeCooldowns=[]`, `failures=[]`, `enabled=13`.
- Docker logs during the run still show upstream Opus 4.7 `INSUFFICIENT_MODEL_CAPACITY` 429s from Kiro IDE, but no downstream request failed and no account remained stuck in cooldown after the run.

Verdict: PASS for concurrency 10 total 100. This run satisfies the requested >=99% downstream success target and the <=90 second max latency constraint without Opus 4.7 model downgrade.

### Production 100-concurrent no-empty-stream regression

- User requirement: when upstream Opus 4.7 returns 429 / `INSUFFICIENT_MODEL_CAPACITY`, Kiro-Go must not return `HTTP 200` with an empty stream. Every downstream response must contain either valid SSE content or an explicit error body/event.
- Code verification added:
  - `TestHandleClaudeStreamOpus47CapacityLimitReturnsExplicitError`
  - `TestHandleClaudeStreamOpus47CapacityLimitNeverReturnsEmptyBodyUnderConcurrency`
- Test commands:
  - `go test ./proxy -run 'TestHandleClaudeStreamOpus47CapacityLimit(ReturnsExplicitError|NeverReturnsEmptyBodyUnderConcurrency)' -count=1`: PASS.
  - `go test ./proxy -run 'Opus47|TooManyRequests|RateLimit|Empty|Stream' -count=1`: PASS.
  - `go test ./proxy -count=1`: PASS.
  - `go test ./... -count=1`: PASS.
- Deployment:
  - `docker compose build kiro-go && docker compose up -d kiro-go`
  - Container: `kiro-go-kiro-go-1`
  - Image ID: `sha256:16ef090bb38179ad34fa262708decf866d7af54766efafe6fe371bb1e8b5db0d`
  - Started at: `2026-05-15T09:22:03Z`
- Real public endpoint validation:
  - Endpoint: `https://kiro.cgtall.com/v1/messages`
  - Model: `claude-opus-4.7`
  - Shape: 100 concurrent requests, total 100.
  - Run directory: `/tmp/kiro-opus47-100-20260515-172324`
  - Elapsed time: 41 seconds.
  - Status counts: `HTTP 200 = 100`.
  - `empty_body = 0`.
  - `http_200_empty_body = 0`.
  - `message_start = 100`.
  - `curl_errors = 0`.
- Runtime evidence:
  - Docker logs during the same run contained real upstream Opus 4.7 429s, including `INSUFFICIENT_MODEL_CAPACITY` and `Too many requests, please wait before trying again.`
  - Docker logs had no `panic`, no `fatal`, no `empty`, and no `http: superfluous response.WriteHeader` evidence.
- Admin/API state after run:
  - `/admin/api/status`: `accounts=13`, `available=13`.
  - `/admin/api/endpoint`: `preferredEndpoint=auto`, `endpointFallback=true`.
  - `/admin/api/accounts`: `count=13`, `enabled=13`, `rateLimited=0`, `quotaExhausted=0`, `cooling=0`.
- Browser verification:
  - Playwright-MCP attempted `https://kiro.cgtall.com/admin` and `http://127.0.0.1:8080/admin`.
  - Both navigations were blocked by the browser backend with `net::ERR_BLOCKED_BY_CLIENT` before page load, so screenshot-based UI verification is not marked PASS.
  - The same admin flows were verified through authenticated admin APIs against the production Docker container.

Verdict: PASS for the no-empty-stream contract under 100 concurrent real Opus 4.7 requests. The run observed upstream capacity/rate-limit pressure but produced zero downstream empty streams.

## Final UAT Verdict

PASS for sequential traffic, PASS for concurrency 10 total 100, and PASS for the 100-concurrent no-empty-stream regression. The earlier concurrency-10 total-100 run remains a valid FAIL baseline before the routing/admission fixes.

The implementation now avoids false `quota_exhausted`, clears stale rate-limit state after cooldown, keeps accounts enabled after cooldown, retries Opus 4.7 without model downgrade, avoids same-account endpoint fan-out on Opus 4.7 429, and met the 90 second latency budget in the latest concurrency-10 total-30 real production test.

The high-concurrency target is certified for the tested shapes: concurrency 10 total 100, and concurrency 100 total 100 for the no-empty-stream contract, Opus 4.7 only, no model downgrade.

## Required Follow-Up For 99%+

- Keep `preferredEndpoint=auto` and `endpointFallback=true` in production, but Opus 4.7 429s should continue to bypass same-account endpoint fan-out.
- Consider making the Opus 4.7 admission-control concurrency and queue size configurable from admin settings.
- Add rolling production metrics for Opus 4.7 by status, latency bucket, upstream reason, endpoint, and account.

## Post-Upstream-1.0.8 Integration Production Retest - 2026-05-15 18:43 CST

### Deployment

- Branch/commit: `main` at `6e76252` after merging upstream `1.0.8` while preserving local Opus 4.7 fixes.
- Deployment command: `docker compose up -d --build kiro-go`.
- Container: `kiro-go-kiro-go-1`.
- Health checks after deploy:
  - `http://127.0.0.1:8080/health`: `{"status":"ok","uptime":9,"version":"1.0.8"}`.
  - `https://kiro.cgtall.com/health`: `{"status":"ok","uptime":9,"version":"1.0.8"}`.
- Runtime config evidence:
  - Accounts: 13 total, 13 enabled.
  - Endpoint config: `preferredEndpoint=auto`, `endpointFallback=true`.
  - API key enforcement: enabled.

### Smoke Test

- Endpoint: `https://kiro.cgtall.com/v1/messages`.
- Model: `claude-opus-4-7`.
- Request: streaming Claude Messages API, message `hi`, `max_tokens=16`.
- Result:
  - HTTP 200.
  - Latency: 1.58 seconds.
  - Body bytes: 794.
  - SSE markers present: `message_start`, `content_block_delta`, `message_stop`.
  - SSE error: 0.

Verdict: PASS.

### Public Domain Concurrency 10 / Total 100

- Endpoint: `https://kiro.cgtall.com/v1/messages`.
- Model: `claude-opus-4-7`.
- Shape: concurrency 10, total 100.
- Client headers included normal `User-Agent: curl/8.5.0` and `Accept: text/event-stream`.
- Run directory: `/tmp/kiro-opus47-domain-ua-20260515-184659`.
- Result:
  - HTTP 200: 100.
  - Success: 100 / 100.
  - Success rate: 100%.
  - Empty body: 0.
  - HTTP 200 empty body: 0.
  - Missing `message_start`: 0.
  - SSE error: 0.
  - Error JSON: 0.
  - Exceptions: 0.
  - Latency: min 1077.9 ms, p50 1486.8 ms, p90 6592.29 ms, p95 7818.725 ms, p99 11095.189 ms, max 11104.9 ms.

Verdict: PASS for 99%+ success target and 90 second downstream latency budget.

### Local Production Container Concurrency 10 / Total 100

- Endpoint: `http://127.0.0.1:8080/v1/messages`.
- Purpose: isolate Kiro-Go and upstream account routing from public front-door WAF/CDN behavior.
- Model: `claude-opus-4-7`.
- Shape: concurrency 10, total 100.
- Run directory: `/tmp/kiro-opus47-local-20260515-184557`.
- Result:
  - HTTP 200: 100.
  - Success: 100 / 100.
  - Success rate: 100%.
  - Empty body: 0.
  - HTTP 200 empty body: 0.
  - Missing `message_start`: 0.
  - SSE error: 0.
  - Error JSON: 0.
  - Exceptions: 0.
  - Latency: min 1026.8 ms, p50 1496.95 ms, p90 7865.13 ms, p95 8825.725 ms, p99 10034.854 ms, max 10035.9 ms.

Verdict: PASS for Kiro-Go production container behavior.

### Front-Door 403 Control Run

- Endpoint: `https://kiro.cgtall.com/v1/messages`.
- Shape: concurrency 10, total 100.
- Client: Python `urllib` default request identity.
- Run directory: `/tmp/kiro-opus47-prod-20260515-184502`.
- Result:
  - HTTP 403: 100.
  - Body: `error code: 1010`.
  - Duration: 430.3 ms for all 100 requests.
  - Container logs showed no matching application requests for this run.
- Interpretation: this run was blocked by the public front door before Kiro-Go. It is not counted as an Opus 4.7/Kiro-Go failure. The same endpoint passed 100/100 when using normal curl-like headers.

Verdict: CONTROL ONLY, not a Kiro-Go UAT failure.

### Runtime Logs And Account State

- Docker logs during valid production runs contained real upstream Opus 4.7 pressure:
  - `INSUFFICIENT_MODEL_CAPACITY`.
  - `Too many requests, please wait before trying again.`
- Despite upstream 429s, downstream valid test runs returned no empty streams and no SSE errors.
- Post-run config state:
  - Accounts: 13.
  - Enabled accounts: 13.
  - Persisted failures: none.
- Health after runs:
  - `http://127.0.0.1:8080/health`: `{"status":"ok","uptime":257,"version":"1.0.8"}`.
  - `https://kiro.cgtall.com/health`: `{"status":"ok","uptime":257,"version":"1.0.8"}`.

### Playwright-MCP Browser UAT

- Attempted pages:
  - `https://kiro.cgtall.com/admin`.
  - `http://127.0.0.1:8080/admin`.
- Result:
  - Both navigations failed before page load with Playwright browser backend error `net::ERR_BLOCKED_BY_CLIENT`.
  - Browser tabs showed `chrome-error://chromewebdata/`.
  - Browser console had 0 messages.
  - Server logs showed no corresponding admin request, confirming the block occurred before reaching Kiro-Go.
- Screenshot evidence:
  - `kiro-playwright-blocked-20260515.png`.

Verdict: BLOCKED by Playwright/browser environment. UI screenshot correctness is not marked PASS. API and production container behavior are marked PASS based on direct HTTP and Docker evidence.

### Post-Integration Verdict

PASS for real production Opus 4.7 API behavior after upstream `1.0.8` integration:

- Public endpoint valid-client concurrency 10 / total 100: 100% success.
- Local production container concurrency 10 / total 100: 100% success.
- Empty stream regression: 0 empty bodies, 0 HTTP 200 empty bodies, 0 missing `message_start`.
- Latency: max 11.105 seconds in public valid-client run, well below the 90 second downstream budget.
- Model: `claude-opus-4-7` only, no downgrade.

Playwright visual UI validation remains blocked by `ERR_BLOCKED_BY_CLIENT` and is not passed.
