#!/usr/bin/env node
'use strict';

const cp = require('child_process');
const fs = require('fs');
const path = require('path');

const outDir = path.resolve(__dirname);
const apiDir = path.join(outDir, 'api');
const dbDir = path.join(outDir, 'db');
const logDir = path.join(outDir, 'logs');
const intervalMs = Number(process.env.MONITOR_INTERVAL_MS || 60000);
const durationMs = Number(process.env.MONITOR_DURATION_MS || 600000);
const adminPassword = process.env.KIRO_GO_ADMIN_PASSWORD || '';
const kiroBase = process.env.KIRO_GO_BASE_URL || 'http://127.0.0.1:8080';
const subBase = process.env.SUB2API_BASE_URL || 'http://127.0.0.1:18080';

for (const dir of [apiDir, dbDir, logDir]) fs.mkdirSync(dir, { recursive: true });

function sh(cmd) {
  return cp.execSync(cmd, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
}

function redact(value) {
  if (value == null) return value;
  if (typeof value === 'string') {
    return value
      .replace(/Bearer\s+[A-Za-z0-9._~+/=-]+/gi, 'Bearer [REDACTED]')
      .replace(/[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+/g, '[EMAIL_REDACTED]')
      .replace(/(authorization|credentials|api[_-]?key|password|secret|refresh[_-]?token|access[_-]?token)(["'\s:=]+)([^"',\s}]+)/gi, '$1$2[REDACTED]');
  }
  if (Array.isArray(value)) return value.map(redact);
  if (typeof value === 'object') {
    const out = {};
    for (const [key, item] of Object.entries(value)) {
      out[key] = /authorization|credentials|api[_-]?key|password|secret|refresh[_-]?token|access[_-]?token|^key$|email|userId|machineId|profileArn|riskGroupKey|anthropicRequestId/i.test(key)
        ? '[REDACTED]'
        : redact(item);
    }
    return out;
  }
  return value;
}

function writeJson(file, value) {
  fs.writeFileSync(file, JSON.stringify(redact(value), null, 2) + '\n');
}

async function jsonFetch(url, options = {}) {
  const started = Date.now();
  try {
    const res = await fetch(url, options);
    const text = await res.text();
    let body = null;
    try {
      body = text ? JSON.parse(text) : null;
    } catch (_) {
      body = { text: text.slice(0, 1000) };
    }
    return { ok: res.ok, status: res.status, durationMs: Date.now() - started, body };
  } catch (error) {
    return { ok: false, status: 0, durationMs: Date.now() - started, error: error.message || String(error) };
  }
}

function pgJson(sql) {
  const sqlFile = path.join(dbDir, `query-${Date.now()}.sql`);
  fs.writeFileSync(sqlFile, sql);
  const containerFile = `/tmp/${path.basename(sqlFile)}`;
  cp.execFileSync('docker', ['cp', sqlFile, `sub2api:${containerFile}`], { stdio: ['ignore', 'pipe', 'pipe'] });
  const out = sh(`docker exec sub2api sh -lc 'PGPASSWORD="$DATABASE_PASSWORD" psql -h "$DATABASE_HOST" -U "$DATABASE_USER" -d "$DATABASE_DBNAME" -At -f ${containerFile}; rm -f ${containerFile}'`);
  fs.unlinkSync(sqlFile);
  return out ? JSON.parse(out) : null;
}

function logTail(container, sinceSeconds) {
  const tailLines = Math.max(200, Math.min(1000, sinceSeconds * 2));
  const raw = sh(`docker logs --since ${sinceSeconds}s --tail ${tailLines} ${container} 2>&1 || true`);
  const redacted = redact(raw);
  return {
    lines: redacted.split(/\n/).filter(Boolean).slice(-200),
    counts: {
      error: (redacted.match(/\berror\b/gi) || []).length,
      warn: (redacted.match(/\bwarn\b/gi) || []).length,
      panic: (redacted.match(/\bpanic\b|\bfatal\b/gi) || []).length,
      rate429: (redacted.match(/\b429\b/g) || []).length,
      temporaryLimit: (redacted.match(/temporary-limit|temporary limits/gi) || []).length,
      noAvailable: (redacted.match(/no available|no account available/gi) || []).length,
      status503: (redacted.match(/\b503\b/g) || []).length,
    },
  };
}

function summarizeRequestLogs(logs) {
  const byStatus = {};
  const byModel = {};
  const byOutcome = {};
  let attemptTrace = 0;
  let errorEntries = 0;
  let maxDurationMs = 0;
  for (const row of logs) {
    byStatus[row.statusCode] = (byStatus[row.statusCode] || 0) + 1;
    byModel[row.model || ''] = (byModel[row.model || ''] || 0) + 1;
    byOutcome[row.outcome || ''] = (byOutcome[row.outcome || ''] || 0) + 1;
    if (Array.isArray(row.attemptTrace) && row.attemptTrace.length) attemptTrace += 1;
    if (Number(row.statusCode || 0) >= 400 || String(row.outcome || '').includes('error')) errorEntries += 1;
    maxDurationMs = Math.max(maxDurationMs, Number(row.durationMs || 0));
  }
  return { total: logs.length, byStatus, byModel, byOutcome, attemptTrace, errorEntries, maxDurationMs };
}

async function sample(index, sinceSeconds) {
  const adminHeaders = adminPassword ? { 'X-Admin-Password': adminPassword } : {};
  const now = new Date();
  const health = await jsonFetch(`${kiroBase}/health`);
  const subHealth = await jsonFetch(`${subBase}/health`);
  const status = await jsonFetch(`${kiroBase}/admin/api/status`, { headers: adminHeaders });
  const readiness = await jsonFetch(`${kiroBase}/admin/api/claude-code/readiness`, { headers: adminHeaders });
  const modelReadiness = await jsonFetch(`${kiroBase}/admin/api/claude-code/model-readiness?model=claude-opus-4-7`, { headers: adminHeaders });
  const fleetReadiness = await jsonFetch(`${kiroBase}/admin/api/fleet/readiness?model=claude-opus-4-7`, { headers: adminHeaders });
  const requestLogs = await jsonFetch(`${kiroBase}/admin/api/request-logs?limit=300`, { headers: adminHeaders });
  const requestStats = await jsonFetch(`${kiroBase}/admin/api/request-stats`, { headers: adminHeaders });
  const logs = requestLogs.body && requestLogs.body.logs || [];
  const db = pgJson(`select json_build_object(
    'now', now(),
    'recentUsage', coalesce((select json_agg(row_to_json(t)) from (select requested_model, stream, count(*) n, min(created_at) min_created_at, max(created_at) max_created_at, max(duration_ms) max_ms, percentile_disc(0.95) within group (order by duration_ms) p95_ms, count(distinct account_id) accounts from usage_logs where created_at > now() - interval '15 minutes' group by requested_model, stream order by n desc limit 20) t), '[]'::json),
    'accounts', coalesce((select json_agg(row_to_json(t)) from (select status, schedulable, count(*) n from accounts where deleted_at is null group by status, schedulable order by status, schedulable) t), '[]'::json),
    'tempUnschedulable', coalesce((select json_agg(row_to_json(t)) from (select id, name, status, schedulable, temp_unschedulable_until, temp_unschedulable_reason from accounts where deleted_at is null and temp_unschedulable_until is not null and temp_unschedulable_until > now() order by temp_unschedulable_until desc limit 20) t), '[]'::json)
  );`);
  const kiroLogs = logTail('kiro-go-kiro-go-1', sinceSeconds);
  const sub2apiLogs = logTail('sub2api', sinceSeconds);
  const row = {
    index,
    sampledAt: now.toISOString(),
    health,
    subHealth,
    status: { ok: status.ok, status: status.status, durationMs: status.durationMs },
    readiness: { ok: readiness.ok, status: readiness.status, durationMs: readiness.durationMs, body: readiness.body },
    modelReadiness: { ok: modelReadiness.ok, status: modelReadiness.status, durationMs: modelReadiness.durationMs, body: modelReadiness.body },
    fleetReadiness: { ok: fleetReadiness.ok, status: fleetReadiness.status, durationMs: fleetReadiness.durationMs, body: fleetReadiness.body },
    requestStats: { ok: requestStats.ok, status: requestStats.status, durationMs: requestStats.durationMs, body: requestStats.body },
    requestLogs: summarizeRequestLogs(logs),
    db,
    dockerLogs: { kiroGo: kiroLogs.counts, sub2api: sub2apiLogs.counts },
  };
  writeJson(path.join(apiDir, `sample-${String(index).padStart(3, '0')}.json`), row);
  fs.writeFileSync(path.join(logDir, `kiro-go-tail-${String(index).padStart(3, '0')}.log`), kiroLogs.lines.join('\n') + '\n');
  fs.writeFileSync(path.join(logDir, `sub2api-tail-${String(index).padStart(3, '0')}.log`), sub2apiLogs.lines.join('\n') + '\n');
  console.log(JSON.stringify({
    index,
    health: health.status,
    subHealth: subHealth.status,
    requestLogErrors: row.requestLogs.errorEntries,
    requestLogMaxMs: row.requestLogs.maxDurationMs,
    safeConcurrency: row.fleetReadiness.body && row.fleetReadiness.body.safeConcurrency,
    locallySchedulable: row.fleetReadiness.body && row.fleetReadiness.body.locallySchedulableAccounts,
    generationBlocked: row.fleetReadiness.body && row.fleetReadiness.body.generationBlocked,
    kiroWarn: row.dockerLogs.kiroGo.warn,
    kiro429: row.dockerLogs.kiroGo.rate429,
    kiro503: row.dockerLogs.kiroGo.status503,
    tempUnschedulable: (db.tempUnschedulable || []).length,
  }));
  return row;
}

async function main() {
  if (!adminPassword) {
    throw new Error('KIRO_GO_ADMIN_PASSWORD is required for admin request-log monitoring.');
  }
  const started = Date.now();
  const startedAt = new Date().toISOString();
  const samples = [];
  let index = 1;
  while (Date.now() - started <= durationMs) {
    samples.push(await sample(index, Math.max(60, Math.ceil((Date.now() - started) / 1000) + 60)));
    index += 1;
    if (Date.now() - started > durationMs) break;
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }
  const hasRequestStatsTraffic = (row) => {
    const body = row.requestStats && row.requestStats.body;
    const byEndpoint = body && body.byEndpoint && body.byEndpoint['/v1/messages'];
    return Boolean(body && body.total > 0 && byEndpoint && byEndpoint.success > 0);
  };
  const aggregate = {
    startedAt,
    completedAt: new Date().toISOString(),
    intervalMs,
    durationMs,
    sampleCount: samples.length,
    pass: samples.every((row) => (
      row.health.status === 200
      && row.subHealth.status === 200
      && row.status.status === 200
      && row.readiness.status === 200
      && row.modelReadiness.status === 200
      && row.fleetReadiness.status === 200
      && row.requestStats.status === 200
      && hasRequestStatsTraffic(row)
      && row.dockerLogs.kiroGo.panic === 0
    )),
    minSafeConcurrency: Math.min(...samples.map((row) => Number(row.fleetReadiness.body && row.fleetReadiness.body.safeConcurrency || 0))),
    minLocallySchedulable: Math.min(...samples.map((row) => Number(row.fleetReadiness.body && row.fleetReadiness.body.locallySchedulableAccounts || 0))),
    maxGenerationBlocked: Math.max(...samples.map((row) => Number(row.fleetReadiness.body && row.fleetReadiness.body.generationBlocked || 0))),
    maxRequestLogErrors: Math.max(...samples.map((row) => row.requestLogs.errorEntries)),
    maxKiro429LogMentions: Math.max(...samples.map((row) => row.dockerLogs.kiroGo.rate429)),
    maxKiro503LogMentions: Math.max(...samples.map((row) => row.dockerLogs.kiroGo.status503)),
    maxTempUnschedulable: Math.max(...samples.map((row) => (row.db.tempUnschedulable || []).length)),
    last: samples[samples.length - 1],
  };
  aggregate.pass = aggregate.pass && aggregate.maxKiro429LogMentions === 0 && aggregate.maxKiro503LogMentions === 0;
  writeJson(path.join(outDir, 'monitor-summary.json'), aggregate);
}

main().catch((error) => {
  console.error(error && error.stack ? error.stack : error);
  process.exit(1);
});
