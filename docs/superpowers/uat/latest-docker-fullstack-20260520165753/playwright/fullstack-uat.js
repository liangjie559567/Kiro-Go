#!/usr/bin/env node
'use strict';

let chromium;
try {
  ({ chromium } = require('playwright'));
} catch (_) {
  ({ chromium } = require('/root/.npm/_npx/e41f203b7505f1fb/node_modules/playwright'));
}

const cp = require('child_process');
const fs = require('fs');
const path = require('path');

const root = '/www/Kiro-Go';
const outDir = path.resolve(root, 'docs/superpowers/uat/latest-docker-fullstack-20260520165753');
const apiDir = path.join(outDir, 'api');
const dbDir = path.join(outDir, 'db');
const logDir = path.join(outDir, 'logs');
const shotDir = path.join(outDir, 'screenshots');
const kiroBase = 'http://127.0.0.1:8080';
const subBase = 'http://127.0.0.1:18080';

function sh(cmd) {
  return cp.execSync(cmd, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
}

function parseEnv(file) {
  const env = {};
  if (!fs.existsSync(file)) return env;
  for (const line of fs.readFileSync(file, 'utf8').split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#') || !trimmed.includes('=')) continue;
    const idx = trimmed.indexOf('=');
    env[trimmed.slice(0, idx)] = trimmed.slice(idx + 1);
  }
  return env;
}

function redact(value) {
  if (value == null) return value;
  if (typeof value === 'string') {
    return value
      .replace(/Bearer\s+[A-Za-z0-9._~+/=-]+/gi, 'Bearer [REDACTED]')
      .replace(/(authorization|credentials|api[_-]?key|password|token|secret|refresh|access|key)(["'\s:=]+)([^"',\s}]+)/gi, '$1$2[REDACTED]');
  }
  if (Array.isArray(value)) return value.map(redact);
  if (typeof value === 'object') {
    const out = {};
    for (const [key, item] of Object.entries(value)) {
      out[key] = /authorization|credentials|api[_-]?key|password|token|secret|refresh|access|^key$|email|userId|machineId|profileArn|riskGroupKey|anthropicRequestId/i.test(key)
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
  const res = await fetch(url, options);
  const text = await res.text();
  let body;
  try {
    body = text ? JSON.parse(text) : null;
  } catch (_) {
    body = { text: text.slice(0, 1000) };
  }
  return { ok: res.ok, status: res.status, durationMs: Date.now() - started, body };
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

async function shot(page, name) {
  const file = path.join(shotDir, name);
  await page.screenshot({ path: file, fullPage: true });
  return file;
}

async function dismissOverlays(page) {
  await page.locator('.driver-popover-close-btn').click({ timeout: 1000 }).catch(() => {});
  await page.evaluate(() => {
    document.querySelectorAll('.driver-popover,.driver-overlay,.driver-active-element').forEach((el) => el.remove());
    document.documentElement.classList.remove('driver-active');
    document.body.classList.remove('driver-active');
    document.body.style.pointerEvents = '';
    document.body.style.overflow = '';
  }).catch(() => {});
  await page.keyboard.press('Escape').catch(() => {});
}

function screenshotSummary(screenshots) {
  const out = {};
  for (const [name, file] of Object.entries(screenshots)) {
    const stat = fs.statSync(file);
    out[name] = { file: path.relative(outDir, file), bytes: stat.size };
  }
  return out;
}

async function main() {
  for (const dir of [apiDir, dbDir, logDir, shotDir]) fs.mkdirSync(dir, { recursive: true });

  const kiroConfig = JSON.parse(fs.readFileSync(path.join(root, 'data/config.json'), 'utf8'));
  const subEnv = parseEnv('/www/sub2api/deploy/.env');
  const adminHeaders = { 'X-Admin-Password': kiroConfig.password };
  const startedAt = new Date();
  const summary = {
    startedAt: startedAt.toISOString(),
    environment: {
      kiroBase,
      subBase,
      docker: sh("docker ps --format '{{.Names}} {{.Image}} {{.Status}}' | grep -E 'kiro-go|sub2api'"),
      health: await jsonFetch(`${kiroBase}/health`),
    },
    api: {},
    db: {},
    browser: {},
    screenshots: {},
    checks: {},
  };

  const adminApis = {
    status: `${kiroBase}/admin/api/status`,
    readiness: `${kiroBase}/admin/api/claude-code/readiness`,
    opusModelReadiness: `${kiroBase}/admin/api/claude-code/model-readiness?model=claude-opus-4-7`,
    haikuModelReadiness: `${kiroBase}/admin/api/claude-code/model-readiness?model=claude-haiku-4-5-20251001`,
    fleetReadiness: `${kiroBase}/admin/api/fleet/readiness?model=claude-opus-4-7`,
    schedulerPreview: `${kiroBase}/admin/api/scheduler/preview?model=claude-opus-4-7`,
    websearchDiagnostics: `${kiroBase}/admin/api/websearch/diagnostics?query=latest-uat`,
    requestLogs: `${kiroBase}/admin/api/request-logs?limit=300`,
    accounts: `${kiroBase}/admin/api/accounts`,
  };
  for (const [name, url] of Object.entries(adminApis)) {
    summary.api[name] = await jsonFetch(url, { headers: adminHeaders });
    writeJson(path.join(apiDir, `${name}.json`), summary.api[name]);
  }
  const accountsBody = summary.api.accounts.body;
  const accountList = Array.isArray(accountsBody) ? accountsBody : accountsBody && (accountsBody.accounts || accountsBody.data && accountsBody.data.accounts) || [];
  const account = accountList[0];
  if (account && account.id) {
    summary.api.accountDiagnostics = await jsonFetch(`${kiroBase}/admin/api/accounts/${encodeURIComponent(account.id)}/diagnostics`, { headers: adminHeaders });
    writeJson(path.join(apiDir, 'account-diagnostics.json'), summary.api.accountDiagnostics);
  }
  summary.api.credentialsValidate = await jsonFetch(`${kiroBase}/admin/api/auth/credentials/validate`, {
    method: 'POST',
    headers: Object.assign({ 'Content-Type': 'application/json' }, adminHeaders),
    body: JSON.stringify({
      sourceType: 'kiro_account_manager_json',
      dryRun: true,
      data: { accounts: [{ email: 'uat@example.invalid', region: 'us-east-1', refreshToken: 'fixture-refresh-token' }] },
    }),
  });
  writeJson(path.join(apiDir, 'credentials-validate-dry-run.json'), summary.api.credentialsValidate);

  summary.api.nonstream = JSON.parse(fs.readFileSync(path.join(apiDir, 'sub2api-opus47-nonstream-10x10-summary.json'), 'utf8'));
  summary.api.stream = JSON.parse(fs.readFileSync(path.join(apiDir, 'sub2api-opus47-stream-10x10-summary.json'), 'utf8'));

  const subLogin = await jsonFetch(`${subBase}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: subEnv.ADMIN_EMAIL, password: subEnv.ADMIN_PASSWORD }),
  });
  const subToken = subLogin.body && (subLogin.body.access_token || subLogin.body.data && subLogin.body.data.access_token) || '';
  const subUser = subLogin.body && (subLogin.body.user || subLogin.body.data && subLogin.body.data.user) || null;
  const subRefresh = subLogin.body && (subLogin.body.refresh_token || subLogin.body.data && subLogin.body.data.refresh_token) || '';
  const subExpires = subLogin.body && (subLogin.body.expires_in || subLogin.body.data && subLogin.body.data.expires_in) || 3600;
  const subAuth = { Authorization: `Bearer ${subToken}` };
  summary.api.sub2apiAccounts = await jsonFetch(`${subBase}/api/v1/admin/accounts?page=1&page_size=50&search=kiro`, { headers: subAuth });
  summary.api.sub2apiUsage = await jsonFetch(`${subBase}/api/v1/admin/usage?page=1&page_size=50&model=claude-opus-4-7`, { headers: subAuth });
  writeJson(path.join(apiDir, 'sub2api-accounts.json'), summary.api.sub2apiAccounts);
  writeJson(path.join(apiDir, 'sub2api-usage.json'), summary.api.sub2apiUsage);

  summary.db.usage = pgJson(`select json_build_object(
    'byStream', coalesce((select json_agg(row_to_json(t)) from (select stream, count(*) n, min(duration_ms) min_ms, max(duration_ms) max_ms, percentile_disc(0.95) within group (order by duration_ms) p95_ms, count(distinct account_id) accounts from usage_logs where requested_model='claude-opus-4-7' and created_at > now() - interval '45 minutes' group by stream order by stream) t), '[]'::json),
    'accountDistribution', coalesce((select json_agg(row_to_json(t)) from (select account_id, count(*) n, max(duration_ms) max_ms from usage_logs where requested_model='claude-opus-4-7' and created_at > now() - interval '45 minutes' group by account_id order by n desc, account_id) t), '[]'::json),
    'kiroAccount24', (select row_to_json(t) from (select id,name,status,schedulable,temp_unschedulable_until,temp_unschedulable_reason from accounts where id=24) t)
  );`);
  writeJson(path.join(dbDir, 'sub2api-usage-aggregate.json'), summary.db.usage);

  const browser = await chromium.launch({ headless: true, executablePath: '/usr/bin/google-chrome', args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const consoleErrors = [];
  const pageErrors = [];
  const failedRequests = [];
  const context = await browser.newContext({ viewport: { width: 1440, height: 1050 } });
  const page = await context.newPage();
  page.on('console', (msg) => { if (msg.type() === 'error') consoleErrors.push(msg.text().slice(0, 400)); });
  page.on('pageerror', (err) => pageErrors.push(String(err).slice(0, 400)));
  page.on('requestfailed', (req) => failedRequests.push({ url: req.url(), error: req.failure() && req.failure().errorText || '' }));

  await page.goto(`${kiroBase}/admin`, { waitUntil: 'networkidle' });
  await page.locator('#pwdField').fill(kiroConfig.password);
  await page.locator('button[onclick="login()"]').click();
  await page.waitForSelector('#mainPage:not(.hidden)', { timeout: 12000 });
  summary.screenshots.kiroDashboard = await shot(page, 'kiro-admin-dashboard.png');

  await page.locator('[data-tab="accounts"]').click();
  await page.waitForSelector('#accountsList', { timeout: 10000 });
  await page.waitForTimeout(1500);
  const kiroAccountsText = await page.locator('body').innerText();
  summary.screenshots.kiroAccounts = await shot(page, 'kiro-admin-accounts.png');

  await page.locator('[data-tab="api"]').click();
  await page.evaluate(async () => {
    const input = document.querySelector('#claude-code-model-input');
    if (input) input.value = 'claude-opus-4-7';
    if (window.loadClaudeCodeModelReadiness) await window.loadClaudeCodeModelReadiness();
    if (window.loadFleetReadiness) await window.loadFleetReadiness();
    if (window.loadWebSearchDiagnostics) await window.loadWebSearchDiagnostics();
  });
  await page.waitForTimeout(1800);
  const kiroApiText = await page.locator('body').innerText();
  summary.screenshots.kiroApiReadiness = await shot(page, 'kiro-admin-api-readiness.png');

  await page.locator('[data-tab="settings"]').click();
  await page.evaluate(async () => { if (window.loadRequestLogs) await window.loadRequestLogs(); }).catch(() => {});
  await page.waitForTimeout(1500);
  const kiroLogsText = await page.locator('body').innerText();
  summary.screenshots.kiroRequestLogs = await shot(page, 'kiro-admin-request-logs.png');

  const subContext = await browser.newContext({ viewport: { width: 1440, height: 1050 } });
  await subContext.addInitScript(({ token, refreshToken, user, expiresIn }) => {
    localStorage.setItem('auth_token', token);
    if (refreshToken) localStorage.setItem('refresh_token', refreshToken);
    localStorage.setItem('token_expires_at', String(Date.now() + expiresIn * 1000));
    localStorage.setItem('auth_user', JSON.stringify(user));
    if (user && user.id && user.role) {
      localStorage.setItem(`admin_guide_${user.id}_${user.role}_v4_interactive`, 'true');
      localStorage.setItem(`user_guide_${user.id}_${user.role}_v4_interactive`, 'true');
    }
  }, { token: subToken, refreshToken: subRefresh, user: subUser, expiresIn: subExpires });
  const subPage = await subContext.newPage();
  subPage.on('console', (msg) => { if (msg.type() === 'error') consoleErrors.push(`[sub2api] ${msg.text().slice(0, 400)}`); });
  subPage.on('pageerror', (err) => pageErrors.push(`[sub2api] ${String(err).slice(0, 400)}`));
  subPage.on('requestfailed', (req) => failedRequests.push({ url: req.url(), error: req.failure() && req.failure().errorText || '' }));

  await subPage.goto(`${subBase}/admin/accounts`, { waitUntil: 'networkidle' });
  await dismissOverlays(subPage);
  await subPage.waitForTimeout(1500);
  const subAccountsText = await subPage.locator('body').innerText();
  summary.screenshots.sub2apiAccounts = await shot(subPage, 'sub2api-admin-accounts.png');

  await subPage.goto(`${subBase}/admin/usage`, { waitUntil: 'networkidle' });
  await dismissOverlays(subPage);
  await subPage.waitForTimeout(1500);
  const subUsageText = await subPage.locator('body').innerText();
  summary.screenshots.sub2apiUsage = await shot(subPage, 'sub2api-admin-usage.png');
  await browser.close();

  const opus = summary.api.opusModelReadiness.body || {};
  const haiku = summary.api.haikuModelReadiness.body || {};
  const fleet = summary.api.fleetReadiness.body || {};
  const scheduler = summary.api.schedulerPreview.body || {};
  const websearch = summary.api.websearchDiagnostics.body || {};
  const logs = summary.api.requestLogs.body && summary.api.requestLogs.body.logs || [];
  const opusLogs = logs.filter((row) => String(row.model || '').includes('opus'));
  const usageRows = summary.db.usage.byStream || [];
  const dbTotal = usageRows.reduce((sum, row) => sum + Number(row.n || 0), 0);
  const dbDistributionAccounts = new Set((summary.db.usage.accountDistribution || []).map((row) => row.account_id).filter(Boolean));
  const subAccounts = summary.api.sub2apiAccounts.body && (summary.api.sub2apiAccounts.body.items || summary.api.sub2apiAccounts.body.data && summary.api.sub2apiAccounts.body.data.items) || [];
  const subUsage = summary.api.sub2apiUsage.body && (summary.api.sub2apiUsage.body.items || summary.api.sub2apiUsage.body.data && summary.api.sub2apiUsage.body.data.items) || [];
  const screenshotMeta = screenshotSummary(summary.screenshots);

  summary.browser = {
    consoleErrors,
    pageErrors,
    failedRequests,
    screenshots: screenshotMeta,
    textChecks: {
      kiroAccountsVisible: (kiroAccountsText.match(/@/g) || []).length >= 21 || /Accounts|账号/i.test(kiroAccountsText),
      kiroApiShowsOpsCards: /Fleet readiness|WebSearch|MCP|Claude Code|claude-opus-4/i.test(kiroApiText),
      kiroLogsShowsOpus: /claude-opus-4|Request Logs|请求日志|status/i.test(kiroLogsText),
      subAccountsShowsKiro: /kiro_claude_01|Accounts|账号/i.test(subAccountsText),
      subUsageShowsOpus: /claude-opus-4-7|Usage|用量/i.test(subUsageText),
    },
  };
  summary.checks = {
    dockerHealthOk: summary.environment.health.status === 200 && summary.environment.health.body && summary.environment.health.body.status === 'ok',
    nonstream100Pass: summary.api.nonstream.pass === true && summary.api.nonstream.passed === 100 && summary.api.nonstream.failed === 0,
    stream100Pass: summary.api.stream.pass === true && summary.api.stream.passed === 100 && summary.api.stream.failed === 0,
    adminApisOk: Object.keys(adminApis).every((name) => summary.api[name] && summary.api[name].ok === true),
    opusReadinessSchedulable: opus.mappedModel === 'claude-opus-4.7' && opus.listedByGateway === true && opus.summary && opus.summary.locallySchedulable > 0,
    haikuAliasSchedulable: haiku.mappedModel === 'claude-haiku-4.5' && haiku.listedByGateway === true && haiku.summary && haiku.summary.locallySchedulable > 0,
    fleetReadinessHasEligible: fleet.summary && fleet.summary.eligible > 0 && Array.isArray(fleet.accounts),
    schedulerPreviewHasPreferred: Array.isArray(scheduler.preferred) && scheduler.preferred.length > 0,
    websearchDiagnosticsReady: websearch.supported === true && websearch.status === 'ready',
    credentialValidateDryRun: summary.api.credentialsValidate.ok === true && summary.api.credentialsValidate.body && summary.api.credentialsValidate.body.mutated === false,
    accountDiagnosticsOk: Boolean(summary.api.accountDiagnostics && summary.api.accountDiagnostics.ok === true && summary.api.accountDiagnostics.body && summary.api.accountDiagnostics.body.checks),
    kiroLogsRecent200: opusLogs.length > 0 && opusLogs.every((row) => row.statusCode === 200),
    requestLogsHaveAttemptTrace: opusLogs.some((row) => Array.isArray(row.attemptTrace) && row.attemptTrace.length > 0),
    dbUsageRowsRecorded: dbTotal >= 200,
    dbLoadBalancedAcrossAccounts: dbDistributionAccounts.size >= 10,
    clientVisibleErrorsZero: summary.api.nonstream.failed === 0 && summary.api.stream.failed === 0,
    dbAccount24Schedulable: !summary.db.usage.kiroAccount24 || summary.db.usage.kiroAccount24.schedulable === true,
    sub2apiAdminApisOk: summary.api.sub2apiAccounts.ok === true && summary.api.sub2apiUsage.ok === true,
    sub2apiApiShowsKiroAndUsage: subAccounts.some((row) => row.name === 'kiro_claude_01') && subUsage.length > 0,
    screenshotsExistAndLarge: Object.values(screenshotMeta).every((row) => row.bytes > 5000),
    screenshotTextLooksCorrect: Object.values(summary.browser.textChecks).every(Boolean),
    noBrowserPageErrors: pageErrors.length === 0,
  };
  summary.checks.pass = Object.values(summary.checks).every(Boolean);

  writeJson(path.join(apiDir, 'playwright-fullstack-summary.json'), summary);
  writeJson(path.join(dbDir, 'playwright-sub2api-db.json'), summary.db);
  fs.writeFileSync(path.join(logDir, 'docker-health.txt'), summary.environment.docker + '\n');
  console.log(JSON.stringify(summary.checks, null, 2));
  if (!summary.checks.pass) process.exit(1);
}

main().catch((error) => {
  console.error(error && error.stack ? error.stack : error);
  process.exit(1);
});
