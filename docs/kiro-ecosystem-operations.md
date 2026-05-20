# Kiro Ecosystem Operations

**Phase:** 03 - Kiro Ecosystem Operations  
**Generated:** 2026-05-20

This document describes the operator-facing Admin/API workflows for managing Kiro account fleets. Kiro-Go does not start, stop, or manage local MCP servers; Claude Code remains the MCP host.

## Credential Validation

Use `POST /admin/api/auth/credentials/validate` with `X-Admin-Password`.

Example:

```json
{
  "sourceType": "kiro_account_manager_json",
  "dryRun": true,
  "data": {
    "accounts": [
      {
        "email": "user@example.com",
        "refreshToken": "redacted",
        "region": "us-east-1"
      }
    ]
  }
}
```

The endpoint is dry-run only and reports per-account `valid`, `invalid`, or `unsupported` results. Kiro CLI / Amazon Q CLI local file discovery is intentionally not performed by the API to avoid reading local secret files; supply JSON fixtures instead.

## Account Diagnostics

Use `GET /admin/api/accounts/{id}/diagnostics`.

The response summarizes:

- auth method and refresh viability
- token expiry
- profile ARN presence
- model-list cache state
- quota and overage blocking
- proxy configuration
- cooldown and last failure reason
- runtime health

The `status`, `reason`, and `message` fields are intended for Admin UI display and operator action.

## Scheduler Preview

Use `GET /admin/api/scheduler/preview?model=claude-opus-4-7`.

This endpoint is read-only. It does not reserve an account, call Kiro upstream, increment request counters, or update cooldowns. It reports the current strategy, mapped model, candidate accounts, eligibility reasons, runtime health, cached model lists, and the preferred accounts according to local readiness.

## Fleet Readiness

Use `GET /admin/api/fleet/readiness?model=claude-opus-4-7`.

The response aggregates the scheduler preview into fleet counts:

- total and enabled accounts
- eligible accounts
- disabled accounts
- cooling accounts
- quota-blocked accounts
- model-cache misses

It also includes current auto-refresh and health-check status, including skipped counts.

## Batch Operations

Use `POST /admin/api/accounts/batch` with `action` set to `enable`, `disable`, or `refresh`.

The response keeps existing summary fields and adds:

- `results[]`: per-account status, reason, and message
- `summary`: success, failed, skipped counts

Single-account refresh, test, update, delete, model refresh, and export flows remain unchanged.

## WebSearch / MCP Diagnostics

Use `GET /admin/api/websearch/diagnostics`.

The endpoint summarizes recent WebSearch/MCP evidence from request logs:

- query
- MCP status
- result count
- injected payload bytes
- latency
- account id
- failure reason

Failure classes are `query_extraction_failed`, `no_account_available`, `kiro_mcp_http_error`, `empty_results`, and `payload_injection_failed`.
