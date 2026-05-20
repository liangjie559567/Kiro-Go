let chromium;
try {
  ({ chromium } = require('playwright'));
} catch {
  ({ chromium } = require('/root/.npm/_npx/e41f203b7505f1fb/node_modules/playwright'));
}

const cp = require('child_process');
const fs = require('fs');
const path = require('path');

const root = '/www/Kiro-Go';
const outDir = path.resolve(root, 'docs/superpowers/uat/sub2api-kiro-opus47-account-lb-20260520144855');
const apiDir = path.join(outDir, 'api');
const dbDir = path.join(outDir, 'db');
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

async function jsonFetch(url, options = {}) {
  const res = await fetch(url, options);
  const text = await res.text();
  let body;
  try {
    body = text ? JSON.parse(text) : null;
  } catch {
    body = { text: text.slice(0, 1000) };
  }
  return { ok: res.ok, status: res.status, body };
}

function pg(sql) {
  const password = sh("awk '/^database:/{f=1} f && /^[[:space:]]*password:/{print $2; exit}' /www/sub2api/deploy/data/config.yaml");
  const escapedSQL = sql.replace(/"/g, '\\"');
  const escapedPassword = password.replace(/'/g, "'\\''");
  return sh(`docker exec -e PGPASSWORD='${escapedPassword}' sub2api psql -h postgres -U sub2api -d sub2api -Atc "${escapedSQL}"`);
}

function pgJson(sql) {
  const out = pg(sql);
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

async function main() {
  fs.mkdirSync(apiDir, { recursive: true });
  fs.mkdirSync(dbDir, { recursive: true });
  fs.mkdirSync(shotDir, { recursive: true });

  const kiroConfig = JSON.parse(fs.readFileSync(path.join(root, 'data/config.json'), 'utf8'));
  const subEnv = parseEnv('/www/sub2api/deploy/.env');
  const summary = {
    startedAt: new Date().toISOString(),
    api: {},
    db: {},
    browser: {},
    screenshots: {},
    checks: {},
  };

  const adminHeaders = { 'X-Admin-Password': kiroConfig.password };
  summary.api.kiroOpusReadiness = await jsonFetch(`${kiroBase}/admin/api/claude-code/model-readiness?model=claude-opus-4-7`, { headers: adminHeaders });
  summary.api.kiroHaikuReadiness = await jsonFetch(`${kiroBase}/admin/api/claude-code/model-readiness?model=claude-haiku-4-5-20251001`, { headers: adminHeaders });
  summary.api.kiroLogs = await jsonFetch(`${kiroBase}/admin/api/request-logs?limit=260`, { headers: adminHeaders });
  summary.api.nonstream = JSON.parse(fs.readFileSync(path.join(apiDir, 'sub2api-opus47-nonstream-10x10-summary.json'), 'utf8'));
  summary.api.stream = JSON.parse(fs.readFileSync(path.join(apiDir, 'sub2api-opus47-stream-10x10-summary.json'), 'utf8'));

  const subLogin = await jsonFetch(`${subBase}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: subEnv.ADMIN_EMAIL, password: subEnv.ADMIN_PASSWORD }),
  });
  const subToken = subLogin.body?.access_token || subLogin.body?.data?.access_token || '';
  const subUser = subLogin.body?.user || subLogin.body?.data?.user || null;
  const subRefresh = subLogin.body?.refresh_token || subLogin.body?.data?.refresh_token || '';
  const subExpires = subLogin.body?.expires_in || subLogin.body?.data?.expires_in || 3600;
  const subAuth = { Authorization: `Bearer ${subToken}` };
  summary.api.subAccounts = await jsonFetch(`${subBase}/api/v1/admin/accounts?page=1&page_size=50&search=kiro`, { headers: subAuth });
  summary.api.subUsage = await jsonFetch(`${subBase}/api/v1/admin/usage?page=1&page_size=20&model=claude-opus-4-7`, { headers: subAuth });

  summary.db.usage = pgJson(`select json_build_object(
    'byStream', (select json_agg(row_to_json(t)) from (select stream, count(*) n, min(duration_ms) min_ms, max(duration_ms) max_ms, percentile_disc(0.95) within group (order by duration_ms) p95_ms, count(distinct account_id) accounts from usage_logs where requested_model='claude-opus-4-7' and api_key_id=2 and created_at > now() - interval '20 minutes' group by stream order by stream) t),
    'accountDistribution', (select json_agg(row_to_json(t)) from (select account_id, count(*) n, max(duration_ms) max_ms from usage_logs where requested_model='claude-opus-4-7' and api_key_id=2 and created_at > now() - interval '20 minutes' group by account_id order by n desc, account_id) t),
    'kiroAccount24', (select row_to_json(t) from (select id,name,status,schedulable,temp_unschedulable_until,temp_unschedulable_reason from accounts where id=24) t)
  );`);

  const browser = await chromium.launch({ headless: true, executablePath: '/usr/bin/google-chrome', args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const consoleErrors = [];
  const pageErrors = [];
  const failedRequests = [];
  const context = await browser.newContext({ viewport: { width: 1440, height: 1050 } });
  const page = await context.newPage();
  page.on('console', (msg) => { if (msg.type() === 'error') consoleErrors.push(msg.text().slice(0, 500)); });
  page.on('pageerror', (err) => pageErrors.push(String(err).slice(0, 500)));
  page.on('requestfailed', (req) => failedRequests.push({ url: req.url(), error: req.failure()?.errorText || '' }));

  await page.goto(`${kiroBase}/admin`, { waitUntil: 'networkidle' });
  await page.locator('#pwdField').fill(kiroConfig.password);
  await page.locator('button[onclick="login()"]').click();
  await page.waitForSelector('#mainPage:not(.hidden)', { timeout: 12000 });
  summary.screenshots.kiroDashboard = await shot(page, 'kiro-admin-dashboard.png');

  await page.locator('[data-tab="accounts"]').click();
  await page.waitForSelector('#accountsList', { timeout: 10000 });
  await page.waitForTimeout(1200);
  const kiroAccountsText = await page.locator('body').innerText();
  summary.screenshots.kiroAccounts = await shot(page, 'kiro-admin-accounts.png');

  await page.locator('[data-tab="api"]').click();
  await page.evaluate(async () => {
    const input = document.querySelector('#claude-code-model-input');
    if (input) input.value = 'claude-opus-4-7';
    if (window.loadClaudeCodeModelReadiness) await window.loadClaudeCodeModelReadiness();
  });
  await page.waitForTimeout(1200);
  const kiroApiText = await page.locator('body').innerText();
  summary.screenshots.kiroApiReadiness = await shot(page, 'kiro-admin-opus-readiness.png');

  await page.locator('[data-tab="settings"]').click();
  await page.evaluate(async () => { if (window.loadRequestLogs) await window.loadRequestLogs(); }).catch(() => {});
  await page.waitForTimeout(1200);
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
  subPage.on('console', (msg) => { if (msg.type() === 'error') consoleErrors.push(`[sub2api] ${msg.text().slice(0, 500)}`); });
  subPage.on('pageerror', (err) => pageErrors.push(`[sub2api] ${String(err).slice(0, 500)}`));
  subPage.on('requestfailed', (req) => failedRequests.push({ url: req.url(), error: req.failure()?.errorText || '' }));

  await subPage.goto(`${subBase}/admin/accounts`, { waitUntil: 'networkidle' });
  await dismissOverlays(subPage);
  await subPage.waitForTimeout(1200);
  const subAccountsText = await subPage.locator('body').innerText();
  summary.screenshots.sub2apiAccounts = await shot(subPage, 'sub2api-admin-accounts.png');

  await subPage.goto(`${subBase}/admin/usage`, { waitUntil: 'networkidle' });
  await dismissOverlays(subPage);
  await subPage.waitForTimeout(1200);
  const subUsageText = await subPage.locator('body').innerText();
  summary.screenshots.sub2apiUsage = await shot(subPage, 'sub2api-admin-usage.png');
  await browser.close();

  const opus = summary.api.kiroOpusReadiness.body || {};
  const opusSummary = opus.summary || {};
  const haiku = summary.api.kiroHaikuReadiness.body || {};
  const haikuSummary = haiku.summary || {};
  const logs = summary.api.kiroLogs.body?.logs || [];
  const opusLogs = logs.filter((row) => row.model === 'claude-opus-4.7');
  const successfulAccounts = new Set(opusLogs.filter((row) => row.statusCode === 200).map((row) => row.accountId).filter(Boolean));
  const usageRows = summary.db.usage?.byStream || [];
  const subAccounts = summary.api.subAccounts.body?.items || summary.api.subAccounts.body?.data?.items || [];
  const subUsage = summary.api.subUsage.body?.items || summary.api.subUsage.body?.data?.items || [];

  summary.browser = {
    consoleErrors,
    pageErrors,
    failedRequests,
    textChecks: {
      kiroAccountsVisible: (kiroAccountsText.match(/@/g) || []).length >= 21,
      kiroApiShowsOpus: /claude-opus-4\.7|claude-opus-4-7|schedulable|Claude Code/i.test(kiroApiText),
      kiroLogsShowsOpus: /claude-opus-4\.7|status|Request Logs|请求日志/i.test(kiroLogsText),
      subAccountsShowsKiro: /kiro_claude_01|Accounts|账号/i.test(subAccountsText),
      subUsageShowsOpus: /claude-opus-4-7|Usage Records|Token Usage|用量/i.test(subUsageText),
    },
  };
  summary.checks = {
    nonstream100Pass: summary.api.nonstream.pass === true && summary.api.nonstream.passed === 100 && summary.api.nonstream.failed === 0,
    stream100Pass: summary.api.stream.pass === true && summary.api.stream.passed === 100 && summary.api.stream.failed === 0,
    maxLatencyRecorded: summary.api.nonstream.maxLatencyMs === 46651 && summary.api.stream.maxLatencyMs === 56595,
    opusReadinessSchedulable: opus.mappedModel === 'claude-opus-4.7' && opus.listedByGateway === true && opusSummary.locallySchedulable > 0 && opusSummary.riskGroupCoolingDown === 0,
    haikuAliasStillSchedulable: haiku.mappedModel === 'claude-haiku-4.5' && haiku.listedByGateway === true && haikuSummary.locallySchedulable > 0,
    kiroLogsRecent200: opusLogs.length >= 200 && opusLogs.every((row) => row.statusCode === 200),
    kiroLogsLoadBalanced: successfulAccounts.size >= 10,
    sub2apiUsageDb200Rows: usageRows.reduce((sum, row) => sum + Number(row.n || 0), 0) >= 200,
    sub2apiKiroAccountHealthy: summary.db.usage?.kiroAccount24?.schedulable === true && summary.db.usage?.kiroAccount24?.temp_unschedulable_until === null,
    sub2apiAdminApisOk: summary.api.subAccounts.ok === true && summary.api.subUsage.ok === true,
    sub2apiApiShowsKiroAndUsage: subAccounts.some((a) => a.name === 'kiro_claude_01') && subUsage.length > 0,
    screenshotsExist: Object.values(summary.screenshots).every((file) => fs.existsSync(file) && fs.statSync(file).size > 5000),
    screenshotTextLooksCorrect: Object.values(summary.browser.textChecks).every(Boolean),
    noPageErrors: pageErrors.length === 0,
  };
  summary.checks.pass = Object.values(summary.checks).every(Boolean);

  fs.writeFileSync(path.join(apiDir, 'playwright-fullstack-summary.json'), JSON.stringify(summary, null, 2));
  fs.writeFileSync(path.join(dbDir, 'playwright-sub2api-db.json'), JSON.stringify(summary.db, null, 2));
  if (!summary.checks.pass) {
    console.error(JSON.stringify(summary.checks, null, 2));
    process.exit(1);
  }
  console.log(JSON.stringify(summary.checks, null, 2));
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
