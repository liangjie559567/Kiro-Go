# Claude Code GAD Concurrency UAT

Run: `gad-20260521160014`

## Verdict

PASS.

## What Ran

- 6 parallel local `claude -p` processes, simulating concurrent GAD-style subagents.
- 36 concurrent sub2api requests against `POST /v1/messages`.
- Mixed stream and non-stream requests.
- Postgres evidence read from `usage_logs` and `ops_error_logs`.

## Results

- Claude agents: `6/6` ok.
- API probes: `36/36` ok.
- HTTP status mix: all `200`.
- Database:
  - `usage_logs` rows for `api_key_id=2`: `42`
  - Claude CLI rows: `6`
  - GAD probe rows: `36`
  - `ops_error_logs` rows: `0`

## Timing

- Claude p50: `8733 ms`
- Claude p95: `10622 ms`
- API p50: `3668 ms`
- API p95: `13370 ms`

## Evidence

- `summary.json`
- `claude/claude-agents-summary.json`
- `api/api-concurrency-summary.json`
- `db/sub2api-db-evidence.json`
- `logs/pre-health.json`
- `logs/post-health.json`

## Notes

- Current live capacity stayed healthy throughout.
- No overload or retryable-error path was triggered in this run.
- This verifies the concurrent success path, not the overloaded fallback path.
