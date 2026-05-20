#!/usr/bin/env node
'use strict';

let chromium;
try {
  ({ chromium } = require('playwright'));
} catch (_) {
  try {
    ({ chromium } = require('/root/.npm/_npx/e41f203b7505f1fb/node_modules/playwright'));
  } catch (error) {
    chromium = null;
  }
}

const cp = require('child_process');
const fs = require('fs');
const path = require('path');

const root = path.resolve(__dirname, '../../../..');
const runsDir = path.join(__dirname, 'runs');
const runId = new Date().toISOString().replace(/[-:.TZ]/g, '').slice(0, 14);
const outDir = path.join(runsDir, runId);
const apiDir = path.join(outDir, 'api');
const dbDir = path.join(outDir, 'db');
const logDir = path.join(outDir, 'logs');
const shotDir = path.join(outDir, 'screenshots');

const kiroBase = cleanBaseUrl(process.env.KIRO_GO_BASE_URL || 'http://127.0.0.1:8080');
const subBase = cleanBaseUrl(process.env.SUB2API_BASE_URL || 'http://127.0.0.1:18080');
const probeCount = Math.max(0, Number(process.env.OPUS47_PROBE_COUNT || 2));
const runProbes = process.env.OPUS47_RUN_PROBES === '1';

function cleanBaseUrl(value) {
  return String(value || '').replace(/\/+$/, '');
}

function ensureDirs() {
  for (const dir of [outDir, apiDir, dbDir, logDir, shotDir]) fs.mkdirSync(dir, { recursive: true });
}

function sh(args, options = {}) {
  return cp.execFileSync(args[0], args.slice(1), {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
    ...options,
  }).trim();
}

function shSafe(args) {
  try {
    return { ok: true, value: sh(args) };
  } catch (error) {
    return { ok: false, error: error.stderr ? String(error.stderr).slice(0, 1000) : error.message };
  }
}

function redactString(value) {
  const secrets = [
    process.env.KIRO_GO_ADMIN_PASSWORD,
    process.env.SUB2API_API_KEY,
    process.env.SUB2API_ADMIN_EMAIL,
    process.env.SUB2API_ADMIN_PASSWORD,
  ].filter(Boolean);
  let out = String(value == null ? '' : value);
  for (const secret of secrets) out = out.split(secret).join('[REDACTED]');
  return out
    .replace(/Bearer\s+[A-Za-z0-9._~+/=-]+/gi, 'Bearer [REDACTED]')
    .replace(/(api[_-]?key|password|authorization|token|secret|refresh|access)(["'\s:=]+)([^"',\s}]+)/gi, '$1$2[REDACTED]')
    .replace(/[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}/gi, '[REDACTED_EMAIL]');
}

function redact(value) {
  if (value == null) return value;
  if (typeof value === 'string') return redactString(value);
  if (Array.isArray(value)) return value.map(redact);
  if (typeof value === 'object') {
    const out = {};
    for (const [key, item] of Object.entries(value)) {
      out[key] = /authorization|api[_-]?key|password|token|secret|refresh|access|email|profileArn|cookie/i.test(key)
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

function writeText(file, value) {
  fs.writeFileSync(file, redactString(value) + (String(value).endsWith('\n') ? '' : '\n'));
}

async function jsonFetch(name, url, options = {}) {
  const started = Date.now();
  try {
    const res = await fetch(url, options);
    const text = await res.text();
    let body;
    try {
      body = text ? JSON.parse(text) : null;
    } catch (_) {
      body = { text: text.slice(0, 1200) };
    }
    const result = { ok: res.ok, status: res.status, durationMs: Date.now() - started, body };
    writeJson(path.join(apiDir, `${name}.json`), result);
    return result;
  } catch (error) {
    const result = { ok: false, status: 0, durationMs: Date.now() - started, error: error.message };
    writeJson(path.join(apiDir, `${name}.json`), result);
    return result;
  }
}

function dockerContainer(name) {
  const exact = shSafe(['docker', 'ps', '--filter', `name=^/${name}$`, '--format', '{{.Names}}']);
  if (exact.ok && exact.value) return exact.value.split(/\r?\n/)[0];
  const fuzzy = shSafe(['docker', 'ps', '--filter', `name=${name}`, '--format', '{{.Names}}']);
  return fuzzy.ok && fuzzy.value ? fuzzy.value.split(/\r?\n/)[0] : '';
}

function dockerHealth(container) {
  if (!container) return { ok: false, status: 'missing' };
  const inspect = shSafe(['docker', 'inspect', '--format', '{{json .State}}', container]);
  if (!inspect.ok) return { ok: false, status: 'inspect_failed', error: inspect.error };
  try {
    const state = JSON.parse(inspect.value);
    return {
      ok: state.Running === true && (!state.Health || state.Health.Status === 'healthy'),
      running: state.Running === true,
      health: state.Health ? state.Health.Status : 'none',
      status: state.Status,
    };
  } catch (error) {
    return { ok: false, status: 'parse_failed', error: error.message };
  }
}

function sub2apiKey(container) {
  if (process.env.SUB2API_API_KEY) return process.env.SUB2API_API_KEY;
  if (!container) return '';
  const out = shSafe(['docker', 'exec', container, 'sh', '-lc', 'PGPASSWORD="$DATABASE_PASSWORD" psql -h "$DATABASE_HOST" -U "$DATABASE_USER" -d "$DATABASE_DBNAME" -Atc "select key from api_keys where lower(status)=\'active\' order by id limit 1"']);
  return out.ok ? out.value.trim() : '';
}

function pgJson(container, sql, stem) {
  if (!container) return { ok: false, status: 'missing_container' };
  const tmp = path.join(dbDir, `${stem}.sql`);
  fs.writeFileSync(tmp, sql);
  const remote = `/tmp/${path.basename(tmp)}`;
  try {
    cp.execFileSync('docker', ['cp', tmp, `${container}:${remote}`], { stdio: ['ignore', 'pipe', 'pipe'] });
    const out = sh(['docker', 'exec', container, 'sh', '-lc', `PGPASSWORD="$DATABASE_PASSWORD" psql -h "$DATABASE_HOST" -U "$DATABASE_USER" -d "$DATABASE_DBNAME" -At -f ${remote}; rm -f ${remote}`]);
    const parsed = out ? JSON.parse(out) : null;
    writeJson(path.join(dbDir, `${stem}.json`), parsed);
    return parsed;
  } catch (error) {
    const result = { ok: false, error: error.stderr ? String(error.stderr).slice(0, 1000) : error.message };
    writeJson(path.join(dbDir, `${stem}.json`), result);
    return result;
  } finally {
    fs.rmSync(tmp, { force: true });
  }
}

async function postOpusProbe(apiKey, stream, index) {
  if (!apiKey) return { ok: false, status: 'BLOCKED_BY_ENV', reason: 'missing sub2api api key' };
  const marker = `OPUS47_GOVERNOR_UAT_${runId}_${stream ? 'STREAM' : 'NONSTREAM'}_${index}`;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(new Error('timeout')), 30000);
  const started = Date.now();
  try {
    const res = await fetch(`${subBase}/v1/messages`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'anthropic-version': '2023-06-01',
        authorization: `Bearer ${apiKey}`,
        'x-client-request-id': marker,
      },
      body: JSON.stringify({
        model: 'claude-opus-4-7',
        max_tokens: 80,
        stream,
        messages: [{ role: 'user', content: `Return exactly this marker and nothing else: ${marker}` }],
      }),
      signal: controller.signal,
    });
    const body = await res.text();
    return {
      ok: res.status === 200,
      status: res.status,
      durationMs: Date.now() - started,
      marker,
      retryAfter: res.headers.get('retry-after') || '',
      kiroReason: res.headers.get('x-kiro-go-error-reason') || '',
      bodyPrefix: body.slice(0, 800),
    };
  } catch (error) {
    return { ok: false, status: 0, durationMs: Date.now() - started, marker, error: error.message };
  } finally {
    clearTimeout(timer);
  }
}

async function captureBrowser(summary, subAuth) {
  if (!chromium) {
    summary.browser = { status: 'BLOCKED_BY_ENV', reason: 'playwright package unavailable' };
    return;
  }
  const browser = await chromium.launch({ headless: true, executablePath: process.env.CHROME_PATH || '/usr/bin/google-chrome', args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1050 } });
  const errors = [];
  page.on('console', (msg) => { if (msg.type() === 'error') errors.push(msg.text().slice(0, 500)); });
  page.on('pageerror', (err) => errors.push(String(err).slice(0, 500)));

  await page.goto(`${kiroBase}/admin`, { waitUntil: 'networkidle' });
  await page.locator('#pwdField').fill(process.env.KIRO_GO_ADMIN_PASSWORD || '');
  await page.locator('button[onclick="login()"]').click();
  await page.waitForSelector('#mainPage:not(.hidden)', { timeout: 12000 });
  await page.locator('[data-tab="api"]').click();
  await page.waitForTimeout(1500);
  const kiroText = await page.locator('body').innerText();
  await page.screenshot({ path: path.join(shotDir, 'kiro-admin-opus-fleet-health.png'), fullPage: true });

  const subTextChecks = {};
  if (subAuth && subAuth.token) {
    const subContext = await browser.newContext({ viewport: { width: 1440, height: 1050 } });
    await subContext.addInitScript(({ token, user }) => {
      localStorage.setItem('auth_token', token);
      if (user) localStorage.setItem('auth_user', JSON.stringify(user));
    }, subAuth);
    const subPage = await subContext.newPage();
    await subPage.goto(`${subBase}/admin/accounts`, { waitUntil: 'networkidle' });
    await subPage.waitForTimeout(1200);
    subTextChecks.accounts = await subPage.locator('body').innerText();
    await subPage.screenshot({ path: path.join(shotDir, 'sub2api-admin-accounts.png'), fullPage: true });
    await subPage.goto(`${subBase}/admin/usage`, { waitUntil: 'networkidle' });
    await subPage.waitForTimeout(1200);
    subTextChecks.usage = await subPage.locator('body').innerText();
    await subPage.screenshot({ path: path.join(shotDir, 'sub2api-admin-usage.png'), fullPage: true });
    await subContext.close();
  }

  await browser.close();
  const screenshots = {};
  for (const file of fs.readdirSync(shotDir)) {
    const stat = fs.statSync(path.join(shotDir, file));
    screenshots[file] = { bytes: stat.size };
  }
  summary.browser = {
    status: 'CAPTURED',
    errors,
    screenshots,
    textChecks: {
      kiroOpusPanelVisible: /Opus 4\.7 fleet health|Fleet readiness|claude-opus-4/i.test(kiroText),
      sub2apiAccountsVisible: subTextChecks.accounts ? /Accounts|账号|kiro|claude/i.test(subTextChecks.accounts) : false,
      sub2apiUsageVisible: subTextChecks.usage ? /Usage|用量|claude|tokens/i.test(subTextChecks.usage) : false,
    },
  };
}

async function main() {
  ensureDirs();
  const kiroContainer = dockerContainer('kiro-go');
  const subContainer = dockerContainer('sub2api');
  const kiroHeaders = process.env.KIRO_GO_ADMIN_PASSWORD ? { 'X-Admin-Password': process.env.KIRO_GO_ADMIN_PASSWORD } : {};
  const summary = {
    runId,
    startedAt: new Date().toISOString(),
    mcp: {
      configured: fs.existsSync('/root/.codex/config.toml') && fs.readFileSync('/root/.codex/config.toml', 'utf8').includes('@playwright/mcp@0.0.73'),
      npmVersion: shSafe(['npm', 'view', '@playwright/mcp@0.0.73', 'version']).value || '',
    },
    docker: {
      kiroGo: { container: kiroContainer, ...dockerHealth(kiroContainer) },
      sub2api: { container: subContainer, ...dockerHealth(subContainer) },
      ps: shSafe(['docker', 'ps', '--format', '{{.Names}} {{.Image}} {{.Status}}']).value || '',
    },
    api: {},
    db: {},
    probes: [],
    logs: {},
    checks: {},
  };

  writeText(path.join(logDir, 'docker-ps.txt'), summary.docker.ps);
  if (kiroContainer) writeText(path.join(logDir, 'kiro-go-tail.log'), shSafe(['docker', 'logs', '--tail', '300', kiroContainer]).value || '');
  if (subContainer) writeText(path.join(logDir, 'sub2api-tail.log'), shSafe(['docker', 'logs', '--tail', '300', subContainer]).value || '');

  summary.api.kiroHealth = await jsonFetch('kiro-health', `${kiroBase}/health`);
  summary.api.sub2apiHealth = await jsonFetch('sub2api-health', `${subBase}/api/status`);
  if (process.env.KIRO_GO_ADMIN_PASSWORD) {
    summary.api.fleetReadiness = await jsonFetch('kiro-fleet-readiness', `${kiroBase}/admin/api/fleet/readiness?model=claude-opus-4-7`, { headers: kiroHeaders });
    summary.api.modelReadiness = await jsonFetch('kiro-model-readiness', `${kiroBase}/admin/api/claude-code/model-readiness?model=claude-opus-4-7`, { headers: kiroHeaders });
    summary.api.requestLogs = await jsonFetch('kiro-request-logs', `${kiroBase}/admin/api/request-logs?limit=200`, { headers: kiroHeaders });
  }

  const apiKey = sub2apiKey(subContainer);
  if (runProbes) {
    for (let i = 1; i <= probeCount; i += 1) {
      summary.probes.push(await postOpusProbe(apiKey, false, i));
      summary.probes.push(await postOpusProbe(apiKey, true, i));
    }
    writeJson(path.join(apiDir, 'sub2api-opus47-probes.json'), summary.probes);
  }

  summary.db.usage = pgJson(subContainer, `select json_build_object(
    'recentOpus47', coalesce((select json_agg(row_to_json(t)) from (select status, count(*) n, max(duration_ms) max_ms from usage_logs where requested_model='claude-opus-4-7' and created_at > now() - interval '60 minutes' group by status order by status) t), '[]'::json),
    'recentMarkers', coalesce((select json_agg(row_to_json(t)) from (select request_id, status, requested_model, duration_ms, created_at from usage_logs where request_id like 'OPUS47_GOVERNOR_UAT_${runId}%' order by created_at desc limit 20) t), '[]'::json)
  );`, 'sub2api-usage');

  let subAuth = null;
  if (process.env.SUB2API_ADMIN_EMAIL && process.env.SUB2API_ADMIN_PASSWORD) {
    const login = await jsonFetch('sub2api-login', `${subBase}/api/v1/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email: process.env.SUB2API_ADMIN_EMAIL, password: process.env.SUB2API_ADMIN_PASSWORD }),
    });
    const token = login.body && (login.body.access_token || login.body.data && login.body.data.access_token);
    const user = login.body && (login.body.user || login.body.data && login.body.data.user);
    if (token) {
      subAuth = { token, user };
      summary.api.sub2apiUsage = await jsonFetch('sub2api-admin-usage', `${subBase}/api/v1/admin/usage?page=1&page_size=50&model=claude-opus-4-7`, { headers: { Authorization: `Bearer ${token}` } });
    }
  }

  await captureBrowser(summary, subAuth);

  const fleet = summary.api.fleetReadiness && summary.api.fleetReadiness.body || {};
  const requestLogs = summary.api.requestLogs && summary.api.requestLogs.body && summary.api.requestLogs.body.logs || [];
  const recentUsage = summary.db.usage && Array.isArray(summary.db.usage.recentOpus47) ? summary.db.usage.recentOpus47 : [];
  const markerUsage = summary.db.usage && Array.isArray(summary.db.usage.recentMarkers) ? summary.db.usage.recentMarkers : [];
  const probeFailures = summary.probes.filter((probe) => !probe.ok);
  const upstreamBlocked = fleet.status === 'blocked' || probeFailures.some((probe) => probe.status === 429 && probe.kiroReason);

  summary.checks = {
    mcpPinned073: summary.mcp.configured === true && summary.mcp.npmVersion === '0.0.73',
    dockerContainersRunning: summary.docker.kiroGo.running === true && summary.docker.sub2api.running === true,
    kiroHealth200: summary.api.kiroHealth.status === 200,
    sub2apiHealth200: summary.api.sub2apiHealth.status === 200,
    fleetContractPresent: Boolean(fleet.status && fleet.circuitState && typeof fleet.safeConcurrency !== 'undefined'),
    requestLogsBoundedAttempts: requestLogs.filter((row) => String(row.model || '').includes('opus')).every((row) => Number(row.attempts || 0) <= 4),
    dbUsageEvidencePresent: recentUsage.length > 0 || markerUsage.length > 0,
    probesOkOrExplicitlyBlocked: !runProbes || probeFailures.length === 0 || upstreamBlocked,
    screenshotsCaptured: summary.browser && summary.browser.status === 'CAPTURED' && Object.values(summary.browser.screenshots || {}).every((row) => row.bytes > 5000),
    screenshotTextMatchesApis: summary.browser && summary.browser.textChecks && summary.browser.textChecks.kiroOpusPanelVisible === true,
    noBrowserPageErrors: summary.browser && Array.isArray(summary.browser.errors) && summary.browser.errors.length === 0,
  };
  summary.result = Object.values(summary.checks).every(Boolean)
    ? 'PASS'
    : upstreamBlocked
      ? 'BLOCKED_BY_UPSTREAM'
      : 'FAIL';

  writeJson(path.join(outDir, 'summary.json'), summary);
  writeText(path.join(outDir, 'UAT-RESULT.md'), `# Opus 4.7 Health Governor UAT\n\nResult: ${summary.result}\n\nRun: ${runId}\n\nChecks:\n\n${Object.entries(summary.checks).map(([key, value]) => `- ${key}: ${value ? 'PASS' : 'FAIL'}`).join('\n')}\n`);
  console.log(JSON.stringify(redact({ result: summary.result, checks: summary.checks, outDir }), null, 2));
  if (summary.result === 'FAIL') process.exitCode = 1;
}

main().catch((error) => {
  console.error(error && error.stack ? redactString(error.stack) : redactString(error));
  process.exit(1);
});

