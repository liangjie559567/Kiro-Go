# Account Health Check Design

## Goal

Add a backend-driven account health check so Kiro-Go can periodically verify whether enabled accounts can still load available models. A successful model list call means the account is healthy. A failed model list call means the account is unhealthy, and the system can optionally disable that account automatically.

The feature is configured from the admin UI and continues running after the admin page is closed.

## Scope

In scope:

- Add an independent account health check setting, separate from automatic account refresh.
- Persist health check settings in `config.json`.
- Run scheduled checks in the backend.
- Check enabled accounts only.
- Reuse the existing `ListAvailableModels(account)` behavior as the health signal.
- Optionally disable unhealthy accounts after failed model loading.
- Show settings and runtime status in the admin settings page.

Out of scope:

- Checking manually disabled accounts.
- Per-account health check intervals.
- Parallel health checks.
- Retrying failed health checks within the same run.
- Changing the existing manual "Load Models" account detail button.

## Configuration

Add `HealthCheckConfig` to `config.Config`:

- `enabled`: whether scheduled health checks run. Default: `false`.
- `intervalMinutes`: scheduled check interval. Default: `60`.
- `autoDisableUnhealthy`: whether failed checks disable accounts. Default: `false`.

Validation:

- `intervalMinutes` must be an integer from `5` to `1440`.
- Missing config in existing installs is normalized to the defaults above.
- Explicit `enabled: false` must be preserved when loading persisted config.

## Backend Behavior

Add a health check scheduler owned by `proxy.Handler`, following the existing auto-refresh scheduler pattern:

- Start when the handler starts.
- Wait according to `HealthCheckConfig`.
- React when settings change.
- Prevent overlapping runs.
- Keep runtime status in memory.

Each run:

1. Read current health check settings.
2. If disabled, clear the next run timestamp and wait.
3. Select accounts where `enabled=true`.
4. For each account, call `ListAvailableModels(account)`.
5. Count successful checks and failed checks.
6. If a check fails and `autoDisableUnhealthy=true`, update that account:
   - `enabled=false`
   - `banStatus="UNHEALTHY"`
   - `banReason` contains the model loading error
   - `banTime` is the current Unix timestamp
7. Reload the account pool if any account was disabled.
8. Update status with last run times and result counts.

Single-account failures must not stop the rest of the run.

## Admin API

Add two authenticated admin endpoints:

- `GET /admin/api/health-check`
- `POST /admin/api/health-check`

`GET` returns:

- `settings`: persisted health check settings
- `status`: runtime scheduler status

`POST` accepts:

- `enabled`
- `intervalMinutes`
- `autoDisableUnhealthy`

After a successful update, the backend saves the config and reschedules the next health check.

## Runtime Status

Expose status fields similar to auto refresh:

- `running`
- `lastStartedAt`
- `lastFinishedAt`
- `nextRunAt`
- `lastSuccess`
- `lastFailed`
- `lastDisabled`
- `lastSkipped`

`lastDisabled` counts accounts automatically disabled during the last completed run.

## Admin UI

Add an "Account Health Check" card to the settings tab.

Controls:

- Enable scheduled health check.
- Interval in minutes.
- Automatically disable unhealthy accounts.
- Save button.

Status display:

- Running
- Last run
- Next run
- Last result with success, failed, disabled, and skipped values when present

The UI should reuse the existing settings page style and i18n pattern. Chinese and English text must be added.

The account detail modal's manual "Load Models" button remains unchanged. It continues to be a manual check only and does not disable accounts.

## Error Handling

- Invalid settings return HTTP 400 with an error message.
- Health check failures are recorded per account and logged.
- If account disabling fails, that account counts as failed and the run continues.
- The scheduler must not panic on empty account lists.

## Tests

Add or update Go tests for:

- Default health check config is disabled.
- Persisted config normalization preserves explicit disabled values.
- Health check config validation rejects intervals outside `5-1440`.
- Enabled-only account selection.
- Batch health checks continue after one account fails.
- Auto-disable updates only failed enabled accounts when configured.
- Scheduler overlap protection.

Manual verification:

- Settings page loads health check settings.
- Saving valid settings succeeds.
- Invalid intervals are rejected in the UI.
- Backend tests pass with `go test ./...`.
