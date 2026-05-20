# Opus 4.7 Health Governor UAT

This harness collects real Docker, API, database, log, and Playwright evidence for the Opus 4.7 health governor. It is read-only except for optional low-volume Opus probe requests and UAT output files under `runs/`.

## Safety Rules

- Do not run `docker compose down -v` or delete Docker volumes.
- Do not print `data/config.json`, recovery snapshots, tokens, passwords, cookies, or account emails.
- Treat `PASS` as valid only when screenshots, APIs, logs, and sub2api database/usage evidence agree.
- If upstream Opus 4.7 is unavailable, record `BLOCKED_BY_UPSTREAM` rather than fabricating success.

## Environment

Required:

- `KIRO_GO_BASE_URL`, default `http://127.0.0.1:8080`
- `KIRO_GO_ADMIN_PASSWORD`
- `SUB2API_BASE_URL`, default `http://127.0.0.1:18080`

Optional:

- `SUB2API_API_KEY`; otherwise the script reads one active key from the sub2api container database.
- `SUB2API_ADMIN_EMAIL` and `SUB2API_ADMIN_PASSWORD` for admin UI screenshots/API login.
- `OPUS47_RUN_PROBES=1` to send bounded non-stream and stream probes through sub2api.
- `OPUS47_PROBE_COUNT`, default `2`.

## Run

```bash
KIRO_GO_ADMIN_PASSWORD=... \
SUB2API_ADMIN_EMAIL=... \
SUB2API_ADMIN_PASSWORD=... \
OPUS47_RUN_PROBES=1 \
node docs/superpowers/uat/opus47-health-governor/run-opus47-governor-uat.js
```

Outputs are written to `docs/superpowers/uat/opus47-health-governor/runs/<timestamp>/`.

