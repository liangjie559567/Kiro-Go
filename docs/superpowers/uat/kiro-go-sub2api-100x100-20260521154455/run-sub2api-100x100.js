const fs = require("fs");
const path = require("path");
const readline = require("readline");

const outDir = __dirname;
const baseURL = process.env.SUB2API_BASE_URL || "http://127.0.0.1:18080";
const kiroAdminURL = process.env.KIRO_ADMIN_URL || "http://127.0.0.1:8080";
const model = process.env.MODEL || "claude-opus-4-7";
const perMode = Number(process.env.PER_MODE || "100");
const requestedConcurrency = Number(process.env.CONCURRENCY || "10");
const fixedConcurrency = process.env.FIXED_CONCURRENCY === "1";
const timeoutMs = Number(process.env.REQUEST_TIMEOUT_MS || "120000");

function ensureDirs() {
  for (const dir of ["request-results", "headers", "api", "logs"]) {
    fs.mkdirSync(path.join(outDir, dir), { recursive: true });
  }
}

function readSecretFromStdin() {
  return new Promise((resolve) => {
    const rl = readline.createInterface({ input: process.stdin, terminal: false });
    rl.once("line", (line) => {
      rl.close();
      resolve(line.trim());
    });
  });
}

function safeHeaders(headers) {
  const keep = [
    "content-type",
    "x-request-id",
    "retry-after",
    "x-kiro-go-error-reason",
    "x-kiro-go-retryable",
    "x-kiro-go-circuit-state",
    "x-kiro-go-safe-concurrency",
    "x-kiro-go-stable-fallback",
    "x-kiro-go-internal-reason",
  ];
  const out = {};
  for (const key of keep) {
    const value = headers.get(key);
    if (value !== null) out[key] = value;
  }
  return out;
}

function parseAnthropicText(body) {
  if (Array.isArray(body.content)) {
    return body.content.map((block) => block && (block.text || "")).join("\n");
  }
  if (body.error && body.error.message) return body.error.message;
  if (body.message && typeof body.message === "string") return body.message;
  return "";
}

function parseStream(raw) {
  let output = "";
  let eventCount = 0;
  let messageStart = false;
  let messageStop = false;
  let errorEvent = "";
  let replayAfterContent = false;
  let sawContent = false;

  for (const block of raw.split(/\r?\n\r?\n/)) {
    if (!block.trim()) continue;
    eventCount += 1;
    const lines = block.split(/\r?\n/);
    const dataLine = lines.find((line) => line.startsWith("data:"));
    if (!dataLine) continue;
    const data = dataLine.slice("data:".length).trim();
    if (!data || data === "[DONE]") continue;
    let parsed;
    try {
      parsed = JSON.parse(data);
    } catch (err) {
      errorEvent = `invalid_sse_json: ${err.message}`;
      continue;
    }
    if (parsed.type === "message_start") messageStart = true;
    if (parsed.type === "message_stop") messageStop = true;
    if (parsed.type === "content_block_delta" && parsed.delta && parsed.delta.text) {
      output += parsed.delta.text;
      sawContent = true;
    }
    if (parsed.type === "content_block_start" && parsed.content_block && parsed.content_block.text) {
      output += parsed.content_block.text;
      sawContent = true;
    }
    if (parsed.type === "message_start" && sawContent) replayAfterContent = true;
    if (parsed.type === "error") {
      errorEvent = parsed.error && parsed.error.message ? parsed.error.message : JSON.stringify(parsed.error || parsed).slice(0, 300);
    }
  }

  return { output, eventCount, messageStart, messageStop, errorEvent, replayAfterContent };
}

async function readKiroReadiness() {
  const encodedModel = encodeURIComponent(model);
  const candidates = [
    `${kiroAdminURL}/admin/api/fleet/readiness?model=${encodedModel}`,
    `${kiroAdminURL}/admin/api/model-readiness?model=${encodedModel}`,
  ];
  for (const url of candidates) {
    try {
      const headers = { "User-Agent": "kiro-go-phase7-sub2api-100x100/1.0" };
      if (process.env.KIRO_ADMIN_PASSWORD) {
        headers["X-Admin-Password"] = process.env.KIRO_ADMIN_PASSWORD;
      }
      const res = await fetch(url, { headers });
      const raw = await res.text();
      let body;
      try {
        body = JSON.parse(raw);
      } catch (err) {
        body = { parseError: err.message, rawPreview: raw.slice(0, 300) };
      }
      if (res.ok && body && !body.parseError) {
        return { ok: true, url, httpStatus: res.status, body };
      }
      return { ok: false, url, httpStatus: res.status, body };
    } catch (err) {
      if (url === candidates[candidates.length - 1]) {
        return { ok: false, url, httpStatus: 0, error: String(err.message || err).slice(0, 300) };
      }
    }
  }
  return { ok: false, httpStatus: 0, error: "no readiness endpoint tried" };
}

function resolveConcurrency(readiness) {
  const body = readiness && readiness.body ? readiness.body : {};
  const observedSafe = Number(body.safeConcurrency || body.admissionEffectiveConcurrency || 0);
  const circuitState = String(body.circuitState || "");
  const status = String(body.status || "");
  const retryAfterSeconds = Number(body.retryAfterSeconds || 0);
  const reasonCodes = Array.isArray(body.reasonCodes) ? body.reasonCodes : [];
  const maxByReadiness = observedSafe > 0 ? observedSafe : 1;
  const effectiveConcurrency = fixedConcurrency ? requestedConcurrency : Math.max(1, Math.min(requestedConcurrency, maxByReadiness));
  const blocked = circuitState === "open" && retryAfterSeconds > 0;
  return {
    requestedConcurrency,
    effectiveConcurrency,
    fixedConcurrency,
    safeConcurrency: observedSafe,
    readinessStatus: status,
    circuitState,
    retryAfterSeconds,
    reasonCodes,
    blocked,
    decision: blocked
      ? "blocked_by_readiness"
      : fixedConcurrency
        ? "fixed_concurrency_requested"
        : effectiveConcurrency < requestedConcurrency
          ? "reduced_to_safe_concurrency"
          : "using_requested_concurrency",
  };
}

async function callOnce(apiKey, mode, index) {
  const stream = mode === "stream";
  const marker = `KIRO_GO_SUB2API_UAT_${mode.toUpperCase()}_${String(index).padStart(3, "0")}`;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  const startedAt = Date.now();

  try {
    const res = await fetch(`${baseURL}/v1/messages`, {
      method: "POST",
      signal: controller.signal,
      headers: {
        Authorization: `Bearer ${apiKey}`,
        "Content-Type": "application/json",
        "anthropic-version": "2023-06-01",
        "User-Agent": "kiro-go-phase7-sub2api-100x100/1.0",
        "X-Sub2API-Request": "kiro-go-phase7-100x100",
      },
      body: JSON.stringify({
        model,
        max_tokens: 32,
        stream,
        messages: [{ role: "user", content: `Reply exactly: ${marker}` }],
      }),
    });

    const raw = await res.text();
    const durationMs = Date.now() - startedAt;
    const headers = safeHeaders(res.headers);

    if (stream) {
      const parsed = parseStream(raw);
      const contentNonEmpty = parsed.output.trim().length > 0;
      const markerPresent = parsed.output.includes(marker);
      const ok = res.status === 200 && contentNonEmpty && markerPresent && !parsed.errorEvent && parsed.messageStart && parsed.messageStop && !parsed.replayAfterContent;
      return {
        index,
        mode,
        ok,
        httpStatus: res.status,
        durationMs,
        safeHeaders: headers,
        eventCount: parsed.eventCount,
        messageStart: parsed.messageStart,
        messageStop: parsed.messageStop,
        contentNonEmpty,
        markerPresent,
        replayAfterContent: parsed.replayAfterContent,
        errorEvent: parsed.errorEvent,
        outputPreview: parsed.output.slice(0, 160),
      };
    }

    let body;
    try {
      body = JSON.parse(raw);
    } catch (err) {
      body = { parseError: err.message };
    }
    const output = parseAnthropicText(body);
    const contentNonEmpty = output.trim().length > 0;
    const markerPresent = output.includes(marker);
    const ok = res.status === 200 && body.type === "message" && contentNonEmpty && markerPresent && !body.error;
    return {
      index,
      mode,
      ok,
      httpStatus: res.status,
      durationMs,
      safeHeaders: headers,
      bodyType: body.type || null,
      responseModel: body.model || null,
      usage: body.usage || null,
      contentNonEmpty,
      markerPresent,
      errorType: body.error && body.error.type ? body.error.type : null,
      errorMessage: body.error && body.error.message ? body.error.message.slice(0, 300) : null,
      outputPreview: output.slice(0, 160),
    };
  } catch (err) {
    return {
      index,
      mode,
      ok: false,
      httpStatus: 0,
      durationMs: Date.now() - startedAt,
      errorType: err.name || "Error",
      errorMessage: String(err.message || err).slice(0, 300),
    };
  } finally {
    clearTimeout(timer);
  }
}

async function runMode(apiKey, mode, count, concurrency) {
  const file = path.join(outDir, "request-results", `${mode}.jsonl`);
  fs.writeFileSync(file, "");
  const results = new Array(count);
  let next = 0;
  let completed = 0;

  async function worker() {
    for (;;) {
      const index = next++;
      if (index >= count) return;
      const result = await callOnce(apiKey, mode, index);
      results[index] = result;
      fs.appendFileSync(file, JSON.stringify(result) + "\n");
      completed += 1;
      if (completed % 10 === 0 || !result.ok) {
        const passed = results.filter(Boolean).filter((item) => item.ok).length;
        console.log(JSON.stringify({ mode, completed, passed, failed: completed - passed, latestStatus: result.httpStatus, latestOk: result.ok }));
      }
    }
  }

  await Promise.all(Array.from({ length: concurrency }, worker));
  return results;
}

function summarize(results) {
  const total = results.length;
  const passed = results.filter((result) => result.ok).length;
  const failed = total - passed;
  return {
    total,
    passed,
    failed,
    http200: results.filter((result) => result.httpStatus === 200).length,
    realContent: results.filter((result) => result.contentNonEmpty).length,
    markerPresent: results.filter((result) => result.markerPresent).length,
    stableFallbackHeaders: results.filter((result) => result.safeHeaders && result.safeHeaders["x-kiro-go-stable-fallback"]).length,
    replayAfterContent: results.filter((result) => result.replayAfterContent).length,
    statusCounts: results.reduce((acc, result) => {
      const key = String(result.httpStatus);
      acc[key] = (acc[key] || 0) + 1;
      return acc;
    }, {}),
    firstFailures: results.filter((result) => !result.ok).slice(0, 10),
  };
}

async function main() {
  ensureDirs();
  const apiKey = await readSecretFromStdin();
  if (!apiKey) {
    console.error("API key is required on stdin");
    process.exit(2);
  }

  const startedAt = new Date().toISOString();
  const readiness = await readKiroReadiness();
  const concurrencyDecision = resolveConcurrency(readiness);
  fs.writeFileSync(path.join(outDir, "api", "readiness-before-run.json"), JSON.stringify({ startedAt, readiness, concurrencyDecision }, null, 2));
  if (concurrencyDecision.blocked) {
    const summary = {
      startedAt,
      finishedAt: new Date().toISOString(),
      baseURL,
      model,
      perMode,
      requestedConcurrency,
      concurrency: concurrencyDecision.effectiveConcurrency,
      readiness,
      concurrencyDecision,
      verdict: "blocked_by_readiness",
    };
    fs.writeFileSync(path.join(outDir, "api", "summary.json"), JSON.stringify(summary, null, 2));
    console.log(JSON.stringify(summary, null, 2));
    process.exit(1);
  }
  const concurrency = concurrencyDecision.effectiveConcurrency;
  const precheck = [await callOnce(apiKey, "non-stream", 999), await callOnce(apiKey, "stream", 999)];
  fs.writeFileSync(path.join(outDir, "api", "precheck.json"), JSON.stringify({ startedAt, finishedAt: new Date().toISOString(), concurrencyDecision, precheck }, null, 2));
  if (precheck.some((result) => !result.ok)) {
    fs.writeFileSync(path.join(outDir, "api", "summary.json"), JSON.stringify({ startedAt, finishedAt: new Date().toISOString(), verdict: "precheck_failed", readiness, concurrencyDecision, precheck }, null, 2));
    console.log(JSON.stringify({ verdict: "precheck_failed", concurrencyDecision, precheck }, null, 2));
    process.exit(1);
  }

  console.log(JSON.stringify({ verdict: "precheck_passed", startedAt, perMode, concurrency, concurrencyDecision }));
  const nonStream = await runMode(apiKey, "non-stream", perMode, concurrency);
  const stream = await runMode(apiKey, "stream", perMode, concurrency);
  const summary = {
    startedAt,
    finishedAt: new Date().toISOString(),
    baseURL,
    model,
    perMode,
    requestedConcurrency,
    concurrency,
    readiness,
    concurrencyDecision,
    nonStream: summarize(nonStream),
    stream: summarize(stream),
  };
  summary.verdict = summary.nonStream.passed === perMode && summary.stream.passed === perMode ? "pass" : "fail";
  fs.writeFileSync(path.join(outDir, "api", "summary.json"), JSON.stringify(summary, null, 2));
  console.log(JSON.stringify(summary, null, 2));
  process.exit(summary.verdict === "pass" ? 0 : 1);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
