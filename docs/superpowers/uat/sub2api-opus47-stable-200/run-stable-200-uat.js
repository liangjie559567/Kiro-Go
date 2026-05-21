const fs = require("fs");
const path = require("path");

const baseURL = envURL("SUB2API_BASE_URL", "http://127.0.0.1:18080");
const apiKey = process.env.SUB2API_API_KEY;
const model = process.env.MODEL || "claude-opus-4.7";
const defaultTotal = nonNegativeInt(process.env.ROUNDS, 100);
const nonStreamTotal = nonNegativeInt(process.env.NON_STREAM_TOTAL, defaultTotal);
const streamTotal = nonNegativeInt(process.env.STREAM_TOTAL, defaultTotal);
const requestedConcurrency = positiveInt(process.env.CONCURRENCY, 4);
const kiroBaseURL = envURL("KIRO_GO_BASE_URL", "http://127.0.0.1:8080");
const kiroAdminPassword = process.env.KIRO_GO_ADMIN_PASSWORD || "";
const readinessPath = process.env.KIRO_GO_READINESS_PATH || "/admin/api/fleet/readiness";
const readinessFile = process.env.KIRO_GO_READINESS_FILE || "";
const readinessWaitSeconds = positiveInt(process.env.READINESS_WAIT_SECONDS, 600);
const readinessPollSeconds = positiveInt(process.env.READINESS_POLL_SECONDS, 5);
const requestTimeoutSeconds = positiveInt(process.env.REQUEST_TIMEOUT_SECONDS, 180);
const abortOnCapacityFailure = process.env.ABORT_ON_CAPACITY_FAILURE !== "false";
const rampUp = process.env.RAMP_UP !== "false";
const requestSpacingMs = positiveInt(process.env.REQUEST_SPACING_MS, 0);
const readinessCheckEvery = positiveInt(process.env.READINESS_CHECK_EVERY, 10);
const capacityRecoveryWaitSeconds = positiveInt(process.env.CAPACITY_RECOVERY_WAIT_SECONDS, 120);
const resumeAfterReadinessBlock = process.env.RESUME_AFTER_READINESS_BLOCK === "true";
const outputDir = process.env.UAT_OUTPUT_DIR || path.join("docs", "superpowers", "uat", "sub2api-opus47-stable-200", `run-${timestamp()}`);

if (!apiKey) {
  console.error("SUB2API_API_KEY is required");
  process.exit(2);
}

function envURL(name, fallback) {
  const value = process.env[name] || fallback;
  return value.replace(/\/+$/, "");
}

function positiveInt(value, fallback) {
  const parsed = Number(value || fallback);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return fallback;
  }
  return Math.floor(parsed);
}

function nonNegativeInt(value, fallback) {
  const parsed = Number(value ?? fallback);
  if (!Number.isFinite(parsed) || parsed < 0) {
    return fallback;
  }
  return Math.floor(parsed);
}

function timestamp() {
  return new Date().toISOString().replace(/[-:.TZ]/g, "").slice(0, 14);
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function fetchWithTimeout(url, options, timeoutSeconds) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutSeconds * 1000);
  try {
    return await fetch(url, { ...options, signal: controller.signal });
  } finally {
    clearTimeout(timer);
  }
}

function capacityFailureReason(status, text) {
  if ([429, 502, 503].includes(status)) return `http_${status}`;
  const patterns = [
    ["http_429", /HTTP 429/i],
    ["http_502", /HTTP 502/i],
    ["http_503", /HTTP 503/i],
    ["stable_fallback", /kiro_go_stable_fallback/i],
    ["stable_waiting", /Opus 4\.7 is temporarily waiting/i],
    ["upstream_capacity", /upstream capacity is temporarily unavailable/i],
    ["insufficient_model_capacity", /INSUFFICIENT_MODEL_CAPACITY/i],
    ["admission_pressure", /admission_pressure/i],
    ["opus47_budget_exhausted", /opus47_budget_exhausted/i],
  ];
  for (const [reason, pattern] of patterns) {
    if (pattern.test(text)) return reason;
  }
  return "";
}

function hasForbiddenStatusMarker(status, text) {
  return capacityFailureReason(status, text) !== "";
}

function hasAnthropicContent(text, stream) {
  if (stream) {
    const { events, error } = parseSSEEvents(text);
    if (error) return false;
    return events.some((event) => {
      if (event && event.type === "content_block_delta" && event.delta) {
        return typeof event.delta.text === "string" && event.delta.text.trim().length > 0 ||
          typeof event.delta.partial_json === "string" && event.delta.partial_json.trim().length > 0 ||
          typeof event.delta.thinking === "string" && event.delta.thinking.trim().length > 0;
      }
      return Array.isArray(event && event.choices) && openAIChoiceTexts(event).some((value) => value.trim().length > 0);
    });
  }
  try {
    const body = JSON.parse(text);
    const content = Array.isArray(body.content) ? body.content : [];
    const hasAnthropicBlock = content.some((block) => {
      if (!block || typeof block !== "object") return false;
      if (block.type === "text") return typeof block.text === "string" && block.text.trim().length > 0;
      if (block.type === "tool_use") return true;
      return false;
    });
    if (hasAnthropicBlock) return true;
    return openAIChoiceTexts(body).some((value) => value.trim().length > 0);
  } catch {
    return false;
  }
}

function expectedMarker(index) {
  return `ok ${index}`;
}

function anthropicTextBlocks(body) {
  const blocks = Array.isArray(body && body.content) ? body.content : [];
  return blocks
    .filter((block) => block && block.type === "text" && typeof block.text === "string")
    .map((block) => block.text);
}

function openAIChoiceTexts(body) {
  const choices = Array.isArray(body && body.choices) ? body.choices : [];
  return choices.flatMap((choice) => {
    const texts = [];
    if (choice && choice.message && typeof choice.message.content === "string") {
      texts.push(choice.message.content);
    }
    if (choice && choice.delta && typeof choice.delta.content === "string") {
      texts.push(choice.delta.content);
    }
    if (typeof (choice && choice.text) === "string") {
      texts.push(choice.text);
    }
    return texts;
  });
}

function hasExpectedJSONMarker(text, marker) {
  try {
    const body = JSON.parse(text);
    const content = anthropicTextBlocks(body).concat(openAIChoiceTexts(body)).join("");
    return content.includes(marker);
  } catch {
    return false;
  }
}

function hasExpectedSSEMarker(text, marker) {
  const { events, error } = parseSSEEvents(text);
  if (error) return false;
  const content = events.map((event) => {
    if (event && event.type === "content_block_delta" && event.delta) {
      if (typeof event.delta.text === "string") return event.delta.text;
      if (typeof event.delta.partial_json === "string") return event.delta.partial_json;
      if (typeof event.delta.thinking === "string") return event.delta.thinking;
    }
    if (event && Array.isArray(event.choices)) {
      return openAIChoiceTexts(event).join("");
    }
    return "";
  }).join("");
  return content.includes(marker);
}

function hasExpectedMarker(text, stream, index) {
  const marker = expectedMarker(index);
  return stream ? hasExpectedSSEMarker(text, marker) : hasExpectedJSONMarker(text, marker);
}

function validateJSONBody(text) {
  try {
    const parsed = JSON.parse(text);
    if (!parsed || typeof parsed !== "object") {
      return "json body is not an object";
    }
    if (parsed.type === "error" || parsed.error) {
      return "json body is an error envelope";
    }
    if (parsed.type === "message" && Array.isArray(parsed.content)) {
      return "";
    }
    if (Array.isArray(parsed.choices)) {
      return "";
    }
    return "json body does not look like Anthropic or OpenAI success";
  } catch (err) {
    return `invalid json: ${err.message}`;
  }
}

function parseSSEEvents(text) {
  const events = [];
  for (const line of text.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed.startsWith("data:")) continue;
    const payload = trimmed.slice("data:".length).trim();
    if (!payload || payload === "[DONE]") continue;
    try {
      events.push(JSON.parse(payload));
    } catch (err) {
      return { events, error: `invalid sse json: ${err.message}` };
    }
  }
  return { events, error: "" };
}

function validateSSEBody(text) {
  const { events, error } = parseSSEEvents(text);
  if (error) return error;
  if (events.length === 0) return "sse body has no data lines";
  if (events.some((event) => event.type === "error" || event.error)) {
    return "sse body contains error envelope";
  }
  if (!events.some((event) => event.type === "message_start") || !events.some((event) => event.type === "message_stop")) {
    return "sse body missing message_start or message_stop";
  }
  if (!hasAnthropicContent(text, true)) {
    return "sse body has no real content delta";
  }
  return "";
}

function validateBody(text, stream) {
  return stream ? validateSSEBody(text) : validateJSONBody(text);
}

async function getReadiness() {
  if (readinessFile) {
    try {
      const raw = JSON.parse(fs.readFileSync(readinessFile, "utf8"));
      const body = raw.body || raw;
      return { statusCode: raw.statusCode || 200, body };
    } catch (err) {
      return {
        statusCode: 0,
        body: {
          status: "blocked",
          safeConcurrency: 0,
          retryAfterSeconds: readinessPollSeconds,
          reasonCodes: [`readiness_file_error:${err.message}`],
        },
      };
    }
  }
	const url = new URL(readinessPath, kiroBaseURL);
	url.searchParams.set("model", model);
	const headers = kiroAdminPassword ? { "X-Admin-Password": kiroAdminPassword } : {};
	const res = await fetchWithTimeout(url.toString(), { headers }, 10);
	if (!res.ok) {
		return {
			statusCode: res.status,
			body: {
				status: "blocked",
				safeConcurrency: 0,
				retryAfterSeconds: readinessPollSeconds,
				reasonCodes: [`readiness_http_${res.status}`],
			},
		};
	}
	const body = await res.json();
	return { statusCode: res.status, body };
}

async function waitForReadiness() {
  const deadline = Date.now() + readinessWaitSeconds * 1000;
  let latest = null;
  for (;;) {
    latest = await getReadiness();
    const body = latest.body || {};
    const status = String(body.status || "");
    const safeConcurrency = Number(body.safeConcurrency || 0);
    if ((status === "healthy" || status === "degraded") && safeConcurrency > 0) {
      return latest;
    }
    if (Date.now() >= deadline) {
      return latest;
    }
    const retryAfter = Number(body.retryAfterSeconds || 0);
    const waitSeconds = Math.min(Math.max(retryAfter || readinessPollSeconds, readinessPollSeconds), 30);
    await sleep(waitSeconds * 1000);
  }
}

function readinessAllowsTraffic(readiness) {
  const body = readiness.body || {};
  const status = String(body.status || "");
  const safeConcurrency = Number(body.safeConcurrency || 0);
  return ["healthy", "degraded"].includes(status) && safeConcurrency > 0;
}

function readinessSafeConcurrency(readiness) {
  return Math.max(0, Number((readiness.body || {}).safeConcurrency || 0));
}

function readinessSummary(body) {
  body = body || {};
  return {
    status: body.status || "",
    safeConcurrency: Number(body.safeConcurrency || 0),
    retryAfterSeconds: body.retryAfterSeconds || 0,
    reasonCodes: body.reasonCodes || [],
    lastPressureReason: body.lastPressureReason || "",
    recentContentRequests: body.recentContentRequests || 0,
    contentSuccessRate: body.contentSuccessRate,
    recentStableFallbacks: body.recentStableFallbacks || 0,
    recentEmptyCompletions: body.recentEmptyCompletions || 0,
    summary: body.summary || undefined,
  };
}

async function waitForCapacityRecovery(writer, reason) {
  const deadline = Date.now() + capacityRecoveryWaitSeconds * 1000;
  let latest = null;
  for (;;) {
    latest = await getReadiness();
    const body = readinessSummary(latest.body || {});
    writer.write(`${JSON.stringify({
      type: "capacity_recovery_probe",
      reason,
      ...body,
      ts: new Date().toISOString(),
    })}\n`);
    if (readinessAllowsTraffic(latest)) {
      return latest;
    }
    if (Date.now() >= deadline) {
      return latest;
    }
    const retryAfter = Number(body.retryAfterSeconds || 0);
    const waitSeconds = Math.min(Math.max(retryAfter || readinessPollSeconds, readinessPollSeconds), 30);
    await sleep(waitSeconds * 1000);
  }
}

async function callOnce(index, stream) {
  const startedAt = Date.now();
  let status = 0;
  let text = "";
  let error = "";
  try {
    const res = await fetchWithTimeout(`${baseURL}/v1/messages`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${apiKey}`,
        "Content-Type": "application/json",
        "User-Agent": "sub2api-stable-200-uat/2.0 claude-cli/2.1.143",
        "X-Sub2API-Request": "uat",
      },
      body: JSON.stringify({
        model,
        max_tokens: 64,
        stream,
        messages: [
          {
            role: "user",
            content: `stable 200 uat ${stream ? "stream" : "non-stream"} request ${index}; reply with exactly: ok ${index}`,
          },
        ],
      }),
    }, requestTimeoutSeconds);
    status = res.status;
    text = await res.text();
  } catch (err) {
    error = err && err.name === "AbortError" ? "request_timeout" : String(err && err.message ? err.message : err);
  }

  const forbidden = hasForbiddenStatusMarker(status, text);
  const capacityReason = capacityFailureReason(status, text);
  const bodyError = text ? validateBody(text, stream) : "empty body";
  const contentOk = text ? hasAnthropicContent(text, stream) : false;
  const markerOk = text ? hasExpectedMarker(text, stream, index) : false;
  const ok = status === 200 && error === "" && !forbidden && bodyError === "" && contentOk && markerOk;

  return {
    index,
    stream,
    status,
    ok,
    contentOk,
    markerOk,
    forbidden,
    capacity_reason: capacityReason || undefined,
    body_error: bodyError || undefined,
    error: error || undefined,
    duration_ms: Date.now() - startedAt,
    sample: text.slice(0, 240),
  };
}

async function runBatch(kind, total, initialConcurrency, writer) {
  const stream = kind === "stream";
  if (total === 0) {
    return { results: [], capacityAbort: null };
  }
  const results = new Array(total);
  let next = 0;
  let capacityAbort = null;
  let dynamicConcurrency = initialConcurrency;
  let inFlight = 0;

  async function maybeRefreshReadiness(completed) {
    if (completed === 0 || completed % readinessCheckEvery !== 0) return;
    const readiness = await getReadiness();
    writer.write(`${JSON.stringify({ type: "readiness_checkpoint", kind, completed, body: readinessSummary(readiness.body || {}) })}\n`);
    if (!readinessAllowsTraffic(readiness)) {
      if (resumeAfterReadinessBlock) {
        const recovered = await waitForCapacityRecovery(writer, "readiness_blocked");
        if (readinessAllowsTraffic(recovered)) {
          dynamicConcurrency = Math.max(1, Math.min(initialConcurrency, readinessSafeConcurrency(recovered)));
          return;
        }
      }
      capacityAbort = {
        kind,
        index: completed,
        status: readiness.statusCode || 0,
        capacity_reason: "readiness_blocked",
        sample: JSON.stringify(readinessSummary(readiness.body || {})).slice(0, 240),
      };
      return;
    }
    const safe = readinessSafeConcurrency(readiness);
    if (safe > 0) {
      dynamicConcurrency = Math.max(1, Math.min(initialConcurrency, safe));
    }
  }

  async function launchOne(index) {
    inFlight += 1;
    try {
      if (requestSpacingMs > 0 && index > 0) {
        await sleep(requestSpacingMs);
      }
      const result = await callOnce(index, stream);
      results[index] = result;
      writer.write(`${JSON.stringify(result)}\n`);
      if (abortOnCapacityFailure && result.forbidden) {
        const recovery = await waitForCapacityRecovery(writer, result.capacity_reason || "capacity_or_gateway_protection");
        capacityAbort = {
          kind,
          index,
          status: result.status,
          capacity_reason: result.capacity_reason || "capacity_or_gateway_protection",
          readiness_after_recovery_wait: recovery.body || {},
          sample: result.sample,
        };
      }
    } finally {
      inFlight -= 1;
    }
  }

  const active = new Set();
  let completed = 0;
  while (!capacityAbort && next < total) {
    await maybeRefreshReadiness(completed);
    if (capacityAbort) break;
    const targetConcurrency = rampUp ? Math.min(dynamicConcurrency, Math.max(1, Math.floor(completed / readinessCheckEvery) + 1)) : dynamicConcurrency;
    while (!capacityAbort && next < total && inFlight < targetConcurrency) {
      const index = next;
      next += 1;
      const promise = launchOne(index).finally(() => active.delete(promise));
      active.add(promise);
    }
    if (active.size === 0) break;
    await Promise.race(active);
    completed = results.filter(Boolean).length;
  }
  await Promise.all(active);
  return { results: results.filter(Boolean), capacityAbort };
}

function summarize(name, results) {
  const failed = results.filter((result) => !result.ok);
  return {
    name,
    total: results.length,
    passed: results.length - failed.length,
    failed: failed.length,
    forbidden_statuses: results.filter((result) => [429, 502, 503].includes(result.status)).length,
    content_failures: results.filter((result) => !result.contentOk).length,
    failures: failed.slice(0, 10),
  };
}

async function main() {
  fs.mkdirSync(outputDir, { recursive: true });
  const readiness = await waitForReadiness();
  fs.writeFileSync(path.join(outputDir, "readiness.json"), `${JSON.stringify(readiness, null, 2)}\n`);
  const body = readiness.body || {};
  const safeConcurrency = Number(body.safeConcurrency || 0);
  const status = String(body.status || "");
  if (!["healthy", "degraded"].includes(status) || safeConcurrency <= 0) {
    const blocked = {
      verdict: "BLOCKED_BY_UPSTREAM_CAPACITY",
      model,
      readinessStatus: status,
      safeConcurrency,
      retryAfterSeconds: body.retryAfterSeconds || 0,
      reasonCodes: body.reasonCodes || [],
    };
    fs.writeFileSync(path.join(outputDir, "summary.json"), `${JSON.stringify(blocked, null, 2)}\n`);
    console.log(JSON.stringify(blocked, null, 2));
    process.exit(3);
  }

  const effectiveConcurrency = Math.max(1, Math.min(requestedConcurrency, safeConcurrency));
  const nonStreamWriter = fs.createWriteStream(path.join(outputDir, "non-stream.jsonl"));
  const streamWriter = fs.createWriteStream(path.join(outputDir, "stream.jsonl"));
  const nonStreamRun = await runBatch("non-stream", nonStreamTotal, effectiveConcurrency, nonStreamWriter);
  const streamRun = nonStreamRun.capacityAbort
    ? { results: [], capacityAbort: nonStreamRun.capacityAbort }
    : await runBatch("stream", streamTotal, effectiveConcurrency, streamWriter);
  nonStreamWriter.end();
  streamWriter.end();

  const finalReadiness = await getReadiness();
  fs.writeFileSync(path.join(outputDir, "readiness-after.json"), `${JSON.stringify(finalReadiness, null, 2)}\n`);
  const summaries = [summarize("non-stream", nonStreamRun.results), summarize("stream", streamRun.results)];
  const failed = summaries.reduce((sum, item) => sum + item.failed, 0);
  const capacityAbort = nonStreamRun.capacityAbort || streamRun.capacityAbort || null;
  const summary = {
    verdict: capacityAbort ? "BLOCKED_BY_UPSTREAM_CAPACITY" : (failed === 0 ? "PASS" : "FAIL"),
    model,
    requestedConcurrency,
    effectiveConcurrency,
    capacityAbort: capacityAbort || undefined,
    summaries,
  };
  fs.writeFileSync(path.join(outputDir, "summary.json"), `${JSON.stringify(summary, null, 2)}\n`);
  console.log(JSON.stringify(summary, null, 2));
  process.exit(capacityAbort ? 3 : (failed === 0 ? 0 : 1));
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
