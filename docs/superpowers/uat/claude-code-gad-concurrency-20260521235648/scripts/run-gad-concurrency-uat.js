const cp = require("child_process");
const fs = require("fs");
const path = require("path");

const root = "/www/Kiro-Go";
const outDir = path.join(root, "docs/superpowers/uat/claude-code-gad-concurrency-20260521235648");
const claudeDir = path.join(outDir, "claude");
const apiDir = path.join(outDir, "api");
const dbDir = path.join(outDir, "db");
const logsDir = path.join(outDir, "logs");

const sub2apiBase = "http://127.0.0.1:18080";
const kiroBase = "http://127.0.0.1:8080";
const model = "claude-opus-4-7";
const claudeAgents = Number(process.env.CLAUDE_AGENTS || 6);
const apiConcurrency = Number(process.env.API_CONCURRENCY || 12);
const apiRequests = Number(process.env.API_REQUESTS || 36);
const requestTimeoutMs = Number(process.env.REQUEST_TIMEOUT_MS || 180000);

for (const dir of [claudeDir, apiDir, dbDir, logsDir]) {
  fs.mkdirSync(dir, { recursive: true });
}

function sh(cmd, options = {}) {
  return cp.execSync(cmd, { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"], ...options }).trim();
}

function pgScalar(sql) {
  const result = cp.spawnSync("docker", [
    "exec",
    "-i",
    "sub2api-postgres",
    "sh",
    "-lc",
    'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atq',
  ], {
    input: sql,
    encoding: "utf8",
    maxBuffer: 10 * 1024 * 1024,
  });
  if (result.status !== 0) {
    throw new Error(`pgScalar failed: ${redact(result.stderr || result.stdout || "")}`);
  }
  return String(result.stdout || "").trim();
}

function redact(text) {
  return String(text)
    .replace(/sk-[A-Za-z0-9_-]+/g, "sk-<redacted>")
    .replace(/(Authorization|ANTHROPIC_AUTH_TOKEN|Bearer)([:= ]+)[^\s"]+/gi, "$1$2<redacted>");
}

function writeJSON(file, value) {
  fs.writeFileSync(file, JSON.stringify(value, null, 2));
}

function parseAnthropicText(bodyText) {
  try {
    const body = JSON.parse(bodyText);
    const content = Array.isArray(body.content) ? body.content : [];
    return content.map((block) => block && block.text || "").join("");
  } catch {
    return "";
  }
}

function parseSSE(bodyText) {
  const events = [];
  const texts = [];
  const errors = [];
  for (const line of bodyText.split(/\r?\n/)) {
    if (line.startsWith("event:")) {
      events.push(line.slice(6).trim());
      continue;
    }
    if (!line.startsWith("data:")) continue;
    const data = line.slice(5).trim();
    if (!data || data === "[DONE]") continue;
    try {
      const obj = JSON.parse(data);
      if (obj.type === "error" || obj.error) errors.push(obj);
      if (obj.delta && typeof obj.delta.text === "string") texts.push(obj.delta.text);
    } catch (err) {
      errors.push({ parse_error: err.message, data: data.slice(0, 200) });
    }
  }
  return { events, text: texts.join(""), errors };
}

async function fetchWithTimeout(url, options) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), requestTimeoutMs);
  try {
    return await fetch(url, { ...options, signal: controller.signal });
  } finally {
    clearTimeout(timer);
  }
}

function runClaudeAgent(index, apiKey, runId) {
  return new Promise((resolve) => {
    const prompt = [
      `GAD parallel agent ${index}.`,
      "Simulate a small development workflow: inspect, reason, verify.",
      `Return exactly: gad-ok-${index}`,
    ].join(" ");
    const startedAt = Date.now();
    const child = cp.spawn("claude", [
      "-p",
      prompt,
      "--model",
      model,
      "--output-format",
      "json",
    ], {
      cwd: root,
      env: {
        ...process.env,
        ANTHROPIC_BASE_URL: sub2apiBase,
        ANTHROPIC_AUTH_TOKEN: apiKey,
        ANTHROPIC_MODEL: model,
        CLAUDE_CODE_MAX_RETRIES: "1",
        API_TIMEOUT_MS: String(requestTimeoutMs),
        GAD_AGENT_INDEX: String(index),
        GAD_RUN_ID: runId,
      },
    });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => stdout += chunk);
    child.stderr.on("data", (chunk) => stderr += chunk);
    child.on("close", (code) => {
      const durationMs = Date.now() - startedAt;
      const rawFile = path.join(claudeDir, `agent-${index}.raw.json`);
      const errFile = path.join(claudeDir, `agent-${index}.stderr.log`);
      fs.writeFileSync(rawFile, redact(stdout));
      fs.writeFileSync(errFile, redact(stderr));
      let parsed = null;
      try { parsed = JSON.parse(stdout); } catch {}
      const resultText = parsed && typeof parsed.result === "string" ? parsed.result : stdout;
      resolve({
        index,
        exitCode: code,
        durationMs,
        parsed: Boolean(parsed),
        isError: parsed ? parsed.is_error === true : code !== 0,
        apiErrorStatus: parsed ? parsed.api_error_status : null,
        stopReason: parsed ? parsed.stop_reason : null,
        result: String(resultText || "").slice(0, 500),
        expected: `gad-ok-${index}`,
        ok: code === 0 && String(resultText || "").includes(`gad-ok-${index}`),
        overloaded: /overloaded_error|529|capacity|temporarily unavailable/i.test(stdout + stderr),
        stderrHead: redact(stderr).slice(0, 500),
      });
    });
  });
}

async function runApiProbe(index, apiKey, runId, stream) {
  const startedAt = Date.now();
  const expected = `api-ok-${index}`;
  const headers = {
    "Authorization": `Bearer ${apiKey}`,
    "Content-Type": "application/json",
    "anthropic-version": "2023-06-01",
    "User-Agent": `gad-concurrency-uat/${runId}`,
    "x-client-request-id": `${runId}-api-${index}-${stream ? "stream" : "sync"}`,
  };
  const payload = {
    model,
    max_tokens: 24,
    stream,
    messages: [{ role: "user", content: `High concurrency API probe ${index}. Reply exactly: ${expected}` }],
  };
  try {
    const res = await fetchWithTimeout(`${sub2apiBase}/v1/messages`, {
      method: "POST",
      headers,
      body: JSON.stringify(payload),
    });
    const body = await res.text();
    const durationMs = Date.now() - startedAt;
    const fileBase = path.join(apiDir, `request-${String(index).padStart(3, "0")}-${stream ? "stream" : "sync"}`);
    fs.writeFileSync(`${fileBase}.body`, redact(body));
    const parsed = stream ? parseSSE(body) : { text: parseAnthropicText(body), events: [], errors: [] };
    const ok = res.status === 200 && parsed.text.includes(expected) && parsed.errors.length === 0;
    return {
      index,
      stream,
      status: res.status,
      durationMs,
      text: parsed.text.slice(0, 200),
      expected,
      ok,
      errorEvent: parsed.errors.length > 0,
      events: stream ? parsed.events : undefined,
      overloaded: /overloaded_error|529|capacity|temporarily unavailable/i.test(body),
      bodyHead: redact(body).slice(0, 500),
    };
  } catch (err) {
    return {
      index,
      stream,
      status: 0,
      durationMs: Date.now() - startedAt,
      text: "",
      expected,
      ok: false,
      error: err.message,
      overloaded: /overloaded_error|529|capacity|temporarily unavailable/i.test(err.message),
    };
  }
}

async function runQueue(items, concurrency, worker) {
  const results = new Array(items.length);
  let cursor = 0;
  async function next() {
    for (;;) {
      const current = cursor++;
      if (current >= items.length) return;
      results[current] = await worker(items[current], current);
    }
  }
  await Promise.all(Array.from({ length: concurrency }, next));
  return results;
}

function percentile(values, p) {
  if (values.length === 0) return 0;
  const sorted = [...values].sort((a, b) => a - b);
  const idx = Math.min(sorted.length - 1, Math.ceil((p / 100) * sorted.length) - 1);
  return sorted[idx];
}

async function main() {
  const runId = `gad-${new Date().toISOString().replace(/[-:.TZ]/g, "").slice(0, 14)}`;
  const startedAt = new Date();
  const startedIso = startedAt.toISOString();
  fs.writeFileSync(path.join(outDir, "run-id.txt"), `${runId}\n`);
  fs.writeFileSync(path.join(outDir, "started-at.txt"), `${startedIso}\n`);

  const apiKey = pgScalar("select key from api_keys where id=2 and status='active'");
  if (!apiKey) throw new Error("sub2api api key id=2 is not active");

  const health = {
    kiro: await (await fetch(`${kiroBase}/health`)).json(),
    sub2api: await (await fetch(`${sub2apiBase}/health`)).json(),
    docker: sh("docker ps --filter name=kiro-go-kiro-go-1 --filter name=sub2api --filter name=sub2api-postgres --filter name=sub2api-redis --format 'table {{.Names}}\\t{{.Status}}\\t{{.Ports}}'"),
  };
  writeJSON(path.join(logsDir, "pre-health.json"), health);

  const claudeResults = await Promise.all(
    Array.from({ length: claudeAgents }, (_, i) => runClaudeAgent(i + 1, apiKey, runId))
  );
  writeJSON(path.join(claudeDir, "claude-agents-summary.json"), claudeResults);

  const apiItems = Array.from({ length: apiRequests }, (_, i) => ({
    index: i + 1,
    stream: i % 2 === 1,
  }));
  const apiResults = await runQueue(apiItems, apiConcurrency, (item) => runApiProbe(item.index, apiKey, runId, item.stream));
  writeJSON(path.join(apiDir, "api-concurrency-summary.json"), apiResults);

  await new Promise((resolve) => setTimeout(resolve, 1500));

  const dbSql = `select json_build_object(
    'usageForRun', (
      select json_agg(row_to_json(t)) from (
        select id, created_at, api_key_id, account_id, group_id, model, requested_model, upstream_model,
               stream, duration_ms, input_tokens, output_tokens, user_agent
        from usage_logs
        where created_at >= '${startedIso}'::timestamptz
          and api_key_id = 2
        order by created_at desc
        limit 200
      ) t
    ),
    'errorsForRun', (
      select json_agg(row_to_json(t)) from (
        select id, created_at, client_request_id, api_key_id, account_id, group_id, model, requested_model,
               stream, status_code, upstream_status_code, error_type, provider_error_type,
               retry_after_seconds, left(coalesce(error_message,''), 300) as error_message
        from ops_error_logs
        where created_at >= '${startedIso}'::timestamptz
          and api_key_id = 2
        order by created_at desc
        limit 200
      ) t
    )
  );`;
  const dbEvidence = JSON.parse(pgScalar(dbSql));
  writeJSON(path.join(dbDir, "sub2api-db-evidence.json"), dbEvidence);

  const postHealth = {
    kiro: await (await fetch(`${kiroBase}/health`)).json(),
    sub2api: await (await fetch(`${sub2apiBase}/health`)).json(),
    docker: sh("docker ps --filter name=kiro-go-kiro-go-1 --filter name=sub2api --filter name=sub2api-postgres --filter name=sub2api-redis --format 'table {{.Names}}\\t{{.Status}}\\t{{.Ports}}'"),
  };
  writeJSON(path.join(logsDir, "post-health.json"), postHealth);

  const apiDurations = apiResults.map((r) => r.durationMs).filter(Number.isFinite);
  const claudeDurations = claudeResults.map((r) => r.durationMs).filter(Number.isFinite);
  const usage = dbEvidence.usageForRun || [];
  const errors = dbEvidence.errorsForRun || [];
  const summary = {
    runId,
    startedAt: startedIso,
    finishedAt: new Date().toISOString(),
    model,
    claudeAgents,
    apiRequests,
    apiConcurrency,
    claude: {
      total: claudeResults.length,
      ok: claudeResults.filter((r) => r.ok).length,
      failed: claudeResults.filter((r) => !r.ok).length,
      overloaded: claudeResults.filter((r) => r.overloaded).length,
      p50Ms: percentile(claudeDurations, 50),
      p95Ms: percentile(claudeDurations, 95),
      maxMs: Math.max(0, ...claudeDurations),
    },
    api: {
      total: apiResults.length,
      ok: apiResults.filter((r) => r.ok).length,
      failed: apiResults.filter((r) => !r.ok).length,
      overloaded: apiResults.filter((r) => r.overloaded).length,
      statuses: apiResults.reduce((acc, r) => {
        acc[r.status] = (acc[r.status] || 0) + 1;
        return acc;
      }, {}),
      p50Ms: percentile(apiDurations, 50),
      p95Ms: percentile(apiDurations, 95),
      maxMs: Math.max(0, ...apiDurations),
    },
    db: {
      usageRowsForApiKey2: usage.length,
      errorRowsForApiKey2: errors.length,
      claudeCliRows: usage.filter((row) => /claude-cli\/2\.1\.143/.test(row.user_agent || "")).length,
      gadApiRows: usage.filter((row) => /gad-concurrency-uat/.test(row.user_agent || "")).length,
    },
    pass: claudeResults.every((r) => r.ok) &&
      apiResults.every((r) => r.ok) &&
      usage.length >= claudeAgents + apiRequests &&
      errors.length === 0 &&
      postHealth.kiro.status === "ok" &&
      postHealth.sub2api.status === "ok",
  };
  writeJSON(path.join(outDir, "summary.json"), summary);
  console.log(JSON.stringify(summary, null, 2));
  process.exit(summary.pass ? 0 : 1);
}

main().catch((err) => {
  const error = { message: err.message, stack: err.stack };
  writeJSON(path.join(outDir, "fatal-error.json"), error);
  console.error(err);
  process.exit(1);
});
