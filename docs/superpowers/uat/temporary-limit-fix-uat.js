const { chromium } = require('/root/.npm/_npx/e41f203b7505f1fb/node_modules/playwright');
const fs = require('fs');
const path = require('path');
const cp = require('child_process');

const root = '/www/Kiro-Go';
const outDir = process.env.OUT_DIR;
const runId = path.basename(outDir);
const kiroBase = 'http://127.0.0.1:8080';
const subBase = 'http://127.0.0.1:18080';

function readJSON(file) {
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

function sh(cmd) {
  return cp.execSync(cmd, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
}

function pgJson(sql) {
  const escaped = sql.replace(/"/g, '\\"');
  const out = sh(`docker exec sub2api-postgres psql -U sub2api -d sub2api -Atc "${escaped}"`);
  return out ? JSON.parse(out) : null;
}

async function jsonFetch(url, options = {}) {
  const res = await fetch(url, options);
  const text = await res.text();
  let body;
  try {
    body = text ? JSON.parse(text) : null;
  } catch {
    body = { text };
  }
  return {
    ok: res.ok,
    status: res.status,
    headers: Object.fromEntries(res.headers.entries()),
    body,
    text,
  };
}

function messageText(body) {
  return ((body && body.content) || []).map((block) => block.text || block.thinking || '').join('');
}

async function capture(page, name) {
  const file = path.join(outDir, name);
  await page.screenshot({ path: file, fullPage: true });
  return file;
}

async function dismissSub2apiOnboarding(page) {
  await page.evaluate(() => {
    for (let id = 0; id <= 50; id++) {
      for (const role of ['admin', 'user', 'owner']) {
        localStorage.setItem(`onboarding_tour_${id}_${role}_v4_interactive`, 'true');
      }
    }
    localStorage.setItem('onboarding_tour', 'true');
    localStorage.setItem('onboarding_tour_v4_interactive', 'true');
    localStorage.setItem('onboarding-tour-seen-admin', 'true');
    document.querySelectorAll('.driver-popover,.driver-overlay,.driver-active-element').forEach((el) => el.remove());
    document.documentElement.classList.remove('driver-active');
    document.body.classList.remove('driver-active');
    document.body.style.pointerEvents = '';
    document.body.style.overflow = '';
  }).catch(() => {});
  await page.keyboard.press('Escape').catch(() => {});
}

async function visibleText(page) {
  return page.locator('body').innerText({ timeout: 10000 }).catch(() => '');
}

async function main() {
  if (!outDir) throw new Error('OUT_DIR is required');
  fs.mkdirSync(outDir, { recursive: true });

  const startedAt = new Date().toISOString();
  const cfg = readJSON(path.join(root, 'data/config.json'));
  const subEnv = parseEnv('/www/sub2api/deploy/.env');
  const summary = {
    runId,
    startedAt,
    outDir,
    api: {},
    db: {},
    browser: {},
    screenshots: {},
    checks: {},
    notes: [],
  };

  summary.api.kiroHealth = await jsonFetch(`${kiroBase}/health`);
  summary.api.sub2apiHealth = await jsonFetch(`${subBase}/health`);
  summary.api.kiroAccountsBefore = await jsonFetch(`${kiroBase}/admin/api/accounts`, {
    headers: { 'X-Admin-Password': cfg.password },
  });
  summary.api.kiroAccountsBefore.body = (summary.api.kiroAccountsBefore.body || []).map((a) => ({
    id: a.id,
    enabled: a.enabled,
    lastFailureReason: a.lastFailureReason || '',
    cooldownUntil: a.cooldownUntil || 0,
    failureCount: a.failureCount || 0,
  }));

  summary.db.before = pgJson("select json_build_object('usageRows', count(*), 'lastUsage', max(created_at)) from usage_logs;");
  summary.db.claudeKeyShape = pgJson(`
    select row_to_json(t)
    from (
      select k.id, k.name, k.status, g.name as group_name, left(k.key, 8) || '...' as key_prefix
      from api_keys k
      join groups g on g.id = k.group_id
      where k.deleted_at is null and k.status = 'active' and g.name = 'claude'
      order by k.id
      limit 1
    ) t;
  `);
  const claudeKey = pgJson(`
    select row_to_json(t)
    from (
      select k.key
      from api_keys k
      join groups g on g.id = k.group_id
      where k.deleted_at is null and k.status = 'active' and g.name = 'claude'
      order by k.id
      limit 1
    ) t;
  `);

  const directRequestId = `${runId}-direct-opus`;
  const direct = await jsonFetch(`${kiroBase}/v1/messages`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${cfg.apiKey}`,
      'anthropic-version': '2023-06-01',
      'anthropic-beta': 'claude-code-20250219,fine-grained-tool-streaming-2025-05-14',
      'x-claude-code-session-id': runId,
      'x-claude-code-agent-id': 'uat-direct',
      'x-request-id': directRequestId,
    },
    body: JSON.stringify({
      model: 'claude-opus-4.7',
      max_tokens: 64,
      stream: false,
      system: 'You are Claude Code. Reply exactly with the marker when asked.',
      messages: [{ role: 'user', content: `Reply exactly: ${directRequestId}` }],
    }),
  });
  summary.api.direct = {
    requestId: directRequestId,
    status: direct.status,
    retryAfter: direct.headers['retry-after'] || '',
    text: messageText(direct.body),
    bodyPreview: JSON.stringify(direct.body).slice(0, 1200),
  };

  if (claudeKey && claudeKey.key) {
    const subSonnetRequestId = `${runId}-sub2api-sonnet`;
    const subSonnetMessage = await jsonFetch(`${subBase}/v1/messages`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${claudeKey.key}`,
        'anthropic-version': '2023-06-01',
        'x-request-id': subSonnetRequestId,
      },
      body: JSON.stringify({
        model: 'claude-sonnet-4-5-20250929',
        max_tokens: 64,
        stream: false,
        messages: [{ role: 'user', content: `Reply exactly: ${subSonnetRequestId}` }],
      }),
    });
    summary.api.sub2apiSonnetMessage = {
      requestId: subSonnetRequestId,
      model: 'claude-sonnet-4-5-20250929',
      status: subSonnetMessage.status,
      retryAfter: subSonnetMessage.headers['retry-after'] || '',
      text: messageText(subSonnetMessage.body),
      bodyPreview: JSON.stringify(subSonnetMessage.body).slice(0, 1200),
    };

    const subOpusRequestId = `${runId}-sub2api-opus`;
    const subOpusMessage = await jsonFetch(`${subBase}/v1/messages`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${claudeKey.key}`,
        'anthropic-version': '2023-06-01',
        'x-request-id': subOpusRequestId,
      },
      body: JSON.stringify({
        model: 'claude-opus-4-7',
        max_tokens: 64,
        stream: false,
        messages: [{ role: 'user', content: `Reply exactly: ${subOpusRequestId}` }],
      }),
    });
    summary.api.sub2apiOpusMessage = {
      requestId: subOpusRequestId,
      model: 'claude-opus-4-7',
      status: subOpusMessage.status,
      retryAfter: subOpusMessage.headers['retry-after'] || '',
      text: messageText(subOpusMessage.body),
      bodyPreview: JSON.stringify(subOpusMessage.body).slice(0, 1200),
    };
  } else {
    summary.api.sub2apiSonnetMessage = { skipped: true, reason: 'no active api key in claude group' };
    summary.api.sub2apiOpusMessage = { skipped: true, reason: 'no active api key in claude group' };
  }

  await new Promise((resolve) => setTimeout(resolve, 1500));
  summary.api.kiroAccountsAfter = await jsonFetch(`${kiroBase}/admin/api/accounts`, {
    headers: { 'X-Admin-Password': cfg.password },
  });
  summary.api.kiroAccountsAfter.body = (summary.api.kiroAccountsAfter.body || []).map((a) => ({
    id: a.id,
    enabled: a.enabled,
    lastFailureReason: a.lastFailureReason || '',
    cooldownUntil: a.cooldownUntil || 0,
    failureCount: a.failureCount || 0,
  }));
  summary.api.kiroLogs = await jsonFetch(`${kiroBase}/admin/api/request-logs?limit=30`, {
    headers: { 'X-Admin-Password': cfg.password },
  });
  summary.db.after = pgJson("select json_build_object('usageRows', count(*), 'lastUsage', max(created_at)) from usage_logs;");
  summary.db.markerUsage = pgJson(`
    select json_agg(row_to_json(t))
    from (
      select ul.id, ul.request_id, ul.model, ul.requested_model, ul.upstream_model, ul.stream,
             ul.input_tokens, ul.output_tokens, ul.duration_ms, ul.created_at,
             a.name as account_name, g.name as group_name, k.name as api_key_name
      from usage_logs ul
      join api_keys k on k.id = ul.api_key_id
      left join groups g on g.id = ul.group_id
      left join accounts a on a.id = ul.account_id
      where ul.request_id like '${runId}%'
      order by ul.created_at desc
    ) t;
  `);
  summary.db.windowUsage = pgJson(`
    select json_agg(row_to_json(t))
    from (
      select ul.id, ul.request_id, ul.model, ul.requested_model, ul.upstream_model, ul.stream,
             ul.input_tokens, ul.output_tokens, ul.duration_ms, ul.created_at,
             a.name as account_name, g.name as group_name, k.name as api_key_name
      from usage_logs ul
      join api_keys k on k.id = ul.api_key_id
      left join groups g on g.id = ul.group_id
      left join accounts a on a.id = ul.account_id
      where ul.created_at >= '${startedAt}'::timestamptz
        and k.name = 'claude'
        and g.name = 'claude'
        and a.name = 'kiro_claude_01'
        and ul.requested_model in ('claude-sonnet-4-5-20250929', 'claude-opus-4-7')
      order by ul.created_at desc
    ) t;
  `);

  const browser = await chromium.launch({
    headless: true,
    executablePath: '/usr/bin/google-chrome',
    args: ['--no-sandbox', '--disable-dev-shm-usage'],
  });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1050 } });
  await context.addInitScript(() => {
    for (let id = 0; id <= 50; id++) {
      for (const role of ['admin', 'user', 'owner']) {
        localStorage.setItem(`onboarding_tour_${id}_${role}_v4_interactive`, 'true');
      }
    }
    localStorage.setItem('onboarding_tour', 'true');
    localStorage.setItem('onboarding_tour_v4_interactive', 'true');
    localStorage.setItem('onboarding-tour-seen-admin', 'true');
  });
  const page = await context.newPage();
  const consoleErrors = [];
  const pageErrors = [];
  const requestFailures = [];
  page.on('console', (msg) => {
    if (msg.type() === 'error') consoleErrors.push(msg.text().slice(0, 300));
  });
  page.on('pageerror', (err) => pageErrors.push(String(err).slice(0, 300)));
  page.on('requestfailed', (req) => {
    if (req.url().includes('/api/')) requestFailures.push({ url: req.url(), error: req.failure() && req.failure().errorText });
  });

  await page.goto(`${kiroBase}/admin`, { waitUntil: 'networkidle' });
  await page.locator('#pwdField').fill(cfg.password);
  await page.getByRole('button', { name: /登录|Login/ }).click();
  await page.waitForSelector('#mainPage:not(.hidden)', { timeout: 12000 });
  await page.waitForTimeout(1000);
  summary.screenshots.kiroDashboard = await capture(page, 'kiro-admin-dashboard.png');
  await page.locator('[data-tab="api"]').click();
  await page.waitForSelector('#claude-code-readiness', { timeout: 10000 });
  await page.waitForTimeout(1000);
  const kiroApiText = await visibleText(page);
  summary.screenshots.kiroApi = await capture(page, 'kiro-admin-api-readiness.png');
  await page.evaluate(() => {
    const logs = document.querySelector('#requestLogsBody');
    if (logs) logs.closest('.card')?.scrollIntoView({ block: 'start' });
  });
  await page.waitForTimeout(1000);
  const kiroLogsText = await visibleText(page);
  summary.screenshots.kiroLogs = await capture(page, 'kiro-admin-request-logs.png');

  await page.goto(`${subBase}/login`, { waitUntil: 'networkidle' });
  await page.locator('#email').fill(subEnv.ADMIN_EMAIL);
  await page.locator('#password').fill(subEnv.ADMIN_PASSWORD);
  await page.getByRole('button', { name: /登录|Sign in|Sign In/i }).click();
  await page.waitForTimeout(2500);
  await dismissSub2apiOnboarding(page);
  await page.goto(`${subBase}/admin/dashboard`, { waitUntil: 'networkidle' });
  await dismissSub2apiOnboarding(page);
  await page.waitForTimeout(1500);
  const subDashboardText = await visibleText(page);
  summary.screenshots.subDashboard = await capture(page, 'sub2api-dashboard.png');
  await page.goto(`${subBase}/admin/accounts`, { waitUntil: 'networkidle' });
  await dismissSub2apiOnboarding(page);
  await page.waitForTimeout(1500);
  const subAccountsText = await visibleText(page);
  summary.screenshots.subAccounts = await capture(page, 'sub2api-accounts.png');
  await page.goto(`${subBase}/admin/groups`, { waitUntil: 'networkidle' });
  await dismissSub2apiOnboarding(page);
  await page.waitForTimeout(1500);
  const subGroupsText = await visibleText(page);
  summary.screenshots.subGroups = await capture(page, 'sub2api-groups.png');
  await page.goto(`${subBase}/admin/usage`, { waitUntil: 'networkidle' });
  await dismissSub2apiOnboarding(page);
  await page.waitForTimeout(2000);
  const subUsageText = await visibleText(page);
  summary.screenshots.subUsage = await capture(page, 'sub2api-usage.png');
  await browser.close();

  const logs = (summary.api.kiroLogs.body && summary.api.kiroLogs.body.logs) || [];
  const directLog = logs.find((log) => JSON.stringify(log).includes(directRequestId));
  const markerUsageRows = Array.isArray(summary.db.markerUsage) ? summary.db.markerUsage : [];
  const windowUsageRows = Array.isArray(summary.db.windowUsage) ? summary.db.windowUsage : [];
  summary.browser = {
    consoleErrors,
    pageErrors,
    requestFailures,
    textChecks: {
      kiroApiHasClaudeCode: /Claude Code/.test(kiroApiText),
      kiroLogsHasRequestUI: /Request|请求|日志|Logs/i.test(kiroLogsText),
      subDashboardVisible: /Dashboard|仪表|Admin|管理/i.test(subDashboardText),
      subAccountsVisible: /Accounts|账号|kiro|claude|anthropic/i.test(subAccountsText),
      subGroupsVisible: /Groups|分组|claude|openai/i.test(subGroupsText),
      subUsageVisible: /Usage|用量|Tokens|令牌|claude|gpt/i.test(subUsageText),
    },
  };
  summary.evidence = { directLog };
  summary.checks = {
    kiroHealth: summary.api.kiroHealth.ok && summary.api.kiroHealth.body.status === 'ok',
    sub2apiHealth: summary.api.sub2apiHealth.ok && summary.api.sub2apiHealth.body.status === 'ok',
    claudeGroupKeySelected: summary.db.claudeKeyShape && summary.db.claudeKeyShape.group_name === 'claude',
    directMessageOk: summary.api.direct.status === 200 && summary.api.direct.text.includes(directRequestId),
    sub2apiSonnetMessageOk: summary.api.sub2apiSonnetMessage.status === 200 && summary.api.sub2apiSonnetMessage.text.includes(summary.api.sub2apiSonnetMessage.requestId || runId),
    sub2apiOpusMessageOk: summary.api.sub2apiOpusMessage.status === 200 && summary.api.sub2apiOpusMessage.text.includes(summary.api.sub2apiOpusMessage.requestId || runId),
    sub2apiDbUsageRows: windowUsageRows.some((row) => row.requested_model === 'claude-sonnet-4-5-20250929' && row.stream === false) &&
      windowUsageRows.some((row) => row.requested_model === 'claude-opus-4-7' && row.stream === false),
    noWrongGroupKey: summary.db.claudeKeyShape && summary.db.claudeKeyShape.group_name === 'claude',
    browserScreenshotsExist: Object.values(summary.screenshots).every((file) => fs.existsSync(file) && fs.statSync(file).size > 5000),
    browserTextLooksCorrect: Object.values(summary.browser.textChecks).every(Boolean),
    browserNoPageErrors: summary.browser.pageErrors.length === 0,
    browserNoApiRequestFailures: summary.browser.requestFailures.length === 0,
  };
  summary.pass = Object.values(summary.checks).every(Boolean);
  fs.writeFileSync(path.join(outDir, 'summary.json'), JSON.stringify(summary, null, 2));
  if (!summary.pass) process.exitCode = 1;
}

main().catch((err) => {
  fs.writeFileSync(path.join(outDir || '.', 'error.txt'), String(err && err.stack || err));
  process.exit(1);
});
