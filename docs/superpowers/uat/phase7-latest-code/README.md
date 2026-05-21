# Phase 7 Latest-Code UAT Evidence

This directory defines the repeatable evidence contract for the v1.1 final acceptance gate.

The harness must not read runtime secret files such as `data/config.json`, `.env`, token stores, keychains, browser sessions, or recovery snapshots. Runtime URLs and credentials must be supplied explicitly through environment variables and redacted before writing artifacts.

## Verdict Rules

- Full generation PASS requires Kiro-Go readiness `healthy` or `degraded` with `safeConcurrency > 0` before running Opus 4.7 generation traffic.
- Non-stream PASS requires 100/100 HTTP completions, 100/100 real content successes, and zero stable fallback successes.
- Stream PASS requires 100/100 valid streams, 100/100 real content successes, zero stable fallback successes, and zero replay-after-content violations.
- If readiness is `blocked` or `safeConcurrency = 0`, the only valid verdict is blocked-capacity PASS or generation blocked by upstream capacity.
- Stable fallback, empty completion, and transport-only HTTP 200 never count as real Opus 4.7 content success.

## Required Bundle

Each run should write a timestamped directory containing:

- `evidence-manifest.json`
- `UAT-RESULT.md`
- `api/fleet-readiness.json`
- `api/acceptance-evidence.json`
- `api/request-logs.json`
- `request-results/non-stream.jsonl`
- `request-results/stream.jsonl`
- `headers/*.txt`
- `sub2api/readiness-decisions.log`
- `sub2api/usage-logs.json`
- `sub2api/ops-errors.json`
- `sub2api/account-scheduling-state.json`
- `screenshots/*.png`
- `console-summary.json`
- `redaction-report.json`

Validate a completed run:

```bash
node docs/superpowers/uat/phase7-latest-code/validate-evidence.js docs/superpowers/uat/phase7-latest-code/runs/<timestamp>/evidence-manifest.json
```
