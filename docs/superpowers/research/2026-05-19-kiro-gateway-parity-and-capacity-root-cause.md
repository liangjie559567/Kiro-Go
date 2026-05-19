# Kiro Gateway Parity and Capacity Root Cause - 2026-05-19

## Root Cause

The observed account failure:

```text
HTTP 429 from Kiro IDE: {"message":"I am experiencing high traffic, please try again shortly.","reason":"INSUFFICIENT_MODEL_CAPACITY"}
```

is model/provider capacity pressure, not an account credential, quota, suspension, or token-refresh failure. Kiro-Go previously classified every `429` as `rate_limited`, then persisted it into account health and account cooldown. That made healthy accounts look failed during upstream high-traffic windows.

The fix adds a separate `model_capacity` failure reason. Account-level failure persistence now skips `model_capacity`; model/admission backoff still handles the pressure.

## Reference Findings

- `jwadow/kiro-gateway` treats HTTP `429` as recoverable for failover, not as malformed request failure. It tracks tried accounts within a request and uses circuit-breaker/backoff.
- AWS official Bedrock-style capacity documentation describes model capacity and throttling as service/model throughput pressure; retry/backoff is appropriate, but it is not evidence that a user account token is bad.
- Claude Code official behavior depends on preserving long context, file contents, and tool results. Kiro-Go should avoid local trimming below Claude model context expectations.

## Current Kiro-Go Fixes

- `INSUFFICIENT_MODEL_CAPACITY`, `experiencing high traffic`, and `model capacity` classify as `model_capacity`.
- `model_capacity` does not increment account failure count, runtime account failures, or persistent cooldown.
- Normal account-level rate limit still classifies as `rate_limited`.
- Suspicious temporary account limits still classify as `temporary_limited` and return non-retryable downstream `409` to stop Claude Code retry storms.
- Default payload guard preserves Claude Code-sized tool/file history around 300-500KB and rejects oversized current user input separately.

## Kiro-Go vs kiro-gateway Capability Gaps

- First-token retry: kiro-gateway has a generic first-token streaming retry wrapper. Kiro-Go has admission/backoff and SSE protection, but first-token timeout behavior can be made more explicit and configurable.
- Error taxonomy: Kiro-Go now has finer handling for temporary account limits and model capacity. Continue separating account failures from provider/model pressure.
- Payload trimming: kiro-gateway exposes `AUTO_TRIM_PAYLOAD` and max payload knobs. Kiro-Go has richer structured guard metadata; making limits configurable would improve operator control.
- Runtime endpoint support: kiro-gateway documents runtime endpoint behavior and lazy model-cache assumptions. Kiro-Go should keep model cache optimistic on cold start and avoid over-filtering accounts.
- Request observability: Kiro-Go request logs now include payload sizes, trimming, routing, attempts, and Claude Code headers. Keep extending this rather than relying on upstream error text alone.

## Next Optimization Targets

- Add configurable model-capacity cooldown/admission values per model.
- Add explicit first-token timeout retry settings for stream paths.
- Add dashboard grouping for `model_capacity`, `temporary_limited`, `rate_limited`, and account-auth/quota failures.
- Add UAT that simulates mixed `model_capacity -> success` without marking intermediate accounts failed.
- Re-run real stream 100x10 after upstream temporary limits cool down; do not mark PASS while upstream is actively limiting accounts.
