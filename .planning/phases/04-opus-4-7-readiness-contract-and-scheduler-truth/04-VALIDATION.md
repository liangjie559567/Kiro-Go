---
phase: 04
slug: opus-4-7-readiness-contract-and-scheduler-truth
status: approved
nyquist_compliant: true
wave_0_complete: true
created: 2026-05-21
---

# Phase 04 - Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go standard `testing` |
| **Config file** | none |
| **Quick run command** | `go test ./proxy ./pool -count=1` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~60 seconds |

---

## Sampling Rate

- **After every task commit:** Run the focused package command for touched files, normally `go test ./proxy ./pool -count=1`.
- **After every plan wave:** Run `go test ./...`.
- **Before `$gsd-verify-work`:** `go test ./...` must be green.
- **Max feedback latency:** 90 seconds for focused package feedback.

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 04-01-01 | 01 | 1 | RDY-03 | T-04-01,T-04-03 | Add in-memory account/model content-success evidence without persisting secrets | pool/unit | `go test ./pool -run 'Test.*ContentSuccess' -count=1` | ✅ | ⬜ pending |
| 04-01-02 | 01 | 1 | RDY-03 | T-04-02,T-04-03 | Fresher success biases only already-eligible account selection | pool/unit | `go test ./pool -run 'TestBeginNextForModel|Test.*ContentSuccess' -count=1` | ✅ | ⬜ pending |
| 04-02-01 | 02 | 2 | RDY-02,RDY-03 | T-04-04,T-04-05 | Shared real-content predicate rejects fallback, empty, and transport-only success | unit/integration | `go test ./proxy -run 'TestRequestLog|Test.*Stable.*Fallback|Test.*ContentSuccess' -count=1` | ✅ | ⬜ pending |
| 04-02-02 | 02 | 2 | RDY-02,RDY-03 | T-04-04,T-04-06 | Claude/OpenAI handlers record pool success only from real content evidence | handler/integration | `go test ./proxy -run 'Test.*ContentSuccess|TestHandleClaude|TestOpenAI' -count=1` | ✅ | ⬜ pending |
| 04-03-01 | 03 | 3 | RDY-04 | T-04-07,T-04-09 | Scheduler preview and fleet readiness share account eligibility reason codes | handler/unit | `go test ./proxy -run 'TestSchedulerPreview|TestFleetReadiness' -count=1` | ✅ | ⬜ pending |
| 04-03-02 | 03 | 3 | RDY-01,RDY-02,RDY-03,RDY-04 | T-04-08,T-04-10 | Fleet readiness exposes versioned contract, exact safe concurrency, and separate evidence counters | handler/unit | `go test ./proxy -run TestFleetReadiness -count=1` | ✅ | ⬜ pending |
| 04-04-01 | 04 | 3 | RDY-05 | T-04-11,T-04-12,T-04-13 | Bounded retry exhaustion returns retryable pressure metadata and attempt evidence | handler/integration | `go test ./proxy -run 'Test.*AttemptBudget|Test.*Opus.*Pressure' -count=1` | ✅ | ⬜ pending |
| 04-04-02 | 04 | 3 | RDY-06 | T-04-14 | Started Claude/OpenAI streams cannot transparently replay on another account | handler/integration | `go test ./proxy -run 'Test.*Stream.*Retry|Test.*Stream.*Started|Test.*SSE' -count=1` | ✅ | ⬜ pending |
| 04-05-01 | 05 | 4 | RDY-01,RDY-02,RDY-03,RDY-04 | T-04-15,T-04-17 | Existing fleet card renders contract labels and separate success/fallback evidence | UI/static + Go | `rg -n 'Contract|Safe concurrency|Retry after|Reasons|Real content success|Stable fallbacks|Opus 4\\.7 fleet health' web/index.html` | ✅ | ⬜ pending |
| 04-05-02 | 05 | 4 | RDY-01,RDY-02,RDY-03,RDY-04 | T-04-15,T-04-16 | Fleet readiness UI block avoids raw credential/runtime-secret fields and escapes dynamic text | UI/static | `awk '/async function loadFleetReadiness\\(\\)/,/async function loadWebSearchDiagnostics\\(\\)/' web/index.html \| rg -n 'data/config|rawHeaders|KIRO_CLI_HOME|accessToken|refreshToken|clientSecret|apiKey|credential'; test $? -ne 0` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Existing Go test infrastructure covers all phase requirements. No framework install is needed.

---

## Manual-Only Verifications

All phase behaviors have automated verification. Browser/screenshot evidence is deferred to Phase 7 unless Phase 4 materially changes admin layout beyond the existing fleet readiness card.

---

## Validation Sign-Off

- [x] All tasks have automated verification commands.
- [x] Sampling continuity: no 3 consecutive tasks without automated verify.
- [x] Wave 0 is complete because existing Go test infrastructure is present.
- [x] No watch-mode flags.
- [x] Feedback latency target is under 90 seconds for focused tests.
- [x] `nyquist_compliant: true` set in frontmatter.

**Approval:** approved 2026-05-21
