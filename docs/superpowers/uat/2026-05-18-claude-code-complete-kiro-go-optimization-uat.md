# Claude Code Complete Kiro-Go Optimization UAT

Date: 2026-05-18

## Scope

- Kiro-Go source changes only.
- `/www/sub2api` is rebuild and real-call verification target only.
- Secrets and API keys are not printed.

## Go Tests

- Command: `go test ./...`
- Result: PASS
- Evidence time: `2026-05-18 03:42:21 CST`
- Notes: Packages reported PASS for `kiro-go`, `kiro-go/auth`, `kiro-go/config`, `kiro-go/pool`, and `kiro-go/proxy`; `kiro-go/logger` has no test files.

## Kiro-Go Local Verification

- Build/restart command: pending Task 7 Docker rebuild.
- Health URL: `http://127.0.0.1:8080/health`
- Health result before rebuild: `{"status":"ok","uptime":4619,"version":"1.0.8"}`
- `/v1/models` result before rebuild: returned JSON model list including `auto`, `auto-thinking`, `claude-opus-4.7`, `claude-opus-4.7-thinking`, `claude-opus-4.6`, `claude-sonnet-4.6`, and `claude-opus-4.5`.
- Direct `/v1/messages` smoke: pending Task 7 real smoke.

## sub2api Downstream Verification

- Rebuild/restart command: pending Task 7.
- Health URL: `http://127.0.0.1:18080/health`
- Health result: pending Task 7.
- Non-stream `/v1/messages` result: pending Task 7.
- Stream `/v1/messages` result: pending Task 7.

## Request Log Evidence

- Kiro-Go request IDs: pending Task 7.
- Models: pending Task 7.
- Accounts: pending Task 7.
- Attempts: pending Task 7.
- First-token timings: pending Task 7.
- Payload/tool trimming: readiness telemetry implemented; runtime evidence pending Task 7.
- Responses restore/readiness: readiness API implemented; runtime evidence pending Task 7.

## Failure Classification

- sub2api layer: pending Task 7.
- Kiro-Go protocol/payload layer: pending Task 7.
- Kiro-Go account/token layer: pending Task 7.
- Kiro upstream capacity/network layer: pending Task 7.
