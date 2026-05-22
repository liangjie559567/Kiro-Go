# Kiro-Go Claude Code Real Dev Correctness UAT

Date: 2026-05-22 10:58 Asia/Shanghai
Verdict: PASS

## Environment

- Kiro-Go: `http://127.0.0.1:8080`
- sub2api: `http://127.0.0.1:18080`
- Claude Code: real local CLI `claude-cli/2.1.143`
- Model: `claude-opus-4-7`
- Docker: rebuilt with latest Kiro-Go image before UAT

## Result

Single real Claude Code development task passed.

- Claude API success: `1/1`
- Done markers: `1/1`
- Capacity/fallback text seen: `0`
- npm test exit: `0`
- sub2api error rows: `0`
- File edited: `src/task1.js`

Bounded 2-agent real Claude Code development run passed while readiness was already degraded but locally schedulable.

- Claude API success: `2/2`
- Done markers: `2/2`
- Capacity/fallback text seen: `0`
- npm test exit: `0`
- sub2api error rows: `0`
- Files edited: `src/task1.js`, `src/task2.js`

## Readiness

Before Docker rebuild, Kiro-Go and sub2api health endpoints were OK. After rebuild, fleet readiness started healthy with `safeConcurrency=10`.

During real Claude Code runs, readiness degraded because of model admission pressure, not because of the fixed fallback path:

- Single-task post-readiness: `degraded`, `half_open`, `reasonCodes=["admission_pressure"]`, `safeConcurrency=2`
- 2-agent post-readiness: `degraded`, `half_open`, `reasonCodes=["admission_pressure"]`, `safeConcurrency=2`
- Browser screenshot showed Opus fleet health with `Circuit: half_open`, `Cooling: 3`, `Temporary limited: 3`

This is acceptable for this UAT because the contract is not "always healthy"; it is "do not fake-complete Claude Code development turns with assistant fallback text." The degraded state was visible and still locally schedulable.

## Evidence

- Single summary: `api/single-summary.json`
- Single Claude result: `claude/single-claude-results.json`
- Single npm test: `claude/single-npm-test.stdout.log`
- Single DB evidence: `api/single-db-evidence.json`
- 2-agent summary: `api/concurrent-summary.json`
- 2-agent Claude result: `claude/concurrent-claude-results.json`
- 2-agent npm test: `claude/concurrent-npm-test.stdout.log`
- 2-agent DB evidence: `api/concurrent-db-evidence.json`
- Edited files: `claude/task1.js`, `claude/task2.js`
- Playwright-MCP screenshot: `playwright/kiro-go-real-dev-correctness-api-readiness.png`

## Conclusion

PASS. Real Claude Code development requests through sub2api and Kiro-Go completed with real file edits and passing tests. No run contained capacity fallback text as a completed assistant answer, and sub2api recorded no error rows for the verified windows.
