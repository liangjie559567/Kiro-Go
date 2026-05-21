# 06-01 Summary: Structured Upstream Error Parser and Reason Mapping

## Status

Complete.

## Implemented

- Added normalized upstream HTTP error parsing with status, source, JSON fields, AWS-style code, request ID, retry-after evidence, and redacted summary.
- Added typed structured reason mapping before legacy string fallback.
- Preserved legacy error strings while redacting secret-like content.
- Added `retry-after-ms` support.

## Verification

- `go test ./pool ./proxy -run 'TestClassifyFailureReason|TestUpstreamError|TestCallKiroAPIReturnsRateLimitReset|TestCallKiroAPIRetainsTooManyRequestsBody' -count=1`
