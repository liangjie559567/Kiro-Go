# Kiro-Go Claude Code 60 Minute UAT Result

Date: 2026-05-23

## Verdict

PASS with one operational caveat: the 60 minute service/API window passed, but the original monitor process stopped after 29 samples because `docker logs` output exceeded Node `execSync` buffer (`ENOBUFS`). The service did not show a captured health/API failure at that point. A lighter monitor continued for 31 more minute samples.

## Code Changes Under Verification

- Claude Code / `claude-cli` fallback now returns a retryable Anthropic `overloaded_error` instead of an HTTP 200 assistant compatibility turn when the request is detected as Claude Code development traffic.
- Non-Claude-Code compatibility traffic still keeps the assistant-shaped fallback.
- Auto-refresh scope `all` now includes cooling accounts so background refresh can refresh every account expiration state.

## Fresh Verification

- `go test ./... -count=1`: pass.
- `git diff --check`: pass.
- Docker health:
  - `kiro-go-kiro-go-1`: healthy.
  - `sub2api`: healthy.
  - `sub2api-postgres`: healthy.
  - `sub2api-redis`: healthy.
- Fresh API capture:
  - Kiro-Go `/health`: `ok`.
  - sub2api `/health`: `ok`.
  - Opus 4.7 model readiness routing reason: `schedulable accounts available`.
  - Opus 4.7 locally schedulable accounts: 21.
  - Opus 4.7 generation blocked accounts: 0.
  - Fleet readiness: `degraded`, reason `admission_pressure`, safe concurrency 1.
- Fresh Claude Code probe:
  - `claude --model claude-opus-4-7 -p 'Return exactly DEV_PROBE_PASS' --output-format json`
  - Result: `DEV_PROBE_PASS`, `is_error=false`.
- Existing real development probe:
  - `/tmp/kiro-go-claude-dev-real-20260523`
  - `node test.js`: `test-pass`.
  - Claude result: `DEV_REAL_PASS`.
- sub2api database evidence:
  - Recent `claude-cli` usage records in the last 30 minutes: 30.

## 60 Minute Monitor Evidence

Machine-readable summary: `monitor-summary.json`.

Original monitor:

- Samples: 29.
- Kiro-Go health HTTP 200: 29/29.
- sub2api health HTTP 200: 29/29.
- Claude Code readiness HTTP 200: 29/29.
- Opus 4.7 model readiness HTTP 200: 29/29.
- Minimum locally schedulable accounts: 21.
- Maximum generation blocked accounts: 0.
- Maximum risk-group cooling accounts: 0.

Light continuation monitor:

- Samples: 31.
- Kiro-Go health `ok`: 31/31.
- sub2api health `ok`: 31/31.
- Minimum locally schedulable accounts: 17.
- Maximum generation blocked accounts: 0.
- Maximum risk-group cooling accounts: 0.

Combined evidence covers 60 minute samples.

## Readiness Interpretation

Current fleet readiness is still `degraded`, but the fresh reason is `admission_pressure`, not `cooling_down`. The model readiness endpoint shows Opus 4.7 remains schedulable with 21 locally schedulable accounts and zero generation-blocked accounts. The readiness state is therefore an admission-control throttle, not a total routing outage.

## Not Counted As Pass

Playwright MCP browser verification is not counted as passing in this report because the earlier Playwright MCP session closed its transport before completion. API, Docker, database, Go test, and real Claude Code CLI evidence are counted.
