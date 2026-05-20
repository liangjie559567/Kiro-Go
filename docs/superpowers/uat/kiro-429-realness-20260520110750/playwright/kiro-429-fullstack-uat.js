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
const outDir = path.resolve(root, 'docs/superpowers/uat/kiro-429-realness-20260520110750');
const shotDir = path.join(outDir, 'screenshots');
const apiDir = path.join(outDir, 'api');
const dbDir = path.join(outDir, 'db');
const kiroBase = 'http://127.0.0.1:8080';
const subBase = 'http://127.0.0.1:18080';

function readJson(file) {
  return JSON.parse(fs.readFileSync(file, 'utf8'));
}

function sh(cmd) {
  return cp.execSync(cmd, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
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
  const escaped = sql.replace(/"/g, '\\"');
  const out = sh(`docker exec sub2api-postgres psql -U sub2api -d sub2api -Atc "${escaped}"`);
  if (!out) return null;
  return JSON.parse(out);
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

async function loginSub2apiViaLocalStorage(context, loginData) {
  const token = loginData.body?.data?.access_token || loginData.body?.access_token;
  const refreshToken = loginData.body?.data?.refresh_token || loginData.body?.refresh_token || '';
  const user = loginData.body?.data?.user || loginData.body?.user;
  const expiresIn = loginData.body?.data?.expires_in || loginData.body?.expires_in || 3600;
  await context.addInitScript(({ token, refreshToken, user, expiresIn }) => {
    localStorage.setItem('auth_token', token);
    if (refreshToken) localStorage.setItem('refresh_token', refreshToken);
    localStorage.setItem('token_expires_at', String(Date.now() + expiresIn * 1000));
    localStorage.setItem('auth_user', JSON.stringify(user));
    localStorage.setItem('onboarding_tour', 'true');
    localStorage.setItem('onboarding_tour_v4_interactive', 'true');
    localStorage.setItem('onboarding-tour-seen-admin', 'true');
    for (let id = 0; id <= 50; id += 1) {
      for (const role of ['admin', 'user', 'owner']) {
        localStorage.setItem(`onboarding_tour_${id}_${role}_v4_interactive`, 'true');
      }
    }
  }, { token, refreshToken, user, expiresIn });
}

async function dismissOverlays(page) {
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
  const startedAt = new Date();
  const startedIso = startedAt.toISOString();
  const cfg = readJson(path.join(root, 'data/config.json'));
  const subEnv = parseEnv('/www/sub2api/deploy/.env');

  const summary = {
    startedAt: startedIso,
    api: {},
    db: {},
    screenshots: {},
    browser: {},
    checks: {},
  };

  summary.api.kiroStatus = await jsonFetch(`${kiroBase}/admin/api/status`, { headers: { 'X-Admin-Password': cfg.password } });
  summary.api.kiroSettings = await jsonFetch(`${kiroBase}/admin/api/settings`, { headers: { 'X-Admin-Password': cfg.password } });
  summary.api.kiroAutoRefresh = await jsonFetch(`${kiroBase}/admin/api/auto-refresh`, { headers: { 'X-Admin-Password': cfg.password } });
  summary.api.kiroHealthCheck = await jsonFetch(`${kiroBase}/admin/api/health-check`, { headers: { 'X-Admin-Password': cfg.password } });
  summary.api.kiroAccounts = await jsonFetch(`${kiroBase}/admin/api/accounts`, { headers: { 'X-Admin-Password': cfg.password } });
  summary.api.kiroReadiness = await jsonFetch(`${kiroBase}/admin/api/claude-code/model-readiness?model=claude-opus-4-7`, { headers: { 'X-Admin-Password': cfg.password } });
  summary.api.kiroRequestLogs = await jsonFetch(`${kiroBase}/admin/api/request-logs?limit=150`, { headers: { 'X-Admin-Password': cfg.password } });
  summary.api.kiroRequestStats = await jsonFetch(`${kiroBase}/admin/api/request-stats`, { headers: { 'X-Admin-Password': cfg.password } });

  const subLogin = await jsonFetch(`${subBase}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: subEnv.ADMIN_EMAIL, password: subEnv.ADMIN_PASSWORD }),
  });
  summary.api.subLogin = { status: subLogin.status, hasToken: Boolean(subLogin.body?.data?.access_token || subLogin.body?.access_token) };
  const subToken = subLogin.body?.data?.access_token || subLogin.body?.access_token || '';
  const subAuth = { Authorization: `Bearer ${subToken}` };
  summary.api.subAccounts = await jsonFetch(`${subBase}/api/v1/admin/accounts?page=1&page_size=50`, { headers: subAuth });
  summary.api.subGroups = await jsonFetch(`${subBase}/api/v1/admin/groups?page=1&page_size=50`, { headers: subAuth });
  summary.api.subUsage = await jsonFetch(`${subBase}/api/v1/admin/usage?page=1&page_size=20&sort_by=created_at&sort_order=desc`, { headers: subAuth });
  summary.api.subOpsErrors = await jsonFetch(`${subBase}/api/v1/admin/ops/request-errors?page=1&page_size=20&status_codes=429`, { headers: subAuth });

  summary.db.sub2api = pgJson(`select json_build_object(
    'apiKeys', (select json_agg(row_to_json(t)) from (select id,name,group_id,status,left(key,8)||'...'||right(key,4) as key_mask from api_keys where deleted_at is null order by id) t),
    'accounts', (select json_agg(row_to_json(t)) from (select a.id,a.name,a.platform,a.type,a.status,a.schedulable,a.concurrency,a.rate_limit_reset_at,a.temp_unschedulable_until,a.temp_unschedulable_reason,ag.group_id from accounts a left join account_groups ag on ag.account_id=a.id where a.deleted_at is null order by a.id) t),
    'recent429', (select json_agg(row_to_json(t)) from (select id,created_at,api_key_id,account_id,group_id,platform,model,requested_model,stream,status_code,upstream_status_code,error_message from ops_error_logs where created_at > now() - interval '3 hours' and coalesce(upstream_status_code,status_code)=429 order by created_at desc limit 30) t),
    'usageSinceStart', (select json_agg(row_to_json(t)) from (select id,created_at,api_key_id,account_id,group_id,model,requested_model,upstream_model,stream,duration_ms,input_tokens,output_tokens from usage_logs where created_at >= '${startedIso}'::timestamptz order by created_at desc limit 20) t)
  );`);

  const apiKey = pgJson("select row_to_json(t) from (select key from api_keys where id=2) t;")?.key;
  const realReq = await jsonFetch(`${subBase}/v1/messages`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${apiKey}`,
      'anthropic-version': '2023-06-01',
      'x-client-request-id': `playwright-kiro-429-uat-${Date.now()}`,
    },
    body: JSON.stringify({
      model: 'claude-opus-4-7',
      max_tokens: 16,
      stream: false,
      messages: [{ role: 'user', content: 'Playwright fullstack UAT probe. Reply exactly: ok' }],
    }),
  });
  summary.api.sub2apiRealProbe = {
    status: realReq.status,
    model: realReq.body?.model,
    text: claudeText(realReq.body),
    error: realReq.body?.error,
  };

  await new Promise((resolve) => setTimeout(resolve, 1000));
  summary.db.afterProbeUsage = pgJson(`select json_agg(row_to_json(t)) from (select id,created_at,api_key_id,account_id,group_id,model,requested_model,upstream_model,stream,duration_ms,input_tokens,output_tokens from usage_logs where created_at >= '${startedIso}'::timestamptz order by created_at desc limit 20) t;`);

  const browser = await chromium.launch({ headless: true, executablePath: '/usr/bin/google-chrome', args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1050 } });
  const consoleErrors = [];
  const pageErrors = [];
  const failedRequests = [];
  const page = await context.newPage();
  page.on('console', (msg) => {
    if (msg.type() === 'error') consoleErrors.push(msg.text().slice(0, 500));
  });
  page.on('pageerror', (err) => pageErrors.push(String(err).slice(0, 500)));
  page.on('requestfailed', (req) => failedRequests.push({ url: req.url(), error: req.failure()?.errorText || '' }));

  await page.goto(`${kiroBase}/admin`, { waitUntil: 'networkidle' });
  await page.locator('#pwdField').fill(cfg.password);
  await page.locator('button[onclick="login()"]').click();
  await page.waitForSelector('#mainPage:not(.hidden)', { timeout: 12000 });
  await page.waitForTimeout(1200);
  const kiroAccountsText = await pageText(page);
  summary.screenshots.kiroAccounts = await shot(page, 'kiro-admin-accounts.png');
  await page.locator('[data-tab="api"]').click();
  await page.waitForTimeout(1200);
  const kiroApiText = await pageText(page);
  summary.screenshots.kiroApi = await shot(page, 'kiro-admin-api-readiness.png');
  await page.locator('[data-tab="settings"]').click();
  await page.waitForTimeout(1200);
  const kiroSettingsText = await pageText(page);
  summary.screenshots.kiroSettings = await shot(page, 'kiro-admin-settings-request-logs.png');

  const subContext = await browser.newContext({ viewport: { width: 1440, height: 1050 } });
  await loginSub2apiViaLocalStorage(subContext, subLogin);
  const subPage = await subContext.newPage();
  subPage.on('console', (msg) => {
    if (msg.type() === 'error') consoleErrors.push(`[sub2api] ${msg.text().slice(0, 500)}`);
  });
  subPage.on('pageerror', (err) => pageErrors.push(`[sub2api] ${String(err).slice(0, 500)}`));
  subPage.on('requestfailed', (req) => failedRequests.push({ url: req.url(), error: req.failure()?.errorText || '' }));

  await subPage.goto(`${subBase}/admin/dashboard`, { waitUntil: 'networkidle' });
  await dismissOverlays(subPage);
  await subPage.waitForTimeout(1200);
  const subDashboardText = await pageText(subPage);
  summary.screenshots.subDashboard = await shot(subPage, 'sub2api-admin-dashboard.png');
  await subPage.goto(`${subBase}/admin/accounts`, { waitUntil: 'networkidle' });
  await dismissOverlays(subPage);
  await subPage.waitForTimeout(1200);
  const subAccountsText = await pageText(subPage);
  summary.screenshots.subAccounts = await shot(subPage, 'sub2api-admin-accounts.png');
  const accountSearch = subPage.locator('input[placeholder*="Search"], input[type="search"], input').first();
  await accountSearch.fill('kiro_claude_01').catch(() => {});
  await subPage.waitForTimeout(1200);
  const subAccountsFilteredText = await pageText(subPage);
  summary.screenshots.subAccountsKiroFiltered = await shot(subPage, 'sub2api-admin-accounts-kiro-filtered.png');
  await subPage.goto(`${subBase}/admin/groups`, { waitUntil: 'networkidle' });
  await dismissOverlays(subPage);
  await subPage.waitForTimeout(1200);
  const subGroupsText = await pageText(subPage);
  summary.screenshots.subGroups = await shot(subPage, 'sub2api-admin-groups.png');
  await subPage.goto(`${subBase}/admin/usage`, { waitUntil: 'networkidle' });
  await dismissOverlays(subPage);
  await subPage.waitForTimeout(1800);
  const subUsageText = await pageText(subPage);
  summary.screenshots.subUsage = await shot(subPage, 'sub2api-admin-usage.png');
  await browser.close();

  const readinessAccounts = summary.api.kiroReadiness.body?.accounts || [];
  const requestLogs = summary.api.kiroRequestLogs.body?.logs || [];
  const dbAccounts = summary.db.sub2api?.accounts || [];
  const db429 = summary.db.sub2api?.recent429 || [];
  const afterProbeUsage = summary.db.afterProbeUsage || [];
  const subAccountsItems = summary.api.subAccounts.body?.data?.items || summary.api.subAccounts.body?.items || [];
  const subGroupsItems = summary.api.subGroups.body?.data?.items || summary.api.subGroups.body?.items || [];

  summary.browser = {
    consoleErrors,
    pageErrors,
    failedRequests,
    textChecks: {
      kiroAccountsShows21: kiroAccountsText.includes('21') || kiroAccountsText.includes('账号'),
      kiroApiShowsOpus47: /claude-opus-4[.-]7/.test(kiroApiText),
      kiroApiShowsSchedulable: /schedulable|可调度|yes/.test(kiroApiText),
      kiroSettingsShowsRequestLogs: /请求日志|Request Logs|claude-opus-4/.test(kiroSettingsText),
      subDashboardShowsAdmin: /Admin|管理员|Dashboard|仪表/.test(subDashboardText),
      subAccountsShowsKiroClaude: /kiro_claude_01|anthropic/i.test(`${subAccountsText}\n${subAccountsFilteredText}`),
      subGroupsShowsGroups: /Groups|分组|claude|openai/i.test(subGroupsText),
      subUsageShowsUsage: /Usage|用量|claude-opus-4-7|Tokens|令牌/i.test(subUsageText),
      noSub2apiOnboardingOverlay: !/Welcome to Sub2API|initial setup|Let's complete/.test([subDashboardText, subAccountsText, subGroupsText, subUsageText].join('\n')),
    },
  };

  summary.checks = {
    kiroApiHealthy: summary.api.kiroStatus.ok && Number(summary.api.kiroStatus.body?.accounts || 0) === 21,
    readinessAll21Schedulable: readinessAccounts.length === 21 && readinessAccounts.every((a) => a.schedulable === true),
    readinessModelListed: summary.api.kiroReadiness.body?.listedByGateway === true && /schedulable/.test(summary.api.kiroReadiness.body?.routingReason || ''),
    historical429ExistsInKiroLogs: requestLogs.some((l) => l.statusCode === 429 && /TEMPORARY_LIMITED|temporary limits|429|rate/i.test(l.error || '')),
    historical429ExistsInSub2apiDb: db429.length > 0 && db429.some((l) => l.api_key_id === 2 && l.account_id === 24 && l.group_id === 1),
    sub2apiClaudeGroupSingleKiroAccount: dbAccounts.filter((a) => a.group_id === 1).length === 1 && dbAccounts.some((a) => a.group_id === 1 && a.name === 'kiro_claude_01'),
    sub2apiOpenaiGroupHasFallbacks: dbAccounts.filter((a) => a.group_id === 2).length > 1,
    sub2apiAdminApisOk: summary.api.subLogin.status === 200 && summary.api.subAccounts.ok && summary.api.subGroups.ok && summary.api.subUsage.ok,
    sub2apiApiShowsKiroAccount: subAccountsItems.some((a) => a.name === 'kiro_claude_01' || a.platform === 'anthropic'),
    sub2apiApiShowsGroups: subGroupsItems.length >= 2,
    realProbeCurrently200: summary.api.sub2apiRealProbe.status === 200 && summary.api.sub2apiRealProbe.text.trim() === 'ok',
    realProbeRecordedInUsageDb: afterProbeUsage.some((u) => u.api_key_id === 2 && u.account_id === 24 && /claude-opus-4-7|claude-opus-4\.7/.test([u.model, u.requested_model, u.upstream_model].join(' '))),
    screenshotsExist: Object.values(summary.screenshots).every((file) => fs.existsSync(file) && fs.statSync(file).size > 5000),
    screenshotTextLooksCorrect: Object.values(summary.browser.textChecks).every(Boolean),
    noPageErrors: summary.browser.pageErrors.length === 0,
  };
  summary.checks.pass = Object.values(summary.checks).every(Boolean);

  fs.writeFileSync(path.join(apiDir, 'playwright-fullstack-summary.json'), JSON.stringify(summary, null, 2));
  fs.writeFileSync(path.join(dbDir, 'sub2api-fullstack-db.json'), JSON.stringify(summary.db, null, 2));

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
