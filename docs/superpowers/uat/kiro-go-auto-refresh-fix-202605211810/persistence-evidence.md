# Persistence / DB Evidence

- `docker-compose.yml` defines only the `kiro-go` service.
- There is no Postgres/MySQL/Redis/SQLite service in this Docker environment.
- Runtime persistence is mounted as `./data:/app/data` with `CONFIG_PATH=/app/data/config.json`.
- Per instruction, `data/config.json` and other secret-bearing files were not read.
- Auto-refresh runtime status is exposed through `/admin/api/auto-refresh`; the verified post-run status is stored in `api/auto-refresh-after.json`.
