# Kiro Temporary Limit Fix UAT

Date: 2026-05-19
Verdict: PASS

## Scope

Validate the Kiro-Go fix for suspicious temporary-limit 429 handling and re-check the sub2api Claude account path using the exact model IDs the UI selects.

## What Changed

- suspicious temporary-limit 429 is classified as `temporary_limited`, not `suspended`;
- cooldown floor is adaptive instead of a fixed 1 hour;
- outward `Retry-After` is raised to about 60 seconds for this case;
- Claude Code-compatible error output remains `429 rate_limit_error`;
- the UAT now uses the real UI-selected model IDs:
  - Sonnet: `claude-sonnet-4-5-20250929`
  - Opus: `claude-opus-4-7`

## Evidence

### Browser

PASS:

- Kiro-Go admin dashboard rendered.
- Kiro-Go API readiness panel rendered.
- Kiro-Go request logs rendered.
- sub2api dashboard rendered.
- sub2api accounts rendered.
- sub2api groups rendered.
- sub2api usage rendered.
- browser console errors: none.
- browser page errors: none.
- browser API request failures: none.

Artifacts:

- `kiro-admin-dashboard.png`
- `kiro-admin-api-readiness.png`
- `kiro-admin-request-logs.png`
- `sub2api-dashboard.png`
- `sub2api-accounts.png`
- `sub2api-groups.png`
- `sub2api-usage.png`

### API

PASS:

- direct Kiro-Go `/v1/messages` with `claude-opus-4.7` returned `200`.
- sub2api `/v1/messages` with `claude-sonnet-4-5-20250929` returned `200`.
- sub2api `/v1/messages` with `claude-opus-4-7` returned `200`.

### Database

PASS:

- PostgreSQL usage logs contain rows created during this UAT window.
- matched rows for `kiro_claude_01` + `claude` key + `claude` group: `6/6`.
- models recorded:
  - `claude-sonnet-4-5-20250929`
  - `claude-opus-4-7`

## Conclusion

The earlier `no available accounts` result was a model-ID mismatch, not a broken account connection.

The sub2api test account `kiro_claude_01` is healthy, active, schedulable, and works for the exact models the UI selects. Kiro-Go also now handles suspicious temporary-limit 429s with a recoverable backoff instead of a hard 1-hour suspension.
