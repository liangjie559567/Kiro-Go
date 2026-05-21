# Open Source Research Summary

Checked at: 2026-05-21 17:04 Asia/Shanghai

## Sources

- `jwadow/kiro-gateway`: https://github.com/jwadow/kiro-gateway
- `zeoak9297/KiroSwitchManager`: https://github.com/zeoak9297/KiroSwitchManager
- `chaogei/Kiro-account-manager`: https://github.com/chaogei/Kiro-account-manager
- Anthropic API errors: https://docs.claude.com/en/api/errors
- Anthropic streaming: https://platform.claude.com/docs/en/build-with-claude/streaming
- Claude Code errors/retry behavior: https://code.claude.com/docs/en/errors

## Findings Applied

- Gateway retry must be bounded. `kiro-gateway` style account retry/failover is useful before any downstream bytes are sent, but retrying after client-visible SSE has started risks duplicate events or tool calls.
- Upstream `429`, `5xx`, and Kiro `INSUFFICIENT_MODEL_CAPACITY` should be treated as recoverable inside the gateway first, then converted to a deliberate terminal downstream response after the gateway budget is exhausted.
- Claude Code can retry server errors, overloads, timeouts, temporary rate limits, and disconnects. Therefore, after Kiro-Go exhausts its internal retry/wait budget, it should not return wording or status that encourages the same Claude Code turn to retry indefinitely.
- `KiroSwitchManager` public materials reinforce the same architecture: keep account switching, token refresh, protocol translation, and proxy retry policy separated; do not classify all upstream failures as bad accounts.

## Local Design Decision

Kiro-Go keeps the existing StableDownstream transport contract for Opus 4.7 sub2api/Claude Code traffic, but changes the Claude-format waiting path from unbounded repeated content-continuity waits to a single bounded wait. If capacity does not recover, Kiro-Go returns a complete HTTP 200 stable fallback message/SSE completion and records it as a stable fallback/content failure, not a model success.

The fallback text was also changed to avoid telling Claude Code to retry the same turn.

