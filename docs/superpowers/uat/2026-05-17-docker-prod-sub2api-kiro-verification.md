# 2026-05-17 Docker Production Kiro-Go / sub2api Verification

Time: 2026-05-17 03:41 CST

## Verdict

PASS for Docker runtime health, direct Kiro-Go API, sub2api -> Kiro-Go API, database usage logging, and browser-visible admin evidence.

The local sub2api downstream remains truly callable through the Docker network route `http://kiro-go:8080`.

## Runtime

| Check | Result | Evidence |
| --- | --- | --- |
| Kiro-Go container | PASS | `kiro-go-kiro-go-1` running, `0.0.0.0:8080->8080/tcp` |
| sub2api container | PASS | `sub2api` running, `0.0.0.0:18080->8080/tcp` |
| sub2api Postgres/Redis | PASS | `sub2api-postgres` and `sub2api-redis` running |
| Kiro-Go health | PASS | `GET http://127.0.0.1:8080/health` -> HTTP 200, version `1.0.8` |
| sub2api health | PASS | `GET http://127.0.0.1:18080/health` -> HTTP 200 |

## API Smoke

Fresh smoke artifact:

`docs/superpowers/uat/sub2api-smoke/docker-prod-fresh-20260517033700.json`

| Check | Result |
| --- | --- |
| `GET /v1/models` through sub2api | PASS, HTTP 200, 31 models |
| `POST /v1/messages/count_tokens` through sub2api | PASS, positive `input_tokens=27` |
| `POST /v1/messages` sync through sub2api | PASS, exact marker returned, duration 2170 ms |
| `POST /v1/messages` stream through sub2api | PASS, exact marker returned, duration 1725 ms |
| Stream event shape | PASS, `message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop` |

Older full artifact with direct Kiro-Go and sub2api comparison:

`docs/superpowers/uat/sub2api-smoke/docker-prod-20260517032828/summary.json`

This artifact also passed direct Kiro-Go `/v1/models`, direct `/v1/messages`, sub2api sync, and sub2api stream.

## Database Evidence

Account configuration in sub2api Postgres:

| id | name | platform | type | status | schedulable | concurrency | base URL |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 24 | `kiro_claude_01` | `anthropic` | `apikey` | `active` | `true` | 12 | `http://kiro-go:8080` |

Fresh usage rows written by the latest API smoke:

| id | model | stream | input | output | inbound | upstream | account |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 43281 | `claude-sonnet-4.5` | false | 4134 | 15 | `/v1/messages` | `/v1/messages` | 24 |
| 43282 | `claude-sonnet-4.5` | true | 4134 | 15 | `/v1/messages` | `/v1/messages` | 24 |

## Browser Evidence

Real Chrome / Playwright screenshots:

| Page | Result | Screenshot |
| --- | --- | --- |
| Kiro-Go admin dashboard | PASS | `docs/superpowers/uat/playwright-2026-05-17/docker-prod-20260517033226/kiro-admin-dashboard.png` |
| Kiro-Go admin request/log view | PASS | `docs/superpowers/uat/playwright-2026-05-17/docker-prod-20260517033226/kiro-admin-logs.png` |
| sub2api filtered account page | PASS | `docs/superpowers/uat/playwright-2026-05-17/sub2api-filtered-20260517033326/sub2api-accounts-filtered-kiro.png` |
| sub2api usage page with selected account | PASS | `docs/superpowers/uat/playwright-2026-05-17/sub2api-usage-selected-20260517033446/sub2api-usage-account-selected.png` |

Screenshot analysis:

- The filtered account page shows exactly `kiro_claude_01`, platform `Anthropic`, type `Key`, status `Active`, group `claude`, and schedulable capacity `0 / 12`.
- The usage page shows account filter `kiro_claude_01`, API key `claude`, account `kiro_claude_01`, models including `claude-sonnet-4.5` and `claude-opus-4-7`.
- The usage table shows both `Stream` and `Sync` rows.
- The usage table shows `Inbound:/v1/messages` and `Upstream:/v1/messages`.

## Notes

- An earlier broad browser summary had `pass=false` because the unfiltered sub2api accounts page landed on OpenAI rows first; the later filtered account screenshot is the valid account proof.
- The selected usage screenshot's script boolean was false because it expected the selected input text to include `#24`; the UI displays `kiro_claude_01` without `#24`, while the table and DB rows prove the account selection and flow.
- A later attempt to rerun browser validation without password by injecting the saved session token redirected to `/login`; diagnostic output showed the token was expired. This does not affect the validated API, DB, or existing real-browser screenshot evidence.

