# Phase 7: Observability, Latest-Code UAT, and Acceptance Evidence - Context

**Gathered:** 2026-05-21
**Status:** Ready for planning
**Mode:** Auto-generated from locked 07-SPEC and read-only code/UAT/sub2api research

<domain>
## Phase Boundary

Phase 7 completes the v1.1 sustainable health milestone by making the Opus 4.7 contract auditable end to end. Kiro-Go must expose safe request-log, readiness, header, and admin evidence; sub2api evidence must distinguish readiness-blocked scheduling from upstream 429/529 and account temporary unschedulable state; final PASS must depend on aligned latest-code stream/non-stream evidence or an explicit blocked-capacity verdict.

</domain>

<decisions>
## Implementation Decisions

- Add explicit request-log fields for requested model, effective model, admission readiness status, safe concurrency, retry-after, circuit state, pressure reason, and content-success evidence source.
- Reuse `/admin/api/fleet/readiness` logic as the authoritative admission snapshot rather than creating a second readiness implementation.
- Add a safe `/admin/api/acceptance/evidence` endpoint that summarizes Phase 7 evidence requirements and recent Opus 4.7 log coverage without reading secrets or probing upstream.
- Define an offline UAT evidence schema and validator under `docs/superpowers/uat/phase7-latest-code/`; the harness must use explicit environment variables and never read runtime secret files.

</decisions>

<code_context>
## Existing Code Insights

- `proxy/request_log.go` owns request-log schema, per-request updates, stable fallback marking, and real content-success classification.
- `proxy/ecosystem_ops.go` owns scheduler preview and `/admin/api/fleet/readiness`; it already exposes status, safe concurrency, retry-after, reason codes, account rows, and content-continuity stats.
- `proxy/handler.go` owns retry/admission paths for Claude, OpenAI Chat, and OpenAI Responses, plus safe pressure headers.
- `/www/sub2api` already logs `kiro_go_readiness_decision` and stores account temp-unschedulable, usage, and ops-error state. Phase 7 should require these as UAT artifacts, not rebuild the provider.

</code_context>

<specifics>
## Specific Ideas

- Full generation PASS requires `healthy|degraded` readiness with `safeConcurrency > 0`, 100/100 non-stream, 100/100 stream, real content success for every request, zero stable fallback, and zero replay-after-content violations.
- Blocked readiness or `safeConcurrency=0` can only produce blocked-capacity PASS or generation blocked by upstream capacity.
- Stable fallback, empty completion, and transport-only HTTP 200 never count as Opus 4.7 generation success.

</specifics>

<deferred>
## Deferred Ideas

- Fully automated live 100/100 UAT execution is intentionally gated by runtime readiness and explicit environment variables.
- sub2api code changes are out of scope unless evidence shows its existing Phase 5 readiness provider no longer emits the required fields.

</deferred>
