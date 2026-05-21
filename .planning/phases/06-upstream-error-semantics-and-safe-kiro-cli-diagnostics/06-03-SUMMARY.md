# 06-03 Summary: Safe Kiro CLI Diagnostics API, UI, and Audit Evidence

## Status

Complete.

## Implemented

- Added safe Kiro CLI diagnostics service with executable detection, `KIRO_CLI_HOME` presence, version probing, allowlisted explicit command runs, bounded output, redaction, and audit ring buffer.
- Added admin API:
  - `GET /admin/api/kiro-cli/diagnostics`
  - `POST /admin/api/kiro-cli/diagnostics`
- Added Opus 4.7 CLI model-list state detection: `present`, `unavailable`, or `unknown`.
- Added lightweight existing-admin UI card for CLI path, availability, version, CLI home, router state, Opus 4.7 model-list state, and latest redacted result.

## Verification

- `go test ./proxy -run 'TestKiroCLI|TestAdminUI' -count=1`
