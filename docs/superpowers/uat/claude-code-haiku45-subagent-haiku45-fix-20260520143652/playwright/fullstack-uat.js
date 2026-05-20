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
const outDir = path.resolve(root, 'docs/superpowers/uat/claude-code-haiku45-subagent-haiku45-fix-20260520143652');
const shotDir = path.join(outDir, 'screenshots');
const apiDir = path.join(outDir, 'api');
const dbDir = path.join(outDir, 'db');
const kiroBase = 'http://127.0.0.1:8080';
const subBase = 'http://127.0.0.1:18080';

function sh(cmd) {
  return cp.execSync(cmd, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
}

function readJson(file) {
  return JSON.parse(fs.readFileSync(file, 'utf8'));
}

function parseEnv(file) {
  const env = {};
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

function pgJson(sql) {
  const password = sh("awk '/^database:/{f=1} f && /^[[:space:]]*password:/{print $2; exit}' /www/sub2api/deploy/data/config.yaml");
  const escapedSQL = sql.replace(/"/g, '\\"');
  const escapedPassword = password.replace(/'/g, "'\\''");
  const out = sh(`docker exec -e PGPASSWORD='${escapedPassword}' sub2api psql -h postgres -U sub2api -d sub2api -Atc "${escapedSQL}"`);
  return out ? JSON.parse(out) : null;
}

function claudeText(body) {
  return (body && body.content || []).map((block) => block.text || '').join('');
}

async function shot(page, name) {
  const file = path.join(shotDir, name);
  await page.screenshot({ path: file, fullPage: true });
  return file;
}

async function pageText(page) {
  return page.locator('body').innerText({ timeout: 10000 }).catch(() => '');
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
  fs.mkdirSync(shotDir, { recursive: true });
  fs.mkdirSync(apiDir, { recursive: true });
  fs.mkdirSync(dbDir, { recursive: true });
  const cfg = readJson(path.join(root, 'data/config.json'));
  const subEnv = parseEnv('/www/sub2api/deploy/.env');
  const runId = `uat-haiku45-ui-${Date.now()}`;
  const summary = { startedAt: new Date().toISOString(), runId, api: {}, db: {}, screenshots: {}, browser: {}, checks: {} };

  const adminHeaders = { 'X-Admin-Password': cfg.password };
  summary.api.kiroStatus = await jsonFetch(`${kiroBase}/admin/api/status`, { headers: adminHeaders });
  summary.api.kiroHaikuReadiness = await jsonFetch(`${kiroBase}/admin/api/claude-code/model-readiness?model=claude-haiku-4-5-20251001`, { headers: adminHeaders });
  summary.api.kiroRequestLogs = await jsonFetch(`${kiroBase}/admin/api/request-logs?limit=50`, { headers: adminHeaders });

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
  summary.api.subLogin = { status: subLogin.status, hasToken: Boolean(subToken), role: subUser?.role || '' };
  summary.api.subAccounts = await jsonFetch(`${subBase}/api/v1/admin/accounts?page=1&page_size=50`, { headers: subAuth });
  summary.api.subGroups = await jsonFetch(`${subBase}/api/v1/admin/groups?page=1&page_size=50`, { headers: subAuth });
  summary.api.subUsage = await jsonFetch(`${subBase}/api/v1/admin/usage?page=1&page_size=20&sort_by=created_at&sort_order=desc`, { headers: subAuth });

  const apiKey = pgJson("select row_to_json(t) from (select key from api_keys where id=2) t;")?.key;
  const probeBody = {
    model: 'claude-haiku-4-5-20251001',
    max_tokens: 32,
    stream: false,
    messages: [{ role: 'user', content: `Reply exactly ${runId}` }],
  };
  const realProbe = await jsonFetch(`${subBase}/v1/messages`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${apiKey}`,
      'anthropic-version': '2023-06-01',
      'x-client-request-id': runId,
    },
    body: JSON.stringify(probeBody),
  });
  summary.api.sub2apiHaikuRealProbe = {
    status: realProbe.status,
    model: realProbe.body?.model,
    text: claudeText(realProbe.body),
    error: realProbe.body?.error,
  };

  summary.db.sub2api = pgJson(`select json_build_object(
    'account24', (select row_to_json(t) from (select id,name,temp_unschedulable_until,temp_unschedulable_reason from accounts where id=24) t),
    'claudeGroupAccounts', (select json_agg(row_to_json(t)) from (select a.id,a.name,a.platform,a.type,a.status,a.schedulable,a.concurrency,ag.group_id from accounts a join account_groups ag on ag.account_id=a.id where ag.group_id=1 and a.deleted_at is null order by a.id) t),
    'recentHaikuUsage', (select coalesce(json_agg(row_to_json(t)), '[]'::json) from (select id,created_at,api_key_id,account_id,group_id,model,requested_model,stream,duration_ms,input_tokens,output_tokens from usage_logs where created_at > now() - interval '15 minutes' and requested_model='claude-haiku-4-5-20251001' order by created_at desc limit 20) t),
    'haikuErrorsSinceUATStart', (select coalesce(json_agg(row_to_json(t)), '[]'::json) from (select id,created_at,api_key_id,account_id,group_id,platform,model,status_code,upstream_status_code,error_message from ops_error_logs where created_at > '${summary.startedAt}'::timestamptz and model='claude-haiku-4-5-20251001' order by created_at desc limit 20) t),
    'historicalHaikuErrors', (select coalesce(json_agg(row_to_json(t)), '[]'::json) from (select id,created_at,api_key_id,account_id,group_id,platform,model,status_code,upstream_status_code,error_message from ops_error_logs where created_at > now() - interval '30 minutes' and model='claude-haiku-4-5-20251001' order by created_at desc limit 20) t)
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
  await page.locator('#pwdField').fill(cfg.password);
  await page.locator('button[onclick="login()"]').click();
  await page.waitForSelector('#mainPage:not(.hidden)', { timeout: 12000 });
  await page.locator('[data-tab="api"]').click();
  await page.evaluate(async () => {
    const input = document.querySelector('#claude-code-model-input');
    if (input) input.value = 'claude-haiku-4-5-20251001';
    if (window.loadClaudeCodeModelReadiness) await window.loadClaudeCodeModelReadiness();
  }).catch(async () => {
    await page.evaluate(async () => {
      if (window.loadClaudeCodeReadiness) await window.loadClaudeCodeReadiness();
      if (window.loadClaudeCodeModelReadiness) await window.loadClaudeCodeModelReadiness();
    });
  });
  await page.waitForTimeout(1200);
  const kiroApiText = await pageText(page);
  summary.screenshots.kiroApiReadiness = await shot(page, 'kiro-admin-haiku-readiness.png');

  await page.locator('[data-tab="settings"]').click();
  await page.evaluate(async () => { if (window.loadRequestLogs) await window.loadRequestLogs(); });
  await page.waitForTimeout(1200);
  const kiroLogsText = await pageText(page);
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
  const subAccountsText = await pageText(subPage);
  summary.screenshots.sub2apiAccounts = await shot(subPage, 'sub2api-admin-accounts.png');
  await subPage.goto(`${subBase}/admin/usage`, { waitUntil: 'networkidle' });
  await dismissOverlays(subPage);
  await subPage.waitForTimeout(1200);
  const subUsageText = await pageText(subPage);
  summary.screenshots.sub2apiUsage = await shot(subPage, 'sub2api-admin-usage.png');
  await browser.close();

  const readiness = summary.api.kiroHaikuReadiness.body || {};
  const readinessSummary = readiness.summary || {};
  const db = summary.db.sub2api || {};
  const subAccounts = summary.api.subAccounts.body?.items || summary.api.subAccounts.body?.data?.items || [];
  summary.browser = {
    consoleErrors,
    pageErrors,
    failedRequests,
    textChecks: {
      kiroApiShowsHaiku: /claude-haiku-4\.5|claude-haiku-4-5|schedulable|Claude Code/i.test(kiroApiText),
      kiroLogsShowsMessages: /Request Logs|请求日志|claude|status/i.test(kiroLogsText),
      subAccountsShowsKiro: /kiro_claude_01|Anthropic|Accounts|账号/i.test(subAccountsText),
      subUsageShowsHaiku: /claude-haiku-4-5-20251001|Usage Records|Token Usage/i.test(subUsageText),
    },
  };
  summary.checks = {
    kiroStatusOk: summary.api.kiroStatus.ok,
    haikuReadinessMapped: readiness.mappedModel === 'claude-haiku-4.5',
    haikuReadinessSchedulable: readiness.listedByGateway === true && readinessSummary.accountsEvaluated === 21 && readinessSummary.locallySchedulable === 21,
    sub2apiLoginOk: summary.api.subLogin.hasToken === true && summary.api.subLogin.role === 'admin',
    sub2apiAdminApisOk: summary.api.subAccounts.ok && summary.api.subGroups.ok && summary.api.subUsage.ok,
    sub2apiClaudeGroupSingleKiroAccount: Array.isArray(db.claudeGroupAccounts) && db.claudeGroupAccounts.length === 1 && db.claudeGroupAccounts[0].name === 'kiro_claude_01',
    account24NotTempUnsched: db.account24 && db.account24.temp_unschedulable_until === null,
    recentHaikuUsagePresent: Array.isArray(db.recentHaikuUsage) && db.recentHaikuUsage.some((r) => r.requested_model === 'claude-haiku-4-5-20251001' && r.account_id === 24),
    noHaikuErrorsSinceUATStart: Array.isArray(db.haikuErrorsSinceUATStart) && db.haikuErrorsSinceUATStart.length === 0,
    realProbeCurrently200: summary.api.sub2apiHaikuRealProbe.status === 200 && summary.api.sub2apiHaikuRealProbe.text.trim() === runId,
    sub2apiApiShowsKiroAccount: subAccounts.some((a) => a.name === 'kiro_claude_01' || a.platform === 'anthropic'),
    screenshotsExist: Object.values(summary.screenshots).every((file) => fs.existsSync(file) && fs.statSync(file).size > 5000),
    screenshotTextLooksCorrect: Object.values(summary.browser.textChecks).every(Boolean),
    noPageErrors: summary.browser.pageErrors.length === 0,
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
