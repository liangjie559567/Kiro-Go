# Claude Code Compatibility Matrix

**Phase:** 01 - Claude Code Official Compatibility  
**Generated:** 2026-05-20  
**Scope:** Kiro-Go's Claude Code-facing Anthropic Messages API behavior.

This matrix separates two claims:

- **Claude Code compatibility**: Kiro-Go-controlled behavior is shaped so Claude Code can call it successfully.
- **Official Anthropic parity**: behavior is proven equivalent to Anthropic upstream. Local estimates, emulation, or Kiro-upstream-opaque behavior are not marked official `PASS`.

Machine-readable source: [`docs/claude-code-compatibility-matrix.json`](./claude-code-compatibility-matrix.json)

| Requirement | Feature | Surface | Claude Code Compatibility | Official Anthropic Parity | Evidence |
|---|---|---|---|---|---|
| CC-01 | Compatibility matrix | Matrix docs and `/admin/api/claude-code/compat` | PASS (`documented_contract`) | PARTIAL (`evidence_split`) | Matrix docs; `TestAdminClaudeCodeCompatibilityEndpointReturnsGatewaySettings` |
| CC-02 | Model alias and readiness | `/v1/models`, readiness APIs | PASS (`deterministic_alias_mapping`) | PARTIAL (`kiro_model_cache_and_local_schedulability`) | `TestParseModelAndThinkingNormalizesOfficialOpus47Names`; readiness tests |
| CC-03 | Tool-loop fidelity | `/v1/messages` tool shapes | PASS (`translated_and_logged`) | PARTIAL (`upstream_tool_shape_unproven`) | translator/request-log/readiness tests |
| CC-04 | Anthropic SSE event ordering | streaming `/v1/messages` | PASS (`anthropic_sse_writer`) | PARTIAL (`kiro_go_chunked_complete_input`) | `TestClaudeSSEWriterMixedThinkingTextToolOrder`; stream error tests |
| CC-05 | Large-context payload policy | large Claude Code payloads | PASS (`local_payload_guard`) | PARTIAL (`local_trim_or_reject_policy`) | payload guard and request-log tests |
| CC-06 | Count tokens | `/v1/messages/count_tokens` | PASS (`estimated`) | PARTIAL (`estimated`) | token estimator and layered readiness tests |
| CC-06 | Prompt cache and `max_tokens=0` | cache controls and zero-token calls | PASS (`local_zero_output`) | BLOCKED_BY_UPSTREAM (`local_zero_output`) | cache tracker and max-token-zero tests |
| CC-06 | Thinking and assistant prefill | thinking config and assistant prefill | EMULATED_PASS (`emulated_text_prefill`) | PARTIAL (`emulated_text_prefill`) | thinking and readiness tests |
| CC-07 | Real UAT evidence | Phase 1 UAT harness | BLOCKED_BY_ENV (`uat_harness_ready`) | BLOCKED_BY_UPSTREAM (`live_evidence_required`) | UAT harness and future run artifacts |

## PASS Rules

- A matrix row may use official `PASS` only when current-code evidence proves the official behavior.
- `estimated`, `emulated`, `local_zero_output`, `local_payload_guard`, `local_trim_or_reject_policy`, and `upstream_*_unproven` modes must not be official `PASS`.
- Live UAT failures caused by missing credentials, unavailable local services, or real Kiro capacity must be recorded as `BLOCKED_BY_ENV`, `BLOCKED_BY_UPSTREAM`, or `FAIL` with evidence. They must not be converted into PASS.

