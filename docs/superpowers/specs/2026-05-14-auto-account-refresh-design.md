# Auto Account Refresh Design

## Goal

Add backend-driven scheduled batch account refresh so Kiro-Go can refresh account subscription, usage, and token data even when the admin page is closed.

The existing admin UI already supports manual single-account refresh and selected-account batch refresh. This feature adds persistent scheduling and configuration while preserving the manual paths.

## User Experience

The admin UI exposes an automatic refresh control area with:

- Enable or disable automatic refresh.
- Refresh interval in minutes.
- Refresh scope:
  - Enabled accounts only.
  - All accounts.
- Runtime status:
  - Last run time.
  - Next scheduled run time.
  - Last run success and failure counts.

Default behavior:

- Automatic refresh is enabled.
- Interval is 60 minutes.
- Scope is enabled accounts only.
- Valid interval range is 5 to 1440 minutes.

Manual single-account refresh and manual selected-account batch refresh remain available.

## Configuration

Add an `autoRefresh` section to the persisted config:

```json
{
  "autoRefresh": {
    "enabled": true,
    "intervalMinutes": 60,
    "scope": "enabled"
  }
}
```

Supported `scope` values:

- `enabled`: refresh only enabled accounts.
- `all`: refresh all configured accounts.

The config layer applies defaults when fields are absent so existing installations upgrade without manual config edits.

## Backend Architecture

Add a backend account refresh scheduler that starts with the HTTP service. The scheduler:

- Reads the current `autoRefresh` config.
- Sleeps until the next scheduled run.
- Re-reads config before each run so admin UI changes take effect without restarting the service.
- Selects accounts according to the configured scope.
- Refreshes accounts one by one using shared refresh logic.
- Reloads the account pool after a run.
- Records last run metadata for the admin UI.

The scheduler must allow only one automatic refresh run at a time. If a previous run is still active when the next interval arrives, the new run is skipped and status should make that visible through timestamps or counts.

## Shared Refresh Logic

Extract the existing refresh behavior into a shared helper used by:

- Single account refresh API.
- Manual batch refresh API.
- Automatic scheduler.

The helper should:

- Refresh the access token when needed or when the current account info call proves the token is invalid.
- Fetch account subscription and usage information.
- Persist refreshed account info.
- Persist refreshed token and profile ARN when returned.
- Treat suspended-account status updates consistently with the current single-account API behavior.

Single account failures must not stop batch or scheduled runs from continuing with the remaining accounts.

## Admin API

Expose endpoints for the UI to read and update automatic refresh settings and status. A compact shape is sufficient:

```json
{
  "settings": {
    "enabled": true,
    "intervalMinutes": 60,
    "scope": "enabled"
  },
  "status": {
    "running": false,
    "lastStartedAt": 1778769265,
    "lastFinishedAt": 1778769278,
    "nextRunAt": 1778772865,
    "lastSuccess": 12,
    "lastFailed": 1,
    "lastSkipped": false
  }
}
```

The update API validates:

- `intervalMinutes` must be between 5 and 1440.
- `scope` must be `enabled` or `all`.
- `enabled` must be boolean.

## Error Handling

- Invalid settings requests return HTTP 400 with a JSON error.
- One failed account increments failure count and does not stop the run.
- Missing accounts in a manual batch count as failures.
- Automatic scheduler failures are reflected in status, not shown through browser alerts.
- Manual batch refresh keeps its current alert-based result behavior.

## Testing

Add focused backend tests for:

- Config defaults for absent `autoRefresh`.
- Config validation for interval and scope.
- Scope filtering for enabled-only and all-accounts refresh runs.
- Scheduler overlap protection.
- Manual batch refresh still reports success and failure counts.

Frontend verification should cover:

- Auto-refresh controls render correctly in both Chinese and English.
- Saving settings updates the backend.
- Status values display without requiring page reload after save.

## Out Of Scope

- Full job history table.
- Per-account scheduled refresh intervals.
- Parallel account refresh.
- Cron expression support.
- Push notifications or browser background timers.
