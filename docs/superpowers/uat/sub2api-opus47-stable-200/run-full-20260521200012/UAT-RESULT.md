# UAT Result: sub2api Opus 4.7 Stable 200 Full Pass

Verdict: PASS

Date: 2026-05-21 Asia/Shanghai

## Scope

This UAT verifies the real Docker deployment, real browser admin UI, database evidence, and the downstream sub2api path for `claude-opus-4-7` with strict 100/100 non-stream + 100/100 stream validation.

## Final Result

- Kiro-Go Docker service rebuilt and came up healthy.
- `http://127.0.0.1:8080/health` returned `{"status":"ok","uptime":1050,"version":"1.0.8"}`.
- Playwright-MCP verified the real admin page at `https://kiro.cgtall.com/admin`.
- The UAT runner completed 100 non-stream and 100 stream requests with strict marker and content validation.
- DB usage records confirm 100 non-stream and 100 stream rows for `api_key_id=14`.
- Temporary UAT key id 14 was soft-deleted after evidence collection.

## Evidence Summary

| Check | Result | Evidence |
| --- | --- | --- |
| Docker health | PASS | `docker ps` showed `kiro-go-kiro-go-1` healthy and `sub2api` healthy |
| Kiro-Go health | PASS | `curl http://127.0.0.1:8080/health` |
| Browser admin page | PASS | Playwright-MCP snapshot showed `运行中`, version `v1.0.8`, `24` accounts |
| Non-stream 100/100 | PASS | `docs/superpowers/uat/sub2api-opus47-stable-200/run-full-20260521200012/non-stream.jsonl` |
| Stream 100/100 | PASS | `docs/superpowers/uat/sub2api-opus47-stable-200/run-full-20260521200012/stream.jsonl` |
| Summary | PASS | `docs/superpowers/uat/sub2api-opus47-stable-200/run-full-20260521200012/summary.json` |
| DB usage | PASS | `docs/superpowers/uat/sub2api-opus47-stable-200/run-full-20260521200012/evidence/db-usage-api-key-14.txt` |
| Readiness before/after | PASS | `docs/superpowers/uat/sub2api-opus47-stable-200/run-full-20260521200012/evidence/readiness-before.json`, `.../readiness-after-full.json` |
| Screenshot archive | PASS | `docs/superpowers/uat/sub2api-opus47-stable-200/run-full-20260521200012/evidence/kiro-go-admin-full-pass.png` |

## Validation Notes

- The runner requires real assistant content and the per-request `ok <index>` marker.
- Stream validation rejects empty SSE shells and requires real delta content.
- The stored `sample` field is truncated preview text, so the PASS decision is based on the structured fields `ok`, `contentOk`, and `markerOk`, not preview text alone.
- Readiness stayed `degraded` with `safeConcurrency=10`, which is acceptable for this paced run because it remained above zero and the runner limited traffic accordingly.

## DB Evidence

`api_key_id = 14` recorded:

- non-stream: `n=100`, `input_tokens=13100`, `output_tokens=1681`
- stream: `n=100`, `input_tokens=13000`, `output_tokens=2069`

## Cleanup

- Temporary key `14` was updated to `inactive` with `deleted_at` set.
- `/tmp/sub2api-uat-key` and `/tmp/sub2api-uat-key-id` were removed.
- Leak scan found no exposed `sk-` tokens or bearer strings in the archived evidence set.
