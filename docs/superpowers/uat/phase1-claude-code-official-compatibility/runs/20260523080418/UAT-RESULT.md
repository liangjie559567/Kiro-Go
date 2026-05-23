# Phase 1 Claude Code Official Compatibility UAT Result

Run ID: 20260523080418
Started: 2026-05-23T08:04:18.930Z
Finished: 2026-05-23T08:04:31.850Z
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
| /v1/messages non-stream | PASS | Received Anthropic message response. |
| /v1/messages stream | PASS | Received parseable Anthropic SSE stream. |
| /v1/messages tool-use shape | PASS | Received tool_use content block. |
| /v1/messages max_tokens=0 | PASS | Received compatible max_tokens=0 response. |
| /v1/messages assistant prefill | PASS | Accepted assistant prefill request. |
| /v1/messages fine-grained tool streaming | PASS | Received fine-grained tool streaming request. |
| /v1/messages tool_reference | PASS | Accepted tool_reference request. |
| /v1/messages tool-result follow-up | PASS | Accepted tool_result follow-up conversation. |
| /v1/messages/count_tokens | PASS | Received token count. |
| /v1/models | PASS | Received model list. |
| /admin/api/claude-code/readiness signals | PASS | Claude Code readiness reflected recent boundary probes. |
| /admin/api/claude-code/model-readiness | PASS | Admin endpoint returned success status. |
| /admin/api/request-logs boundary signals | FAIL | Expected recent request logs for all boundary probes. |
| sub2api black-box optional /v1/models | FAIL | HTTP 401 |

## Artifacts

- Run directory: runs/20260523080418/
- Raw responses are saved with secrets redacted.
- `summary.json` contains machine-readable results.
