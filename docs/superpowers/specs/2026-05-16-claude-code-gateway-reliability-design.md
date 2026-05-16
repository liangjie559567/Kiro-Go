# Claude Code Gateway Reliability Design

Date: 2026-05-16

## Goal

Improve Kiro-Go as an Anthropic-compatible upstream for downstream sub2api and Claude Code usage. The first optimization phase targets the failure modes observed in production: Opus 4.7 queue timeouts, HTTP/2 stream resets, weak diagnostics during 100x10 tests, and missing request-level evidence for account/session concurrency.

## Scope

- Keep `/v1/messages`, `/v1/messages/count_tokens`, `/v1/responses`, and OpenAI-compatible routes working for the local sub2api deployment.
- Preserve existing prompt-injection filtering and Claude Code system-prompt sanitization.
- Improve concurrency health without simply raising global concurrency to 10 and overloading one Kiro account.
- Add observability for production validation: queue wait, first-token latency, attempts, tool-use count, account, region, and Claude Code session headers.
- Map upstream failures to Anthropic-style error types so Claude Code and sub2api can classify them correctly.

## Root Cause

The account pool selected an account and only later incremented `activeConnections`. Under high concurrency, several goroutines could select the same apparently idle account before any of them called `BeginRequest`. This allowed bursts to pile onto one Kiro account, creating upstream capacity errors even when other accounts were available.

The previous request log also lacked enough data to distinguish:

- gateway queue wait versus upstream latency;
- first-token latency versus total latency;
- single-attempt failures versus retry exhaustion;
- normal text turns versus tool-use turns;
- Claude Code session/agent correlation.

## Design

### Atomic Account Reservation

Add `AccountPool.BeginNextForModelExcept(model, excluded)` that selects an account and increments `activeConnections` while holding the same pool lock. The method returns a release function. Claude, OpenAI Chat, and OpenAI Responses retry loops use this atomic reservation before calling upstream.

This keeps the existing health and least-connections strategy meaningful under concurrency.

### Request Reliability Metadata

Extend request logs with:

- `claudeCodeSessionId`
- `claudeCodeAgentId`
- `queueWaitMs`
- `firstTokenMs`
- `attempts`
- `toolUseCount`

The admin request-log table surfaces these values under the latency column. Request stats aggregate queue wait, first-token time, attempts, and tool-use count.

### Error Mapping

Add a central Claude upstream error classifier:

- rate limit: HTTP 429, `rate_limit_error`
- transient network / HTTP2 reset / upstream 5xx: HTTP 503, `overloaded_error`
- timeout/deadline: HTTP 504, `timeout_error`
- auth expiry: HTTP 401, `authentication_error`
- quota/billing: HTTP 402, `billing_error`

Streams that have already started still emit SSE `error` events, because HTTP status cannot be changed after headers are sent.

## Verification Plan

Automated:

- `go test ./...`
- `go build ./...`
- targeted tests for atomic account reservation, request-log metadata, Opus admission, and error mapping.

Production:

- rebuild Docker service;
- verify `/health`;
- run real sub2api non-stream 100 requests at concurrency 10;
- run real sub2api stream 100 requests at concurrency 10;
- verify response correctness by unique marker per request;
- collect latency distribution and request-log evidence;
- capture admin UI screenshots with browser automation.

## Limits

This phase cannot make Kiro upstream capacity equivalent to the official Anthropic API if the available Kiro accounts have lower concurrency quota. It does make overload visible, classified, and less likely to be caused by Kiro-Go routing itself.
