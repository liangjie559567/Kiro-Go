# Opus 4.7 Sustainable Health Research

**Milestone:** v1.1 Opus 4.7 Sustainable Health  
**Researched:** 2026-05-21  
**Scope:** `sub2api -> Kiro-Go -> Kiro Opus 4.7` sustainable calling behavior

## Summary

Kiro-Go already has the core ingredients for Opus 4.7 reliability: account-level cooldown, model-level breaker/admission, stable downstream waits, request logs, fleet readiness, and admin diagnostics. The next milestone should turn those ingredients into a strict downstream contract for `/www/sub2api`.

The contract cannot honestly mean "Kiro upstream never fails." Opus 4.7 is dynamic, tier/region dependent, and can hit real upstream capacity or account limits. The practical target is:

- if at least one real account can serve Opus 4.7, Kiro-Go keeps routing successfully;
- if the pool is degraded or blocked, Kiro-Go exposes actionable `degraded`/`blocked` readiness with retry timing and safe concurrency;
- sub2api consumes that readiness before sending generation requests;
- synthetic fallback or transport-level HTTP 200 never counts as real Opus 4.7 content success.

## External References

### Kiro Official Docs

Confirmed facts:

- Kiro has both desktop IDE and CLI forms.
- Kiro CLI supports interactive login and headless `KIRO_API_KEY` authentication.
- Kiro CLI/IDE models are dynamic and tier/region dependent.
- Opus 4.7 is listed as an experimental model with 1M context and premium cost characteristics.
- Official docs do not expose a stable low-level Kiro runtime API contract for Kiro-Go to depend on.

Design implication: Kiro-Go must treat Kiro model lists, auth source shape, region, and error body as variable protocol surfaces. Readiness should be based on recent live evidence plus cached model lists, not static assumptions.

### `jwadow/kiro-gateway`

Useful patterns:

- layered model resolution: alias, normalization, cached models, hidden models, passthrough;
- account-level sticky selection with per-request account exclusion;
- circuit breaker and exponential backoff;
- first-token retry only before stream content starts;
- debug logging that preserves request/response evidence for hard failures.

Do not copy source directly because the project is AGPL-3.0. Borrow design ideas only.

### `zeoak9297/KiroSwitchManager`

The public repository is release-only, but README/release notes document useful operational expectations:

- CLI/IDE dual-mode account workflows;
- token refresh plus model-list validation;
- quota display and automatic switching;
- WebSearch injection visibility;
- user-facing account diagnostics.

Do not copy machine-ID or desktop account switching behavior into Kiro-Go. Kiro-Go should stay a server/API gateway.

## Local Kiro CLI Findings

Kiro-Go Docker images install `kiro-cli` and a `kiro` alias, and Compose sets:

- `CONFIG_PATH=/app/data/config.json`
- `KIRO_CLI_HOME=/app/data/kiro-cli`

The current host shell does not have `kiro` or `kiro-cli` in `PATH`; this is acceptable because the runtime container is the intended environment. Requirements must support configurable CLI path and status diagnostics rather than hard-coding host paths.

Security constraint:

- do not scan or read CLI caches, `data/config.json`, or recovery candidates by default;
- expose only safe existence/version/status fields until an admin explicitly requests import/probe;
- redact secrets from every diagnostic response.

## sub2api Findings

sub2api already provides:

- OpenAI-compatible account/channel model mapping;
- usage logs with requested/upstream model and mapping chain;
- account scheduling fields including rate limit, overload, temp unschedulable, load factor, concurrency;
- failover on 401/403/429/529/5xx;
- response header filtering that can allow `retry-after` and selected upstream headers;
- channel monitor history, but monitor data is not a hard scheduling gate.

Main gap:

sub2api does not query Kiro-Go `/admin/api/fleet/readiness?model=...` before choosing/sending to a Kiro-Go upstream. It only learns from failures after it has already sent traffic.

Recommended integration:

1. Configure a Kiro-Go readiness provider on the sub2api OpenAI-compatible account or channel.
2. Cache readiness for a short TTL.
3. For `blocked`, mark the sub2api account temporarily unschedulable using Kiro-Go `retryAfterSeconds`.
4. For `degraded`, cap sub2api concurrency to Kiro-Go `safeConcurrency`.
5. For `healthy`, proceed through existing priority/load/concurrency scheduling.
6. Record readiness status, safe concurrency, retry-after, and content-success evidence in usage/ops logs.

## Kiro-Go Current State

Already implemented:

- Opus 4.7 model normalization;
- account model filtering and session selection;
- per-account cooldown for temporary limits;
- model breaker for model capacity pressure;
- admission gate with open/half-open/degraded states;
- stable downstream wait/heartbeat path;
- fleet readiness endpoint;
- request logs with attempt trace and content success metadata.

Known gaps:

- latest-code sub2api 10x10 Opus 4.7 evidence is not a final current PASS;
- recent UAT showed Opus 4.7 fleet degraded with safe concurrency 1;
- 429 classification still depends partly on response strings;
- sub2api can mistake fallback/transport success for true model success unless explicit headers/readiness are consumed;
- CLI diagnostics are not a safe explicit first-class admin flow yet.

## Design Direction

Recommended v1.1 architecture:

1. Kiro-Go is the source of truth for Opus 4.7 fleet readiness.
2. sub2api uses readiness before dispatching requests to Kiro-Go.
3. Kiro-Go emits explicit headers/fields for content success, stable fallback, retry-after, and selected readiness state.
4. Kiro-Go learns `account + model` recent success from real calls and folds it into readiness/scheduler preview.
5. Kiro CLI integration is explicit and safe: version/path/status diagnostics first, admin-triggered import/probe later.
6. UAT gates require both stream and non-stream latest-code sub2api runs, with aligned Kiro-Go request logs, sub2api usage/logs, readiness, and screenshots.

## Expanded Research Findings

### Kiro-Go Local Code

Kiro-Go already has the main building blocks for sustainable Opus 4.7 health:

- `/admin/api/fleet/readiness` aggregates fleet status, circuit state, retry-after, safe concurrency, schedulable accounts, cooldown counts, and content-continuity signals.
- Scheduler preview explains enabled state, cooldown, token expiry, usage limits, and model cache state.
- Opus 4.7 model normalization covers dot, hyphen, dated, latest, and thinking aliases.
- Account scheduling already filters by model list, cooldown, usage limits, token/session state, runtime health, and model breaker state.
- Request logs already record model/account, retry budget, admission waits, capacity retry, stable fallback, and content-success metadata.

The strongest local implementation direction is not a scheduler rewrite. It is to turn the existing readiness, admission gate, model breaker, and request logs into a stable machine contract for sub2api, then make scheduler preview and fleet readiness explain the same account/model eligibility state.

Key gaps:

- readiness is usable, but still more human/admin oriented than a strict sub2api contract;
- row-level scheduler preview can disagree with final fleet totals when model breaker state is applied later;
- upstream error classification still depends partly on response strings;
- CLI diagnostics are not yet a safe first-class admin flow for official Kiro CLI state;
- stable fallback must remain separate from real Opus 4.7 content success.

### sub2api Local Code

sub2api already has account/channel model mapping, sticky routing, load-aware scheduling, failover, temp-unschedulable state, 429/529 handling, response header filtering, usage logs, and ops/system logs.

The missing integration is dispatch-before-send readiness consumption. The recommended insertion point is candidate filtering before account concurrency is consumed:

- advanced scheduler: add readiness checks inside `defaultOpenAIAccountScheduler.isAccountRequestCompatible`;
- legacy/load-aware scheduler: reuse the same helper across sticky, candidate, and fallback-wait paths;
- readiness queries must use the effective upstream model after channel/account/compact mapping, not only the client-requested model.

Recommended sub2api behavior:

- `healthy`: schedule normally and cache briefly;
- `degraded`: schedule within Kiro-Go `safeConcurrency`, lower priority or cap concurrency;
- `blocked`: skip the Kiro-Go candidate or short-term unschedule it without writing permanent account errors;
- readiness timeout/error: support fail-closed for Opus 4.7 production safety and fail-open only for controlled rollout.

### `jwadow/kiro-gateway`

Useful design ideas from `kiro-gateway`:

- bounded multi-account failover rather than fake success;
- per-request account exclusion so one failed account is not retried immediately;
- failure classification into recoverable versus fatal classes;
- first-token retry for streams only before any downstream content is emitted;
- layered model resolution: alias, normalization, cached/dynamic models, hidden models, and pass-through;
- debug evidence around request/stream conversion failures.

Kiro-Go should borrow only architecture ideas. The repository is AGPL-3.0, so Kiro-Go should not copy source, tests, constants, or implementation structure.

Sources:

- `kiro-gateway` README: https://github.com/Jwadow/kiro-gateway/blob/main/README.md
- `kiro-gateway` license: https://github.com/Jwadow/kiro-gateway/blob/main/LICENSE
- Account manager: https://github.com/Jwadow/kiro-gateway/blob/main/kiro/account_manager.py
- Failure classification: https://github.com/Jwadow/kiro-gateway/blob/main/kiro/account_errors.py
- HTTP retry: https://github.com/Jwadow/kiro-gateway/blob/main/kiro/http_client.py
- Streaming retry: https://github.com/Jwadow/kiro-gateway/blob/main/kiro/streaming_openai.py
- Model resolver: https://github.com/Jwadow/kiro-gateway/blob/main/kiro/model_resolver.py

### `KiroSwitchManager` and Official Kiro CLI

`KiroSwitchManager` is release-only/no-source in public, but its README and releases show operational expectations around multi-account switching, token refresh, CLI/IDE dual mode, model locking, WebSearch injection, quota display, CLI SQLite import, and rollback. Those are useful operator signals, but not safe server defaults.

Official Kiro CLI exposes safer read-only diagnostic commands:

- `kiro-cli --version`;
- `kiro-cli whoami --format json`;
- `kiro-cli doctor --all --format json-pretty`;
- `kiro-cli diagnostic --force --format json-pretty`;
- `kiro-cli chat --list-models --format json`;
- `kiro-cli settings list --format json-pretty`.

Kiro-Go should use official commands as explicit, redacted diagnostics. It should not parse token stores, SQLite auth databases, browser sessions, keychains, or API keys by default.

Official docs and public pages indicate:

- CLI and IDE are separate official surfaces, with command routing between `kiro`, `kiro-cli`, and `kiro ide`;
- Opus 4.7 availability is tier, region, rollout, and account dependent;
- the local account's model list is stronger evidence than a static model whitelist;
- no stable public CLI quota command was identified, so real-time quota should remain unknown or operator-provided in v1.1.

Sources:

- KiroSwitchManager: https://github.com/zeoak9297/KiroSwitchManager
- KiroSwitchManager releases: https://github.com/zeoak9297/KiroSwitchManager/releases
- Kiro CLI commands: https://kiro.dev/docs/cli/reference/cli-commands/
- Kiro CLI authentication: https://kiro.dev/docs/cli/authentication/
- Kiro CLI models: https://kiro.dev/docs/cli/models/
- Kiro Opus 4.7 blog: https://kiro.dev/blog/opus-4-7/
- Kiro pricing: https://kiro.dev/pricing/
- Kiro FAQ: https://kiro.dev/faq/

## Candidate Requirements

- Kiro-Go exposes an Opus 4.7 readiness contract that distinguishes `healthy`, `degraded`, and `blocked`.
- sub2api can consume Kiro-Go readiness before generation dispatch.
- Kiro-Go and sub2api both preserve `Retry-After` and retryability semantics.
- Kiro-Go classifies upstream failures structurally before falling back to string matching.
- Kiro-Go tracks real model content success separately from stable fallback or transport-level success.
- Kiro-Go provides safe Kiro CLI diagnostics without reading secrets by default.
- UAT proves 100/100 stream and 100/100 non-stream only when real content success is present.
