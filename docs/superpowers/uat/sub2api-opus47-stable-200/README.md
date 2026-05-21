# sub2api Opus 4.7 Stable 200 UAT

This UAT verifies the downstream contract:

- sub2api receives no Kiro-Go generation response with HTTP 429, 502, or 503.
- Opus 4.7 stream and non-stream calls remain syntactically valid.
- Kiro-Go request logs record any internally suppressed retryable failure.

Required environment:

- `SUB2API_BASE_URL`, default `http://127.0.0.1:18080`
- `SUB2API_API_KEY`
- `MODEL`, default `claude-opus-4.7`
- `NON_STREAM_TOTAL`, default `100`
- `STREAM_TOTAL`, default `100`
- `CONCURRENCY`, default `4`
- `KIRO_GO_BASE_URL`, default `http://127.0.0.1:8080`
- `KIRO_GO_ADMIN_PASSWORD`, optional when Kiro-Go admin APIs require authentication
- `KIRO_GO_READINESS_FILE`, optional pre-captured readiness JSON file
- `READINESS_WAIT_SECONDS`, default `600`
- `ABORT_ON_CAPACITY_FAILURE`, default `true`
- `UAT_OUTPUT_DIR`, optional output directory

Run:

```bash
SUB2API_BASE_URL=http://127.0.0.1:18080 \
SUB2API_API_KEY="$SUB2API_API_KEY" \
NON_STREAM_TOTAL=100 \
STREAM_TOTAL=100 \
CONCURRENCY=4 \
node docs/superpowers/uat/sub2api-opus47-stable-200/run-stable-200-uat.js
```

Pass criteria:

- Kiro-Go fleet readiness for the requested model is `healthy` or `degraded` with `safeConcurrency > 0` before sending generation traffic.
- The runner limits effective concurrency to Kiro-Go `safeConcurrency`.
- Every HTTP response status is `200`.
- Every response body is valid JSON or valid Anthropic SSE for the chosen endpoint.
- No response body contains gateway-level `HTTP 429`, `HTTP 502`, or `HTTP 503`.
- Every successful response includes real assistant text, thinking, or tool_use content.
- Every generation response must include the per-request marker `ok <index>` in real assistant text or stream delta content.
- Empty HTTP `200` completions fail this UAT even if the JSON/SSE envelope is valid.
- Kiro-Go request logs must show `contentSuccess=true` for successful samples.
- Kiro-Go Admin request logs show stable fallback metadata if upstream capacity was exhausted.

Blocked criteria:

- If readiness stays `blocked` or `safeConcurrency=0`, the runner exits with code `3` and writes `summary.json` with `BLOCKED_BY_UPSTREAM_CAPACITY`.
- If any response contains Kiro-Go upstream capacity fallback, `admission_pressure`, or `opus47_budget_exhausted`, the runner stops early, exits with code `3`, and writes `summary.json` with `BLOCKED_BY_UPSTREAM_CAPACITY`.
- A blocked run is not a generation PASS. It means the test correctly avoided adding load while Kiro-Go reports no safe Opus 4.7 capacity.
