# Screenshot Analysis

## Kiro-Go Admin

- `kiro-admin-dashboard.png`: PASS. The screenshot is authenticated and shows the Kiro-Go admin shell, version `v1.0.8`, running status, account/request/success/failure/token/credit summary, and masked account rows.
- `kiro-admin-claude-readiness.png`: PASS. The screenshot shows the Claude Code readiness panel, setup guidance, capability badges, and explicit `PASS`/`PARTIAL` statuses for messages, toolReference, toolSchemaValidation, countTokens, maxTokensZero, assistantPrefill, and fineGrainedToolStreaming.
- `kiro-admin-model-readiness.png`: PASS. The screenshot shows the model readiness table in the Claude Code panel with requested model mapping text and account rows containing enabled/healthy/lists-model/schedulable/reason columns.
- `kiro-admin-request-logs.png`: PARTIAL. The screenshot is authenticated and still shows the Claude Code readiness/model section, but it does not visibly show the request-log table. Request-log evidence is available in `kiro-request-logs.json`, so browser evidence for this specific visual surface is partial.

## sub2api Admin

- `sub2api-dashboard.png`: PASS. The screenshot is authenticated and shows the admin dashboard, sidebar navigation, API key/account/request/user/token statistics, model distribution, and recent usage widgets. The onboarding overlay is visible but does not prevent the dashboard content from being visible.
- `sub2api-accounts.png`: PASS. The screenshot is authenticated and shows Account Management with filters, account rows, platform/type/status/schedulable/groups/usage windows/action columns.
- `sub2api-usage.png`: PASS. The screenshot is authenticated and shows Usage Records, total requests/tokens/cost/duration, model distribution, group distribution, endpoint distribution including `/v1/messages`, and filters.
- `sub2api-groups-or-channels.png`: PASS. The screenshot is authenticated and shows Group Management with `openai`, `google`, and `claude` groups, account availability/capacity/usage/status columns. The onboarding overlay is visible but core group content remains visible.

## Console And Page Errors

- PASS. `playwright-summary.json` records zero console messages, zero page errors, and zero failed requests for the final authenticated browser run.
