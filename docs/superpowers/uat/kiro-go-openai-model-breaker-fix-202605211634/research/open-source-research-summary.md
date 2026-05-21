# Open Source Research Summary

Checked at: 2026-05-21 16:34 Asia/Shanghai

## jwadow/kiro-gateway

Repository: https://github.com/jwadow/kiro-gateway

Subagent reviewed commit `a5292ca04c7c6231e0b47673ac3f981f5a706e1e`.

Useful findings:

- Uses layered retry: account-local HTTP retry first, then route-level account failover.
- Keeps a tried-account set per request and can fail over between accounts.
- Treats stream retry as safe only before the first downstream chunk/token is emitted.
- Keeps failed accounts in recovery/backoff and occasionally probes recovery.
- Preserves final upstream response for higher-level classification rather than hiding it in retry code.

Not copied directly:

- Error classification is coarser than Kiro-Go. It treats 429 broadly as recoverable and 5xx as fatal, while Kiro-Go already distinguishes `model_capacity`, `temporary_limited`, `rate_limited`, `upstream_5xx`, and `transient_network`.
- Its global sticky account strategy would conflict with Kiro-Go's per-model breaker and session-scoped sticky behavior.
- The repository license is AGPL-3.0, so no code was copied.

Decision applied to Kiro-Go:

- Keep Kiro-Go's finer failure taxonomy.
- Ensure OpenAI Chat and OpenAI Responses generation attempts use Kiro-Go's existing per-account, per-model breaker path, matching the Claude path.
- Preserve the existing no-replay-after-stream-start boundary.

## zeoak9297/KiroSwitchManager

Repository: https://github.com/zeoak9297/KiroSwitchManager

The public repository contains README, screenshots, and releases only; it does not expose source code.

Useful findings from public material:

- Manages multiple Kiro accounts and account switching.
- Emphasizes one account bound to one machine id.
- Supports automatic account switching by quota/round-robin/random strategy.
- Provides local reverse proxy behavior and stream heartbeat.

Not copied directly:

- No public source code is available.
- CLI/IDE credential import and account switching are not appropriate for this server-side fix because they would cross credential-safety boundaries.
- Random or max-quota default scheduling is less explainable than Kiro-Go's health, least-connection, and per-model breaker behavior.

Decision applied to Kiro-Go:

- Preserve account-local machine id behavior.
- Do not read or manipulate Kiro CLI/IDE credential stores.
- Keep temporary limits and model capacity scoped locally rather than globally disabling accounts.
