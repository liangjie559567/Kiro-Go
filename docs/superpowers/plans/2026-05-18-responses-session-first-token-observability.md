# Responses Session First Token Observability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve Claude Code downstream reliability by adding Responses session continuity, safer pre-first-token streaming retry behavior, and admin visibility for routing/payload pressure metadata.

**Architecture:** Keep the existing Kiro-Go adapter boundaries. Add a small in-memory Responses session store in `proxy`, keep stream retry logic before any SSE bytes are written, and expose already-recorded request metadata in the admin UI.

**Tech Stack:** Go 1.21, `net/http`, existing Kiro-Go proxy tests, vanilla `web/index.html`, Docker Compose.

---

### Task 1: Responses Session Continuity

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/handler_test.go`

- [ ] Write failing tests for `/v1/responses` with `previous_response_id` restoring prior assistant tool calls and tool outputs.
- [ ] Implement a bounded in-memory session store keyed by response id.
- [ ] Save normalized request/response turn state after successful Responses calls.
- [ ] Restore saved messages/tools/tool_choice before converting Responses to Chat/Kiro payload.
- [ ] Run targeted tests.

### Task 2: First Token Retry Safety

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/handler_test.go`

- [ ] Write failing tests for stream attempt retry before first event and no retry after first SSE event.
- [ ] Track first-token/first-event state through stream callbacks.
- [ ] Return retryable errors only before stream start.
- [ ] Run targeted tests.

### Task 3: Admin Observability UI

**Files:**
- Modify: `web/index.html`

- [ ] Add request-log rendering for `routingDecision`, `routingPressure`, `payloadDeferredTools`, `payloadMaterializedToolRefs`, and `payloadCompactedPairs`.
- [ ] Add an admin pressure fetch beside existing request-log refresh.
- [ ] Keep display compact and read-only.

### Task 4: Verification

**Files:**
- No source files beyond Tasks 1-3.

- [ ] Run `go test ./...`.
- [ ] Rebuild `kiro-go` via Docker Compose.
- [ ] Verify Kiro-Go and `/www/sub2api` health.
- [ ] Run real sub2api `/v1/messages` smoke.
- [ ] Run real sub2api `/v1/responses` smoke if the downstream route is enabled.
- [ ] Check post-deploy logs for `HTTP 400`, `Improperly`, 5xx, panic, EOF, and stream errors after the service is ready.
