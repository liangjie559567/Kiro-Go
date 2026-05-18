# Claude Code P0-P3 Optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Preserve Claude Code core tool availability and expose payload/tool trimming evidence while keeping `/www/sub2api` real calls working.

**Architecture:** Extend the existing payload guard result with capped kept/trimmed tool-name metadata, then propagate those fields into request logs. The payload guard remains the only place that mutates Kiro payload shape.

**Tech Stack:** Go, Docker Compose, Kiro-Go proxy package, sub2api downstream smoke checks.

---

### Task 1: Tool Trim Metadata

**Files:**
- Modify: `proxy/payload_guard.go`
- Modify: `proxy/payload_guard_test.go`

- [x] Add failing tests that assert kept and trimmed tool names are recorded when current tools are capped.
- [x] Implement capped name collection in `sanitizeCurrentToolsForPayload`.
- [x] Keep core Claude Code tools above ordinary MCP tools when trimming.
- [x] Run targeted payload guard tests.

### Task 2: Request Log Propagation

**Files:**
- Modify: `proxy/request_log.go`
- Modify: `proxy/request_log_test.go`

- [x] Add failing tests that request log entries include kept and trimmed tool names.
- [x] Add request log fields for `payloadKeptTools` and `payloadTrimmedTools`.
- [x] Populate fields from `payloadGuardResult`.
- [x] Run targeted request log tests.

### Task 3: Production Verification

**Files:**
- No source changes expected.

- [ ] Run `go test ./...`.
- [ ] Rebuild and restart `kiro-go` with Docker Compose.
- [ ] Check `http://127.0.0.1:8080/health`.
- [ ] Check `http://127.0.0.1:18080/health`.
- [ ] Execute a real `/www/sub2api` `/v1/messages` smoke request without printing the API key.
- [ ] Count Kiro-Go and sub2api errors since deploy.
