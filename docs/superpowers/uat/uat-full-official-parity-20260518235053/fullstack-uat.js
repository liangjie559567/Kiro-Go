const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const outDir = process.argv[2];
if (!outDir) throw new Error('usage: node fullstack-uat.js <outDir>');

(async () => {
  const launchOptions = { headless: true };
  if (fs.existsSync('/usr/bin/google-chrome')) {
    launchOptions.executablePath = '/usr/bin/google-chrome';
  }
  const browser = await chromium.launch(launchOptions);
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  const errors = [];
  page.on('console', msg => {
    if (msg.type() === 'error') errors.push({ type: 'console', text: msg.text() });
  });
  page.on('pageerror', err => errors.push({ type: 'pageerror', text: String(err) }));

  const shots = [];
  async function shot(name) {
    const file = path.join(outDir, name + '.png');
    await page.screenshot({ path: file, fullPage: true });
    shots.push(file);
  }

  await page.goto('http://127.0.0.1:8080/admin', { waitUntil: 'networkidle' });
  await shot('kiro-admin-login-or-dashboard');

  await page.goto('http://127.0.0.1:8080/admin/api/claude-code/readiness');
  await shot('kiro-claude-readiness-json');

  await page.goto('http://127.0.0.1:8080/admin/api/claude-code/model-readiness');
  await shot('kiro-model-readiness-json');

  await page.goto('http://127.0.0.1:18080/health', { waitUntil: 'networkidle' });
  await shot('sub2api-health-json');

  await page.goto('http://127.0.0.1:18080', { waitUntil: 'networkidle' });
  await shot('sub2api-root');

  const summary = {
    ok: errors.length === 0,
    errors,
    screenshots: shots,
    checkedAt: new Date().toISOString()
  };
  fs.writeFileSync(path.join(outDir, 'playwright-summary.json'), JSON.stringify(summary, null, 2));
  await browser.close();
})().catch(err => {
  console.error(err);
  process.exit(1);
});
