# Phase 1 Claude Code Official Compatibility UAT Result

Run ID: kiro-go-real-boundary-20260523154026
Started: 2026-05-23T07:40:26.691Z
Finished: 2026-05-23T07:40:29.113Z
Status: FAIL

## Environment

- KIRO_GO_BASE_URL: SET
- KIRO_GO_API_KEY: [REDACTED]
- KIRO_GO_ADMIN_PASSWORD: [REDACTED]
- SUB2API_BASE_URL: SET
- SUB2API_API_KEY: [REDACTED]

## Checks

| Check | Status | Detail |
| --- | --- | --- |
| /v1/messages non-stream | FAIL | HTTP 400 |
| /v1/messages stream | FAIL | HTTP 400 |
| /v1/messages tool-use shape | FAIL | HTTP 400 |
| /v1/messages tool-result follow-up | SKIPPED | Skipped because tool-use shape check did not produce a tool_use response. |
| /v1/messages/count_tokens | PASS | Received token count. |
| /v1/models | PASS | Received model list. |
| /admin/api/claude-code/readiness | PASS | Admin endpoint returned success status. |
| /admin/api/claude-code/model-readiness | PASS | Admin endpoint returned success status. |
| /admin/api/request-logs | PASS | Admin endpoint returned success status. |
| sub2api black-box optional /v1/models | PASS | sub2api black-box model endpoint responded. |

## Artifacts

- Run directory: runs/kiro-go-real-boundary-20260523154026/
- Raw responses are saved with secrets redacted.
- `summary.json` contains machine-readable results.
