# Opus 4.7 sub2api 100x10 Full-Stack Verification

Date: 2026-05-16

Scope:
- Downstream route: `sub2api -> Kiro-Go -> upstream Kiro/Claude-compatible path`.
- Base URLs: `http://127.0.0.1:18080` for sub2api, `http://127.0.0.1:8080` for Kiro-Go.
- Model under test: `claude-opus-4-7`.
- Load target: 100 non-stream requests and 100 stream requests, each at concurrency 10.
- Content correctness target: every response must return its unique marker exactly, without prompt-pollution warning text.

## Result Summary

PASS for the requested Opus 4.7 load shape through sub2api:

| Mode | Total | Concurrency | HTTP 200 | Exact content correct | Warning text | Failed | Avg ms | P50 ms | P95 ms | P99 ms | Max ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Non-stream | 100 | 10 | 100 | 100 | 0 | 0 | 21079 | 24157 | 42490 | 47655 | 52566 |
| Stream | 100 | 10 | 100 | 100 | 0 | 0 | 2824 | 1366 | 7450 | 8256 | 8647 |

The stream path is materially faster for this short-marker workload. Non-stream remained correct and error-free, but suffered high tail latency because the real upstream returned Opus capacity pressure during the run and Kiro-Go absorbed it via retry/admission instead of surfacing failures downstream.

## Commands Run

Non-stream:

```bash
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js sync 100 10 claude-opus-4-7 opus47-sync-100x10-20260516232231
```

Stream:

```bash
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js stream 100 10 claude-opus-4-7 opus47-stream-100x10-20260516232621
```

Kiro-Go compile/test verification:

```bash
go test ./...
go build ./...
docker compose up -d --build
curl -fsS http://127.0.0.1:8080/health
```

## Artifacts

- Non-stream summary: `docs/superpowers/uat/sub2api-100x10-2026-05-16/opus47-sync-100x10-20260516232231-summary.json`
- Non-stream per-request JSONL: `docs/superpowers/uat/sub2api-100x10-2026-05-16/opus47-sync-100x10-20260516232231.jsonl`
- Stream summary: `docs/superpowers/uat/sub2api-100x10-2026-05-16/opus47-stream-100x10-20260516232621-summary.json`
- Stream per-request JSONL: `docs/superpowers/uat/sub2api-100x10-2026-05-16/opus47-stream-100x10-20260516232621.jsonl`
- Browser MCP screenshot: `docs/superpowers/uat/playwright-2026-05-16/kiro-go-admin-opus47-100x10-20260516.png`
- Kiro-Go admin stats capture: `/tmp/kiro_request_stats_after_opus47_final.json`

## Content Correctness

Each request used a unique marker and asked the model to return exactly that marker and nothing else.

Fresh JSONL validation checked both runs:

```json
{
  "file": "docs/superpowers/uat/sub2api-100x10-2026-05-16/opus47-sync-100x10-20260516232231.jsonl",
  "n": 100,
  "badPromptMentions": 0,
  "wrong": 0
}
{
  "file": "docs/superpowers/uat/sub2api-100x10-2026-05-16/opus47-stream-100x10-20260516232621.jsonl",
  "n": 100,
  "badPromptMentions": 0,
  "wrong": 0
}
```

No response contained the previously observed contamination patterns:
- `伪造`
- `system prompt`
- `SYSTEM PROMPT`
- `忽略它`
- `Kiro 的方式`

## sub2api Database Verification

The real sub2api Postgres schema stores request outcomes in `usage_logs` without a `status_code` column. Relevant columns include:

- `requested_model`
- `upstream_model`
- `stream`
- `duration_ms`
- `input_tokens`
- `output_tokens`
- `created_at`

Non-stream database window, `2026-05-16 23:22:31+08` to `2026-05-16 23:26:21+08`:

| stream | rows | min ms | avg ms | max ms | input tokens | output tokens | first_at | last_at |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | --- | --- |
| false | 101 | 1054 | 20816 | 52559 | 592280 | 1702 | 2026-05-16 23:22:33.457536+08 | 2026-05-16 23:26:15.005091+08 |

The extra one non-stream row in this broad time window is a nearby manual/API verification request. The load-test script summary and per-request JSONL are the exact source of truth for the requested 100 requests.

Stream database window, `2026-05-16 23:26:21+08` to `2026-05-16 23:26:55+08`:

| stream | rows | min ms | avg ms | max ms | input tokens | output tokens | first_at | last_at |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | --- | --- |
| true | 100 | 1010 | 2812 | 8636 | 592100 | 1800 | 2026-05-16 23:26:23.088889+08 | 2026-05-16 23:26:53.750558+08 |

## Kiro-Go Runtime Stats

Authenticated Kiro-Go admin request stats after the two requested runs:

```json
{
  "total": 208,
  "success": 208,
  "failed": 0,
  "byEndpoint": {
    "total": 208,
    "success": 208,
    "failed": 0,
    "averageDurationMs": 11579,
    "queueWaitMs": 1775739,
    "maxQueueWaitMs": 46551,
    "attempts": 264,
    "toolUseCount": 1
  },
  "opus": {
    "total": 202,
    "success": 202,
    "failed": 0,
    "averageDurationMs": 11870,
    "queueWaitMs": 1775739,
    "maxQueueWaitMs": 46551,
    "attempts": 260,
    "toolUseCount": 0
  }
}
```

The Opus counters include the requested 200 load-test calls plus nearby verification calls.

## Service Health

Docker state during verification:

| Container | Status | Port |
| --- | --- | --- |
| `kiro-go-kiro-go-1` | Up | `0.0.0.0:8080->8080/tcp` |
| `sub2api` | Up, healthy | `0.0.0.0:18080->8080/tcp` |
| `sub2api-postgres` | Up, healthy | `5432/tcp` |
| `sub2api-redis` | Up, healthy | `6379/tcp` |

Kiro-Go health endpoint returned:

```json
{"status":"ok","uptime":641,"version":"1.0.8"}
```

Authenticated sub2api model listing returned 31 models and included:

- `claude-opus-4-7`
- `claude-opus-4.7`
- `claude-opus-4.7-thinking`
- adjacent Claude Opus/Sonnet/Haiku variants

Unauthenticated `GET /v1/models` returned `401`, which confirms the gateway still enforces downstream API-key auth.

## Browser MCP Verification

Browser MCP successfully opened `http://127.0.0.1:8080/admin` and captured:

- Screenshot: `docs/superpowers/uat/playwright-2026-05-16/kiro-go-admin-opus47-100x10-20260516.png`
- Image metadata: PNG, `1440 x 1100`, 114470 bytes.
- Visible UI state: Kiro-Go title, `运行中`, version `v1.0.8`, account/request/success/failure dashboard cards.

Browser MCP navigation to `http://127.0.0.1:18080/`, `http://localhost:18080/`, and `http://127.0.0.1:18080/login` failed with browser-backend `net::ERR_BLOCKED_BY_CLIENT`. The same sub2api URL returned HTTP 200 via `curl`, and the returned HTML contained the SPA shell for `CGTall-AI - AI API Gateway`, so this is recorded as a Browser MCP environment block rather than a service failure.

## Official Documentation Cross-Check

The implementation and verification were checked against current official documentation:

- Anthropic Messages API streaming uses SSE events such as message start/content deltas/message stop; the stream parser validates `message_stop`.
- Anthropic prompt caching is reported in usage fields and requires preserving `cache_control` blocks where provided.
- Claude Code third-party provider guidance relies on compatible base URLs, auth tokens, and model discovery/fixed model configuration; `/v1/models` and Opus model aliases were therefore verified through sub2api.
- Kiro hooks can inject hook output into the agent context; this supports treating raw Kiro hook/system material as contextual text rather than synthesizing a fake system prompt header.

## Residual Risk

- Real upstream Opus 4.7 capacity pressure still occurred during load. Downstream correctness and success were preserved, but non-stream tail latency was high because retries/admission absorbed upstream `429`/capacity responses.
- The current production admission setting for `claude-opus-4.7` is concurrency 10 and queue 300, with adaptive pressure reducing concurrency under sustained pressure. This is appropriate for preserving success rate, but may trade latency for reliability during upstream scarcity.
- Browser MCP could screenshot Kiro-Go but was blocked on the sub2api port. sub2api frontend/API health was verified by direct HTTP and database/API evidence instead.

## Verdict

For `claude-opus-4-7`, the local `/www/sub2api` downstream route remained genuinely callable after the P0-P4 Kiro-Go changes:

- 100/100 non-stream requests succeeded with exact marker content.
- 100/100 stream requests succeeded with exact marker content.
- 0 prompt-pollution warning responses were observed.
- 0 downstream HTTP failures were observed.
- sub2api database usage rows were written for the real requests.
- Kiro-Go runtime stats reported all observed Opus requests as successful.
- Kiro-Go admin UI was verified by Browser MCP screenshot.
