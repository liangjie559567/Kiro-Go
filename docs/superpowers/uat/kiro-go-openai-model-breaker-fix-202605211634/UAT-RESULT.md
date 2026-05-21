# Kiro-Go OpenAI Model Breaker Fix Verification

Run directory: `docs/superpowers/uat/kiro-go-openai-model-breaker-fix-202605211634`

Checked at: 2026-05-21 16:34 Asia/Shanghai

## Verdict

**FIX VERIFIED LOCALLY / 100x100 STILL NOT PASS**

The code fix was verified by targeted tests and Docker health/browser checks. This is not a full 100/100 non-stream plus 100/100 stream PASS, because upstream Opus 4.7 readiness remains capacity-sensitive and the previous real sub2api pressure run had already failed.

## Root Cause Fixed

Kiro-Go already had a per-account, per-model breaker for model-specific failures. Claude request paths used `recordAccountModelFailure`, but OpenAI Chat and OpenAI Responses generation paths were only calling `recordAccountFailure` on upstream generation failures.

For `INSUFFICIENT_MODEL_CAPACITY`, account-level failure recording intentionally does not poison account health. That is correct, but it also meant OpenAI/Responses model-capacity failures did not open the model breaker. Under sub2api OpenAI/Responses traffic, this made repeated selection of the same failing account/model more likely.

## Code Change

- `proxy/handler.go`
  - OpenAI Chat stream/non-stream attempts now call `recordAccountModelFailure` for upstream generation failures.
  - OpenAI Responses stream/non-stream attempts now call `recordAccountModelFailure` for upstream generation failures.
  - Callback `OnError` paths were updated the same way.

- `proxy/handler_test.go`
  - Added `TestHandleOpenAIResponsesModelCapacityRecordsModelBreaker`.
  - The test first failed on the previous implementation, showing `ModelBlockState` had no `model_capacity` block.
  - It now passes and confirms account health is not poisoned by model capacity.

## Verification

| Check | Result | Evidence |
| --- | --- | --- |
| Target failing test | PASS | `go test ./proxy -run TestHandleOpenAIResponsesModelCapacityRecordsModelBreaker -count=1` |
| Focused regression tests | PASS | OpenAI Responses retry, stream no-replay, Claude temporary-limit fallback, Opus pressure contract |
| Broader unit tests | PASS | `go test ./pool ./proxy` |
| Docker rebuild | PASS | `docker compose up -d --build kiro-go` |
| Kiro-Go health | PASS | `api/kiro-health.json`: `status=ok`, version `1.0.8` |
| sub2api health | PASS | `api/sub2api-health.json`: `status=ok` |
| Docker service health | PASS | `logs/docker-compose-ps-after-rebuild.txt`: Kiro-Go healthy after rebuild |
| Playwright-MCP browser page | PASS for UI reachability | `screenshots/kiro-go-after-openai-model-breaker-fix-202605211634.png`: real Kiro-Go admin dashboard visible |

## Browser Screenshot Analysis

The Playwright-MCP screenshot shows the real local Kiro-Go admin page at `http://127.0.0.1:8080/admin`, not a login page or error page. Visible counters:

- Accounts: 25
- Requests: 16173
- Successes: 6736
- Failures: 9437
- Tokens: 222.8M
- Credits: 1960.1

The screenshot validates UI reachability after the Docker rebuild. It does not prove 100/100 generation success.

## Final Decision

This fix addresses a real scheduling/breaker gap found through the failed UAT evidence and external project research. The correct status is:

- Code fix: **verified**
- Docker service: **healthy**
- Browser evidence: **valid**
- 100/100 non-stream plus 100/100 stream through sub2api: **not passed in this run**

Do not mark the full sub2api pressure UAT as PASS until a real post-fix run completes all required requests and API/database/screenshots agree.
