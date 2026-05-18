# Claude Code P0-P3 Optimization Design

## Goal

Improve Kiro-Go as a Claude Code downstream target so `/www/sub2api` can continue making real calls with high reliability, low avoidable latency, complete core tool availability, and clear observability.

## Scope

This phase focuses on adapter-layer correctness and production safety:

- Preserve Claude Code core tools when Kiro payload budgets require tool trimming.
- Make tool trimming observable without logging secrets or full schemas.
- Keep malformed-payload self recovery and account failover behavior separated.
- Verify through Go tests, Docker deployment, health checks, and a real `/www/sub2api` smoke request.

This phase does not implement a full virtual tool directory or rolling Docker deployment. Those remain future work.

## Architecture

Kiro-Go remains the Anthropic-compatible adapter between Claude Code/sub2api and Kiro IDE. The payload guard continues to be the boundary where oversized or malformed-prone Kiro requests are made safe. The request log becomes the operational contract: it must show enough metadata to explain whether tool visibility or payload trimming affected a request.

## P0 Stability

Malformed Kiro requests remain request-specific. Kiro-Go retries once with conservative guarding only when Kiro returns malformed/improperly formed request errors and the conservative guard actually changes the payload. Capacity and rate-limit errors remain account/model scheduling signals.

## P1 Concurrency And Latency

Existing admission gates, sticky sessions, and breakers remain in place. This phase avoids changing admission limits directly; instead it improves evidence by keeping queue wait, first token, attempts, and tool counts visible in request logs.

## P2 Tool Completeness

When current tools exceed Kiro-safe limits, Kiro-Go ranks tools before trimming. Claude Code orchestration and editing tools are treated as core and kept above ordinary MCP tools. Prompt-mentioned tools remain high priority.

The core set is:

- `agent`, `task`
- `todoRead`, `todoWrite`
- `bash`, `read`, `write`, `edit`, `multiEdit`, `glob`, `grep`, `ls`
- `webFetch`, `webSearch`

Tool logs must include names retained and names trimmed, capped to a small count and sanitized to names only.

## P3 Observability And Verification

Request logs expose:

- original/final payload bytes
- trim flag and count
- current tool count and schema bytes
- kept tool names
- trimmed tool names

Verification requires:

- targeted proxy tests for tool retention and request log metadata
- `go test ./...`
- Docker rebuild of `kiro-go`
- health checks for Kiro-Go and sub2api
- real sub2api `/v1/messages` smoke without printing API keys
- post-deploy error log count

## Risks

Keeping more core tools may force less relevant MCP tools out of the Kiro payload. This is intentional for Claude Code UX: losing `Agent`/`Task`/edit tools is more damaging than losing an unmentioned MCP tool. Future virtual tool loading can reduce this tradeoff.

