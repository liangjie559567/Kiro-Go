# All Available Models 99% Load Test Report

Date: 2026-05-16  
Environment: Docker production stack, Kiro-Go `v1.0.8`, sub2api `/v1/messages` downstream path  
Run window: 2026-05-16 21:38:43+08 to 21:59:16+08  
Request path: Claude-compatible client -> sub2api `http://127.0.0.1:18080/v1/messages` -> Kiro-Go -> Kiro IDE upstream

## Verdict

PASS for the requested 99% availability/correctness gate across all currently available models and both modes.

- Precheck: 58/58 model-mode checks passed.
- Load test: 5800 total requests, 5798 correct, overall correctness 99.9655%.
- Per model/mode floor: 99.00%; no model-mode group fell below 99%.
- HTTP status: 5800/5800 client requests returned HTTP 200.
- Modes covered: non-streaming and streaming, 100 requests each per model/mode, global concurrency 10.
- Legacy model name `claude-opus-4-7` remains usable: 100/100 non-stream, 100/100 stream.

## Models Covered

29 model names were tested, each in non-streaming and streaming mode:

`auto`, `auto-thinking`, `claude-haiku-4.5`, `claude-haiku-4.5-thinking`, `claude-opus-4-7`, `claude-opus-4.5`, `claude-opus-4.5-thinking`, `claude-opus-4.6`, `claude-opus-4.6-thinking`, `claude-opus-4.7`, `claude-opus-4.7-thinking`, `claude-sonnet-4`, `claude-sonnet-4-thinking`, `claude-sonnet-4.5`, `claude-sonnet-4.5-thinking`, `claude-sonnet-4.6`, `claude-sonnet-4.6-thinking`, `deepseek-3.2`, `deepseek-3.2-thinking`, `glm-5`, `glm-5-thinking`, `gpt-4`, `gpt-4o`, `minimax-m2.1`, `minimax-m2.1-thinking`, `minimax-m2.5`, `minimax-m2.5-thinking`, `qwen3-coder-next`, `qwen3-coder-next-thinking`.

## Corrections Applied Before Load Test

sub2api account 24 (`kiro_claude_01`) was missing scheduler `model_mapping` keys for many Kiro-Go exposed models. This caused sub2api to reject requests before they reached Kiro-Go with `503 No available accounts`.

I added mappings for the missing exposed models, including thinking variants, `claude-opus-4.6`, `claude-sonnet-4`, `claude-sonnet-4.6`, `gpt-4`, `gpt-4o`, and other Kiro-Go model names. sub2api was restarted and health checked after the update.

## Load Summary

| Metric | Result |
| --- | ---: |
| Total requests | 5800 |
| Correct responses | 5798 |
| Incorrect responses | 2 |
| Overall correctness | 99.9655% |
| HTTP 2xx | 5800/5800 |
| Model-mode groups below 99% | 0 |
| Model-mode groups exactly 99% | 2 |
| Minimum model-mode correctness | 99.00% |

Two groups were exactly at the 99% threshold:

| Model | Mode | Correct | Failure |
| --- | --- | ---: | --- |
| `claude-opus-4.5-thinking` | stream | 99/100 | HTTP 200 but stream missing terminal/text structure |
| `qwen3-coder-next` | stream | 99/100 | HTTP 200, valid stream shape, content answered `474` instead of expected `374` |

## Latency Notes

Most Claude-family and GPT-family models had p95 latency in the 1-8.5s range. The longest tail latencies came from non-Claude routed models:

| Model | Mode | Avg ms | p95 ms | p99 ms |
| --- | --- | ---: | ---: | ---: |
| `qwen3-coder-next-thinking` | nonstream | 3936 | 6076 | 120823 |
| `auto` | nonstream | 2363 | 1974 | 34915 |
| `glm-5-thinking` | nonstream | 4804 | 21259 | 25787 |
| `glm-5-thinking` | stream | 5018 | 20366 | 22780 |
| `glm-5` | nonstream | 4187 | 18038 | 22674 |
| `glm-5` | stream | 4203 | 14681 | 20048 |
| `deepseek-3.2` | nonstream | 1887 | 9933 | 19633 |
| `minimax-m2.5` | nonstream | 2023 | 10641 | 12299 |

The 120s qwen long-tail requests completed successfully, but they are a real UX risk for Claude Code-style interactive use.

## Database Evidence

sub2api usage records for `/v1/messages` during the load window:

- Total usage rows: 5801
- Rows on account 24: 5799
- Stream rows: 2899
- First row: 2026-05-16 21:38:45.534954+08
- Last row: 2026-05-16 21:58:56.733418+08

The extra row versus 5800 test requests is from concurrent environment noise in the same time window. The usage distribution page also shows the all-model request distribution.

## Log Evidence

Client-observed result was 5800/5800 HTTP 200. Internal logs still revealed production risks:

- Kiro-Go saw 15 upstream Opus 4.7 capacity/rate-limit warnings (`429`) during the run.
- Kiro-Go saw 4 upstream Kiro IDE `HTTP 500` warnings near the end of the run.
- sub2api recorded 1 `gateway.forward_failed` for the failed stream terminal event.
- sub2api recorded 2400 `Calculate cost failed: pricing not found` entries.

The upstream 429/500 events did not surface to the client in this run, indicating retry/failover behavior absorbed them. They should still be monitored because they affect latency and may reduce correctness under heavier load.

## Pricing Gap

12 tested models have `total_cost = 0` in sub2api because `model_pricing.json` lacks pricing entries:

`auto`, `auto-thinking`, `deepseek-3.2`, `deepseek-3.2-thinking`, `glm-5`, `glm-5-thinking`, `minimax-m2.1`, `minimax-m2.1-thinking`, `minimax-m2.5`, `minimax-m2.5-thinking`, `qwen3-coder-next`, `qwen3-coder-next-thinking`.

This does not affect downstream call correctness, but it does affect production billing/cost reporting and should be fixed before relying on financial dashboards.

## Browser Evidence

Playwright-MCP screenshots saved in this directory:

- `kiro-go-admin-all-models-20260516.png`: Kiro-Go admin page, service running, version `v1.0.8`, request statistics visible.
- `sub2api-admin-dashboard-all-models-20260516.png`: sub2api admin dashboard page rendered through browser.
- `sub2api-usage-all-models-20260516.png`: sub2api Usage page showing model distribution and `/v1/messages` traffic.

## Artifacts

- Precheck JSONL: `precheck-20260516PRE4.jsonl`
- Precheck summary: `precheck-summary-20260516PRE4.json`
- Load JSONL: `load-20260516ALL100.jsonl`
- Load summary: `load-summary-20260516ALL100.json`
- Test runner: `all-models-loadtest.mjs`

## Follow-Up Items

1. Add pricing entries for the 12 zero-cost Kiro-Go models in sub2api's persisted `model_pricing.json` or database pricing source.
2. Investigate qwen/GLM/deepseek long-tail latency, especially 120s non-streaming completions.
3. Monitor upstream Kiro IDE 429/500 frequency under heavier concurrent Claude Code traffic.
4. Consider adding explicit request timeouts and retry classification in the load runner for future unattended soak tests.
