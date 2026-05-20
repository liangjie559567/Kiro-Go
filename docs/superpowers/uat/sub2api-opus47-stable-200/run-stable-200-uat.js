const baseURL = process.env.SUB2API_BASE_URL || "http://127.0.0.1:18080";
const apiKey = process.env.SUB2API_API_KEY;
const model = process.env.MODEL || "claude-opus-4.7";
const rounds = positiveInt(process.env.ROUNDS, 10);
const concurrency = positiveInt(process.env.CONCURRENCY, 10);

if (!apiKey) {
  console.error("SUB2API_API_KEY is required");
  process.exit(2);
}

function positiveInt(value, fallback) {
  const parsed = Number(value || fallback);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return fallback;
  }
  return Math.floor(parsed);
}

function hasForbiddenStatusMarker(status, text) {
  return [429, 502, 503].includes(status) ||
    /HTTP 429|HTTP 502|HTTP 503|kiro_go_stable_fallback|Opus 4\.7 is temporarily waiting/.test(text);
}

function hasAnthropicContent(text, stream) {
  if (stream) {
    return /content_block_delta/.test(text) && /text_delta|input_json_delta|thinking_delta/.test(text);
  }
  try {
    const body = JSON.parse(text);
    const content = Array.isArray(body.content) ? body.content : [];
    return content.some((block) => {
      if (!block || typeof block !== "object") return false;
      if (block.type === "text") return typeof block.text === "string" && block.text.trim().length > 0;
      if (block.type === "tool_use") return true;
      return false;
    });
  } catch {
    return false;
  }
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

function validateSSEBody(text) {
  const dataLines = text
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line.startsWith("data:"));

  if (dataLines.length === 0) {
    return "sse body has no data lines";
  }

  let sawMessageStart = false;
  let sawMessageStop = false;
  for (const line of dataLines) {
    const payload = line.slice("data:".length).trim();
    if (!payload || payload === "[DONE]") {
      continue;
    }
    let event;
    try {
      event = JSON.parse(payload);
    } catch (err) {
      return `invalid sse json: ${err.message}`;
    }
    if (event.type === "error" || event.error) {
      return "sse body contains error envelope";
    }
    if (event.type === "message_start") {
      sawMessageStart = true;
    }
    if (event.type === "message_stop") {
      sawMessageStop = true;
    }
  }

  if (!sawMessageStart || !sawMessageStop) {
    return "sse body missing message_start or message_stop";
  }
  return "";
}

function validateBody(text, stream) {
  if (stream) {
    return validateSSEBody(text);
  }
  return validateJSONBody(text);
}

async function callOnce(index, stream) {
  const res = await fetch(`${baseURL}/v1/messages`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${apiKey}`,
      "Content-Type": "application/json",
      "User-Agent": "sub2api-stable-200-uat/1.0 claude-cli/2.1",
      "X-Sub2API-Request": "uat",
    },
    body: JSON.stringify({
      model,
      max_tokens: 64,
      stream,
      messages: [
        {
          role: "user",
          content: `stable 200 uat request ${index}; reply with ok`,
        },
      ],
    }),
  });

  const text = await res.text();
  const forbidden = hasForbiddenStatusMarker(res.status, text);
  const bodyError = validateBody(text, stream);
  const contentOk = hasAnthropicContent(text, stream);
  const ok = res.status === 200 && !forbidden && bodyError === "" && contentOk;

  return {
    index,
    stream,
    status: res.status,
    ok,
    contentOk,
    forbidden,
    body_error: bodyError || undefined,
    sample: text.slice(0, 240),
  };
}

async function main() {
  const total = rounds * concurrency;
  const results = new Array(total);
  let next = 0;

  async function worker() {
    for (;;) {
      const index = next;
      next += 1;
      if (index >= total) {
        return;
      }
      results[index] = await callOnce(index, index % 2 === 0);
    }
  }

  await Promise.all(Array.from({ length: concurrency }, worker));
  const failed = results.filter((result) => !result.ok);
  const forbiddenStatuses = results.filter((result) => [429, 502, 503].includes(result.status)).length;

  console.log(JSON.stringify({
    total: results.length,
    passed: results.length - failed.length,
    failed: failed.length,
    forbidden_statuses: forbiddenStatuses,
    failures: failed.slice(0, 10),
  }, null, 2));

  process.exit(failed.length === 0 ? 0 : 1);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
