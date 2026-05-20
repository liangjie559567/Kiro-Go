# sub2api Opus 4.7 Stable 200 UAT

This UAT verifies the downstream contract:

- sub2api receives no Kiro-Go generation response with HTTP 429, 502, or 503.
- Opus 4.7 stream and non-stream calls remain syntactically valid.
- Kiro-Go request logs record any internally suppressed retryable failure.

Required environment:

- `SUB2API_BASE_URL`, default `http://127.0.0.1:18080`
- `SUB2API_API_KEY`
- `MODEL`, default `claude-opus-4.7`
- `ROUNDS`, default `10`
- `CONCURRENCY`, default `10`

Run:

```bash
SUB2API_BASE_URL=http://127.0.0.1:18080 \
SUB2API_API_KEY="$SUB2API_API_KEY" \
ROUNDS=10 \
CONCURRENCY=10 \
node docs/superpowers/uat/sub2api-opus47-stable-200/run-stable-200-uat.js
```

Pass criteria:

- Every HTTP response status is `200`.
- Every response body is valid JSON or valid Anthropic SSE for the chosen endpoint.
- No response body contains gateway-level `HTTP 429`, `HTTP 502`, or `HTTP 503`.
- Every successful response includes real assistant text, thinking, or tool_use content.
- Empty HTTP `200` completions fail this UAT even if the JSON/SSE envelope is valid.
- Kiro-Go request logs must show `contentSuccess=true` for successful samples.
- Kiro-Go Admin request logs show stable fallback metadata if upstream capacity was exhausted.
