const fs = require('fs');
const path = require('path');

const base = process.env.SUB2API_BASE || 'http://127.0.0.1:18080';
const model = process.env.SUB2API_MODEL || 'claude-sonnet-4.5';
const keyPath = process.env.SUB2API_KEY_FILE || '/tmp/sub2api_claude_key';
const outDir = process.env.SUB2API_SMOKE_OUT || '/www/Kiro-Go/docs/superpowers/uat/sub2api-smoke';
const runId = process.env.SUB2API_SMOKE_RUN_ID || `sub2api-smoke-${Date.now()}`;
let apiKey = '';

fs.mkdirSync(outDir, {recursive: true});

const results = {
  runId,
  base,
  model,
  keyPath,
  startedAt: new Date().toISOString(),
};

function errorDetails(err) {
  return {
    message: err && err.message ? err.message : String(err),
    code: err && err.code,
    stack: err && err.stack,
  };
}

function writeArtifact() {
  results.finishedAt = new Date().toISOString();
  const out = path.join(outDir, `${runId}.json`);
  fs.writeFileSync(out, JSON.stringify(results, null, 2));
  return out;
}

function requestHeaders(endpoint) {
  return {
    Authorization: `Bearer ${apiKey}`,
    'Content-Type': 'application/json',
    'anthropic-version': '2023-06-01',
    'x-request-id': `${runId}-${endpoint.replace(/[^a-z0-9]+/gi, '-').replace(/^-|-$/g, '')}`,
    'x-claude-code-session-id': runId,
  };
}

function extractClaudeText(body) {
  if (!body || typeof body !== 'object' || !Array.isArray(body.content)) return '';
  return body.content.map((block) => {
    if (!block || typeof block !== 'object') return '';
    if (block.type === 'text' && typeof block.text === 'string') return block.text;
    if (typeof block.text === 'string') return block.text;
    return '';
  }).join('');
}

function parseSSE(raw) {
  const events = [];
  const text = [];
  const parseErrors = [];
  let currentEvent = '';
  let dataLines = [];

  function flush() {
    if (!currentEvent && dataLines.length === 0) return;
    const data = dataLines.join('\n').trim();
    const event = currentEvent || '';
    if (event) events.push(event);
    currentEvent = '';
    dataLines = [];

    if (!data || data === '[DONE]') return;
    try {
      const parsed = JSON.parse(data);
      if (parsed && parsed.type && !event) events.push(parsed.type);
      if (parsed.type === 'content_block_delta' && parsed.delta && typeof parsed.delta.text === 'string') {
        text.push(parsed.delta.text);
      }
      if (parsed.type === 'error') {
        text.push(JSON.stringify(parsed.error || parsed));
      }
    } catch (err) {
      events.push('parse_error');
      parseErrors.push({
        event,
        dataPreview: data.slice(0, 500),
        error: err && err.message ? err.message : String(err),
      });
    }
  }

  for (const line of raw.split(/\r?\n/)) {
    if (line === '') {
      flush();
      continue;
    }
    if (line.startsWith(':')) continue;
    if (line.startsWith('event:')) {
      currentEvent = line.slice(6).trim();
      continue;
    }
    if (line.startsWith('data:')) {
      dataLines.push(line.slice(5).trimStart());
    }
  }
  flush();

  return {
    events,
    text: text.join(''),
    parseErrors,
    hasMessageStart: events.includes('message_start'),
    hasMessageStop: events.includes('message_stop'),
  };
}

async function requestJSON(endpoint, payload) {
  const started = Date.now();
  const res = await fetch(`${base}${endpoint}`, {
    method: 'POST',
    headers: requestHeaders(endpoint),
    body: JSON.stringify(payload),
  });
  const raw = await res.text();
  return {
    status: res.status,
    ok: res.ok,
    durationMs: Date.now() - started,
    raw,
  };
}

async function runProbe(name, fn) {
  try {
    results[name] = await fn();
  } catch (err) {
    results[name] = {
      ok: false,
      error: errorDetails(err),
    };
  }
}

function parseJSON(raw) {
  try {
    return JSON.parse(raw);
  } catch (err) {
    return null;
  }
}

async function main() {
  try {
    apiKey = fs.readFileSync(keyPath, 'utf8').trim();
  } catch (err) {
    results.error = {
      phase: 'read_api_key',
      ...errorDetails(err),
    };
    results.passed = false;
    const out = writeArtifact();
    console.log(JSON.stringify({out, passed: results.passed, results}, null, 2));
    process.exit(1);
  }

  const marker = `${runId}-marker`;
  const messages = [{role: 'user', content: `Return exactly this marker and nothing else: ${marker}`}];

  await runProbe('models', async () => {
    const started = Date.now();
    const res = await fetch(`${base}/v1/models`, {
      method: 'GET',
      headers: requestHeaders('/v1/models'),
    });
    const raw = await res.text();
    const body = parseJSON(raw);
    return {
      status: res.status,
      ok: res.ok,
      durationMs: Date.now() - started,
      modelCount: body && Array.isArray(body.data) ? body.data.length : undefined,
      rawPreview: res.ok ? undefined : raw.slice(0, 1000),
    };
  });

  await runProbe('countTokens', async () => {
    const count = await requestJSON('/v1/messages/count_tokens', {
      model,
      messages,
    });
    const body = parseJSON(count.raw);
    return {
      status: count.status,
      ok: count.ok,
      durationMs: count.durationMs,
      body,
      positiveInputTokens: Boolean(body && Number(body.input_tokens) > 0),
      rawPreview: count.ok ? undefined : count.raw.slice(0, 1000),
    };
  });

  await runProbe('sync', async () => {
    const sync = await requestJSON('/v1/messages', {
      model,
      max_tokens: 64,
      stream: false,
      messages,
    });
    const body = parseJSON(sync.raw);
    const text = extractClaudeText(body);
    return {
      status: sync.status,
      ok: sync.ok,
      durationMs: sync.durationMs,
      text,
      textTrimmed: text.trim(),
      correct: sync.ok && text === marker,
      stopReason: body && body.stop_reason,
      usage: body && body.usage,
      rawPreview: sync.ok && text === marker ? undefined : sync.raw.slice(0, 1000),
    };
  });

  await runProbe('stream', async () => {
    const stream = await requestJSON('/v1/messages', {
      model,
      max_tokens: 64,
      stream: true,
      messages,
    });
    const parsed = parseSSE(stream.raw);
    const text = parsed.text;
    return {
      status: stream.status,
      ok: stream.ok,
      durationMs: stream.durationMs,
      text,
      textTrimmed: text.trim(),
      correct: stream.ok && text === marker,
      hasMessageStart: parsed.hasMessageStart,
      hasMessageStop: parsed.hasMessageStop,
      eventCount: parsed.events.length,
      events: parsed.events,
      parseErrors: parsed.parseErrors,
      parseErrorCount: parsed.parseErrors.length,
      rawPreview: stream.ok && text === marker && parsed.hasMessageStop && parsed.parseErrors.length === 0 ? undefined : stream.raw.slice(0, 1000),
    };
  });

  results.passed = Boolean(
    results.models && results.models.ok &&
    results.countTokens && results.countTokens.ok &&
    results.countTokens.positiveInputTokens &&
    results.sync && results.sync.correct &&
    results.stream && results.stream.correct &&
    results.stream.hasMessageStop &&
    results.stream.parseErrorCount === 0
  );

  const out = writeArtifact();
  console.log(JSON.stringify({out, passed: results.passed, results}, null, 2));

  if (!results.passed) process.exit(1);
}

main().catch((err) => {
  results.error = {
    phase: 'main',
    ...errorDetails(err),
  };
  results.passed = false;
  try {
    const out = writeArtifact();
    console.log(JSON.stringify({out, passed: results.passed, results}, null, 2));
  } catch (writeErr) {
    console.error(writeErr && writeErr.stack || writeErr);
  }
  console.error(err && err.stack || err);
  process.exit(1);
});
