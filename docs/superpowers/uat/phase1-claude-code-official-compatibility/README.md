# Phase 1 Claude Code Official Compatibility UAT Harness

This directory contains a real UAT harness for Kiro-Go Phase 1 Claude Code official compatibility.

The harness exercises the Anthropic-compatible surface exposed by Kiro-Go and writes all run artifacts under `runs/<run-id>/`. It does not read repository config files, `data/config.json`, or recovery files. Runtime inputs come only from environment variables.

## Requirements

- Node.js 18 or newer
- A running Kiro-Go instance
- Optional: a running sub2api instance for black-box comparison

## Environment

Required for Kiro-Go checks:

- `KIRO_GO_BASE_URL`
- `KIRO_GO_API_KEY`
- `KIRO_GO_ADMIN_PASSWORD`

Optional for sub2api black-box check:

- `SUB2API_BASE_URL`
- `SUB2API_API_KEY`

If required variables are missing, the related checks are marked `BLOCKED_BY_ENV`. The harness does not fabricate `PASS` for checks it cannot execute.

## Run

```bash
cd docs/superpowers/uat/phase1-claude-code-official-compatibility
node run-phase1-uat.js
```

Optional fixed run id:

```bash
UAT_RUN_ID=manual-001 node run-phase1-uat.js
```

Artifacts are written to:

- `runs/<run-id>/summary.json`
- `runs/<run-id>/UAT-RESULT.md`
- redacted raw response files for each executed check
- `UAT-RESULT.md` at this directory root, updated to the latest run

## Coverage

The harness covers:

- `/v1/messages` non-stream
- `/v1/messages` streaming SSE
- `/v1/messages` tool-use response shape
- `/v1/messages` tool-result follow-up
- `/v1/messages/count_tokens`
- `/v1/models`
- `/admin/api/claude-code/readiness`
- `/admin/api/claude-code/model-readiness`
- `/admin/api/request-logs`
- Optional sub2api black-box `/v1/models`

## SSE Parser

Parse a saved Anthropic SSE file:

```bash
node parse-anthropic-sse.js runs/<run-id>/messages-stream.sse
```

The parser outputs JSON with event order, message metadata, reconstructed text content, and reconstructed tool input summaries when present.

## Secret Handling

The harness sends API keys and admin password only in HTTP headers. It redacts common secret-bearing headers and values before writing artifacts. Do not paste raw terminal environment exports into UAT reports.
