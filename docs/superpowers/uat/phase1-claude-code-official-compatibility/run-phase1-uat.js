#!/usr/bin/env node
'use strict';

const fs = require('fs');
const path = require('path');
const { parseSse, summarizeEvents } = require('./parse-anthropic-sse');

const ROOT = __dirname;
const RUNS_DIR = path.join(ROOT, 'runs');
const DEFAULT_MODEL = process.env.UAT_MODEL || process.env.ANTHROPIC_MODEL || 'claude-opus-4.7';
const REQUIRED_ENV = [
  'KIRO_GO_BASE_URL',
  'KIRO_GO_API_KEY',
  'KIRO_GO_ADMIN_PASSWORD',
];
const OPTIONAL_ENV = [
  'SUB2API_BASE_URL',
  'SUB2API_API_KEY',
];

function isoCompact(date) {
  return date.toISOString().replace(/[-:.TZ]/g, '').slice(0, 14);
}

function ensureDir(dir) {
  fs.mkdirSync(dir, { recursive: true });
}

function cleanBaseUrl(value) {
  return String(value || '').replace(/\/+$/, '');
}

function redactString(value) {
  const secrets = [
    process.env.KIRO_GO_API_KEY,
    process.env.KIRO_GO_ADMIN_PASSWORD,
    process.env.SUB2API_API_KEY,
  ].filter(Boolean);
  let out = String(value);
  for (const secret of secrets) out = out.split(secret).join('[REDACTED]');
  out = out.replace(/Bearer\s+[A-Za-z0-9._~+/=-]+/gi, 'Bearer [REDACTED]');
  out = out.replace(/(api[_-]?key|admin[_-]?password|authorization)(["'\s:=]+)([^"',\s}]+)/gi, '$1$2[REDACTED]');
  return out;
}

function redact(value) {
  if (value == null) return value;
  if (typeof value === 'string') return redactString(value);
  if (Array.isArray(value)) return value.map(redact);
  if (typeof value === 'object') {
    const out = {};
    for (const [key, item] of Object.entries(value)) {
      if (/authorization|api[_-]?key|admin[_-]?password|password|token|secret/i.test(key)) {
        out[key] = '[REDACTED]';
      } else {
        out[key] = redact(item);
      }
    }
    return out;
  }
  return value;
}

function writeJson(file, value) {
  fs.writeFileSync(file, JSON.stringify(redact(value), null, 2) + '\n');
}

function writeText(file, value) {
  fs.writeFileSync(file, redactString(value));
}

function blocked(name, missingEnv) {
  return {
    name,
    status: 'BLOCKED_BY_ENV',
    missing_env: missingEnv,
    detail: 'Required environment variable(s) missing; check was not executed.',
  };
}

function skipped(name, detail) {
  return { name, status: 'SKIPPED', detail };
}

function pass(name, detail, extra) {
  return Object.assign({ name, status: 'PASS', detail }, extra || {});
}

function fail(name, detail, extra) {
  return Object.assign({ name, status: 'FAIL', detail }, extra || {});
}

function envSnapshot() {
  const names = REQUIRED_ENV.concat(OPTIONAL_ENV);
  const out = {};
  for (const name of names) out[name] = process.env[name] ? 'SET' : 'MISSING';
  return out;
}

function requiredMissing(names) {
  return names.filter((name) => !process.env[name]);
}

function anthHeaders() {
  return {
    'content-type': 'application/json',
    'anthropic-version': '2023-06-01',
    'x-api-key': process.env.KIRO_GO_API_KEY,
    'authorization': `Bearer ${process.env.KIRO_GO_API_KEY}`,
  };
}

function adminHeaders() {
  return {
    'content-type': 'application/json',
    'x-admin-password': process.env.KIRO_GO_ADMIN_PASSWORD,
    'authorization': `Bearer ${process.env.KIRO_GO_ADMIN_PASSWORD}`,
  };
}

function sub2apiHeaders() {
  return {
    'content-type': 'application/json',
    'anthropic-version': '2023-06-01',
    'anthropic-beta': 'claude-code-20250219,fine-grained-tool-streaming-2025-05-14',
    'x-claude-code-session-id': `phase1-uat-${process.pid}`,
    'x-claude-code-agent-id': 'phase1-uat',
    'authorization': `Bearer ${process.env.SUB2API_API_KEY}`,
  };
}

function baseRequestBody(extra) {
  return Object.assign({
    model: DEFAULT_MODEL,
    max_tokens: 128,
    messages: [
      {
        role: 'user',
        content: 'Reply with the exact text: phase1-uat-ok',
      },
    ],
  }, extra || {});
}

async function captureResponse(res, outDir, stem) {
  const contentType = res.headers.get('content-type') || '';
  const text = await res.text();
  const file = path.join(outDir, `${stem}${contentType.includes('text/event-stream') ? '.sse' : '.json'}`);
  writeText(file, text);

  let json = null;
  if (text) {
    try {
      json = JSON.parse(text);
    } catch (_) {
      json = null;
    }
  }

  return {
    status: res.status,
    ok: res.ok,
    content_type: contentType,
    body_file: path.basename(file),
    text,
    json,
  };
}

async function postJson(url, headers, body, outDir, stem) {
  const started = Date.now();
  const res = await fetch(url, {
    method: 'POST',
    headers,
    body: JSON.stringify(body),
  });
  const captured = await captureResponse(res, outDir, stem);
  return Object.assign(captured, { duration_ms: Date.now() - started });
}

async function getJson(url, headers, outDir, stem) {
  const started = Date.now();
  const res = await fetch(url, { method: 'GET', headers });
  const captured = await captureResponse(res, outDir, stem);
  return Object.assign(captured, { duration_ms: Date.now() - started });
}

function jsonHasMessageShape(json) {
  return Boolean(json && json.id && json.type === 'message' && Array.isArray(json.content));
}

function hasToolUse(json) {
  return Boolean(json && Array.isArray(json.content) && json.content.some((item) => item && item.type === 'tool_use'));
}

function hasAssistantText(json, expectedPrefix) {
  return Boolean(
    json &&
      Array.isArray(json.content) &&
      json.content.some((item) => item && item.type === 'text' && String(item.text || '').startsWith(expectedPrefix)),
  );
}

async function checkMessagesNonStream(ctx) {
  const name = '/v1/messages non-stream';
  const missing = requiredMissing(['KIRO_GO_BASE_URL', 'KIRO_GO_API_KEY']);
  if (missing.length) return blocked(name, missing);

  try {
    const body = baseRequestBody({ stream: false });
    const result = await postJson(`${ctx.kiroBase}/v1/messages`, anthHeaders(), body, ctx.outDir, 'messages-nonstream');
    if (!result.ok) return fail(name, `HTTP ${result.status}`, { response: summarizeHttp(result) });
    if (!jsonHasMessageShape(result.json)) return fail(name, 'Response did not match Anthropic message shape.', { response: summarizeHttp(result) });
    return pass(name, 'Received Anthropic message response.', { response: summarizeHttp(result) });
  } catch (error) {
    return fail(name, error.message);
  }
}

async function checkMessagesStream(ctx) {
  const name = '/v1/messages stream';
  const missing = requiredMissing(['KIRO_GO_BASE_URL', 'KIRO_GO_API_KEY']);
  if (missing.length) return blocked(name, missing);

  try {
    const body = baseRequestBody({ stream: true });
    const result = await postJson(`${ctx.kiroBase}/v1/messages`, anthHeaders(), body, ctx.outDir, 'messages-stream');
    const events = parseSse(result.text);
    const summary = summarizeEvents(events);
    writeJson(path.join(ctx.outDir, 'messages-stream.summary.json'), summary);
    if (!result.ok) return fail(name, `HTTP ${result.status}`, { response: summarizeHttp(result), sse: summary });
    if (!summary.event_order.includes('message_start') || !summary.event_order.includes('message_stop')) {
      return fail(name, 'SSE did not include message_start and message_stop.', { response: summarizeHttp(result), sse: summary });
    }
    return pass(name, 'Received parseable Anthropic SSE stream.', { response: summarizeHttp(result), sse: summary });
  } catch (error) {
    return fail(name, error.message);
  }
}

async function checkMaxTokensZero(ctx) {
  const name = '/v1/messages max_tokens=0';
  const missing = requiredMissing(['KIRO_GO_BASE_URL', 'KIRO_GO_API_KEY']);
  if (missing.length) return blocked(name, missing);

  try {
    const body = baseRequestBody({
      max_tokens: 0,
      messages: [
        {
          role: 'user',
          content: 'Warm the cache and return no output.',
        },
      ],
    });
    const result = await postJson(`${ctx.kiroBase}/v1/messages`, anthHeaders(), body, ctx.outDir, 'messages-max-tokens-zero');
    if (!result.ok) return fail(name, `HTTP ${result.status}`, { response: summarizeHttp(result) });
    if (!jsonHasMessageShape(result.json)) return fail(name, 'Response did not match Anthropic message shape.', { response: summarizeHttp(result) });
    if (result.json.stop_reason !== 'max_tokens') return fail(name, 'Expected stop_reason=max_tokens.', { response: summarizeHttp(result) });
    if (!Array.isArray(result.json.content) || result.json.content.length !== 0) return fail(name, 'Expected empty content array.', { response: summarizeHttp(result) });
    if (!result.json.usage || result.json.usage.output_tokens !== 0) return fail(name, 'Expected output_tokens=0.', { response: summarizeHttp(result) });
    return pass(name, 'Received compatible max_tokens=0 response.', { response: summarizeHttp(result) });
  } catch (error) {
    return fail(name, error.message);
  }
}

async function checkAssistantPrefill(ctx) {
  const name = '/v1/messages assistant prefill';
  const missing = requiredMissing(['KIRO_GO_BASE_URL', 'KIRO_GO_API_KEY']);
  if (missing.length) return blocked(name, missing);

  try {
    const body = baseRequestBody({
      max_tokens: 64,
      messages: [
        {
          role: 'user',
          content: 'Return valid JSON with key ok.',
        },
        {
          role: 'assistant',
          content: '{"ok":',
        },
      ],
    });
    const result = await postJson(`${ctx.kiroBase}/v1/messages`, anthHeaders(), body, ctx.outDir, 'messages-assistant-prefill');
    if (!result.ok) return fail(name, `HTTP ${result.status}`, { response: summarizeHttp(result) });
    if (!jsonHasMessageShape(result.json)) return fail(name, 'Response did not match Anthropic message shape.', { response: summarizeHttp(result) });
    return pass(name, 'Accepted assistant prefill request.', { response: summarizeHttp(result) });
  } catch (error) {
    return fail(name, error.message);
  }
}

async function checkFineGrainedToolStreaming(ctx) {
  const name = '/v1/messages fine-grained tool streaming';
  const missing = requiredMissing(['KIRO_GO_BASE_URL', 'KIRO_GO_API_KEY']);
  if (missing.length) return blocked(name, missing);

  try {
    const body = baseRequestBody({
      max_tokens: 256,
      messages: [
        {
          role: 'user',
          content: 'Use the get_weather tool for Shanghai.',
        },
      ],
      tools: [
        {
          name: 'get_weather',
          description: 'Get current weather for a city.',
          input_schema: {
            type: 'object',
            properties: { city: { type: 'string' } },
            required: ['city'],
          },
        },
      ],
      tool_choice: { type: 'tool', name: 'get_weather' },
      stream: true,
    });
    const headers = Object.assign({}, anthHeaders(), {
      'anthropic-beta': 'fine-grained-tool-streaming-2025-05-14',
    });
    const result = await postJson(`${ctx.kiroBase}/v1/messages`, headers, body, ctx.outDir, 'messages-fine-grained-tool-streaming');
    const events = parseSse(result.text);
    const summary = summarizeEvents(events);
    writeJson(path.join(ctx.outDir, 'messages-fine-grained-tool-streaming.summary.json'), summary);
    if (!result.ok) return fail(name, `HTTP ${result.status}`, { response: summarizeHttp(result), sse: summary });
    if (!summary.event_order.length) return fail(name, 'Expected parseable SSE events.', { response: summarizeHttp(result), sse: summary });
    return pass(name, 'Received fine-grained tool streaming request.', { response: summarizeHttp(result), sse: summary });
  } catch (error) {
    return fail(name, error.message);
  }
}

async function checkToolReference(ctx) {
  const name = '/v1/messages tool_reference';
  const missing = requiredMissing(['KIRO_GO_BASE_URL', 'KIRO_GO_API_KEY']);
  if (missing.length) return blocked(name, missing);

  try {
    const body = baseRequestBody({
      max_tokens: 64,
      messages: [
        {
          role: 'user',
          content: 'Read README.md through the referenced MCP tool if possible.',
        },
      ],
      tool_reference: [
        {
          type: 'tool_reference',
          id: 'toolref_1',
          name: 'mcp__filesystem__read_file',
          description: 'Read a file',
          input_schema: {
            type: 'object',
            properties: {
              path: { type: 'string' },
            },
            required: ['path'],
          },
        },
      ],
    });
    const headers = Object.assign({}, anthHeaders(), {
      'anthropic-beta': 'tool-search-2025-10-19',
    });
    const result = await postJson(`${ctx.kiroBase}/v1/messages`, headers, body, ctx.outDir, 'messages-tool-reference');
    if (!result.ok) return fail(name, `HTTP ${result.status}`, { response: summarizeHttp(result) });
    if (!jsonHasMessageShape(result.json) && !hasToolUse(result.json)) {
      return fail(name, 'Response did not match Anthropic message or tool_use shape.', { response: summarizeHttp(result) });
    }
    return pass(name, 'Accepted tool_reference request.', { response: summarizeHttp(result) });
  } catch (error) {
    return fail(name, error.message);
  }
}

async function checkPromptCacheControl(ctx) {
  const name = '/v1/messages cache_control passthrough';
  const missing = requiredMissing(['KIRO_GO_BASE_URL', 'KIRO_GO_API_KEY']);
  if (missing.length) return blocked(name, missing);

  try {
    const body = baseRequestBody({
      max_tokens: 64,
      system: [
        {
          type: 'text',
          text: 'You are validating prompt cache control passthrough for Claude Code compatibility.',
          cache_control: { type: 'ephemeral' },
        },
      ],
      messages: [
        {
          role: 'user',
          content: [
            {
              type: 'text',
              text: 'Reply with the exact text: phase1-cache-control-ok',
            },
          ],
        },
      ],
      metadata: {
        user_id: 'phase1-uat-cache-control',
      },
    });
    const result = await postJson(`${ctx.kiroBase}/v1/messages`, anthHeaders(), body, ctx.outDir, 'messages-cache-control');
    if (!result.ok) return fail(name, `HTTP ${result.status}`, { response: summarizeHttp(result) });
    if (!jsonHasMessageShape(result.json)) return fail(name, 'Response did not match Anthropic message shape.', { response: summarizeHttp(result) });
    return pass(name, 'Accepted cache_control and metadata fields.', { response: summarizeHttp(result) });
  } catch (error) {
    return fail(name, error.message);
  }
}

async function checkClaudeCodeReadinessSignals(ctx) {
  const name = '/admin/api/claude-code/readiness signals';
  const missing = requiredMissing(['KIRO_GO_BASE_URL', 'KIRO_GO_ADMIN_PASSWORD']);
  if (missing.length) return blocked(name, missing);

  try {
    const result = await getJson(`${ctx.kiroBase}/admin/api/claude-code/readiness`, adminHeaders(), ctx.outDir, 'admin-claude-code-readiness-signals');
    if (!result.ok) return fail(name, `HTTP ${result.status}`, { response: summarizeHttp(result) });
    const capabilities = result.json && result.json.capabilities;
    if (!capabilities || typeof capabilities !== 'object') return fail(name, 'Readiness response missing capabilities.', { response: summarizeHttp(result) });
    if (!result.json.recentToolReferences || !result.json.recentMCPTools || !result.json.recentFineGrainedToolStreaming) {
      return fail(name, 'Expected recent Claude Code signals to be present.', { response: summarizeHttp(result) });
    }
    return pass(name, 'Claude Code readiness reflected recent boundary probes.', { response: summarizeHttp(result) });
  } catch (error) {
    return fail(name, error.message);
  }
}

async function checkRequestLogSignals(ctx) {
  const name = '/admin/api/request-logs boundary signals';
  const missing = requiredMissing(['KIRO_GO_BASE_URL', 'KIRO_GO_ADMIN_PASSWORD']);
  if (missing.length) return blocked(name, missing);

  try {
    const result = await getJson(`${ctx.kiroBase}/admin/api/request-logs?limit=50`, adminHeaders(), ctx.outDir, 'admin-request-logs-signals');
    if (!result.ok) return fail(name, `HTTP ${result.status}`, { response: summarizeHttp(result) });
    const logs = Array.isArray(result.json && result.json.logs) ? result.json.logs : [];
    const maxZero = logs.find((entry) => entry.maxTokensZeroMode);
    const prefill = logs.find((entry) => entry.assistantPrefillMode);
    const fineGrained = logs.find((entry) => entry.fineGrainedToolStreamingRequested || entry.fineGrainedToolStreamingMode);
    const toolReference = logs.find((entry) => entry.toolReferenceCount > 0 || (Array.isArray(entry.payloadDeferredTools) && entry.payloadDeferredTools.length > 0));
    if (!maxZero || !prefill || !fineGrained || !toolReference) {
      return fail(name, 'Expected recent request logs for all boundary probes.', {
        response: summarizeHttp(result),
        found: {
          maxZero: Boolean(maxZero),
          prefill: Boolean(prefill),
          fineGrained: Boolean(fineGrained),
          toolReference: Boolean(toolReference),
        },
      });
    }
    return pass(name, 'Request logs recorded the boundary probes.', { response: summarizeHttp(result) });
  } catch (error) {
    return fail(name, error.message);
  }
}

async function checkToolUse(ctx) {
  const name = '/v1/messages tool-use shape';
  const missing = requiredMissing(['KIRO_GO_BASE_URL', 'KIRO_GO_API_KEY']);
  if (missing.length) return blocked(name, missing);

  try {
    const body = baseRequestBody({
      max_tokens: 256,
      messages: [
        {
          role: 'user',
          content: 'Use the get_weather tool for Shanghai.',
        },
      ],
      tools: [
        {
          name: 'get_weather',
          description: 'Get current weather for a city.',
          input_schema: {
            type: 'object',
            properties: { city: { type: 'string' } },
            required: ['city'],
          },
        },
      ],
      tool_choice: { type: 'tool', name: 'get_weather' },
    });
    const result = await postJson(`${ctx.kiroBase}/v1/messages`, anthHeaders(), body, ctx.outDir, 'messages-tool-use');
    if (!result.ok) return fail(name, `HTTP ${result.status}`, { response: summarizeHttp(result) });
    if (!hasToolUse(result.json)) return fail(name, 'Response did not include a tool_use content block.', { response: summarizeHttp(result) });
    ctx.toolUseResponse = result.json;
    return pass(name, 'Received tool_use content block.', { response: summarizeHttp(result) });
  } catch (error) {
    return fail(name, error.message);
  }
}

async function checkToolResultFollowUp(ctx) {
  const name = '/v1/messages tool-result follow-up';
  const missing = requiredMissing(['KIRO_GO_BASE_URL', 'KIRO_GO_API_KEY']);
  if (missing.length) return blocked(name, missing);
  if (!ctx.toolUseResponse) return skipped(name, 'Skipped because tool-use shape check did not produce a tool_use response.');

  try {
    const toolUse = ctx.toolUseResponse.content.find((item) => item.type === 'tool_use');
    const body = baseRequestBody({
      max_tokens: 128,
      messages: [
        {
          role: 'user',
          content: 'Use the get_weather tool for Shanghai.',
        },
        {
          role: 'assistant',
          content: ctx.toolUseResponse.content,
        },
        {
          role: 'user',
          content: [
            {
              type: 'tool_result',
              tool_use_id: toolUse.id,
              content: 'Shanghai weather is 23 C and clear.',
            },
          ],
        },
      ],
      tools: [
        {
          name: 'get_weather',
          description: 'Get current weather for a city.',
          input_schema: {
            type: 'object',
            properties: { city: { type: 'string' } },
            required: ['city'],
          },
        },
      ],
    });
    const result = await postJson(`${ctx.kiroBase}/v1/messages`, anthHeaders(), body, ctx.outDir, 'messages-tool-result-follow-up');
    if (!result.ok) return fail(name, `HTTP ${result.status}`, { response: summarizeHttp(result) });
    if (!jsonHasMessageShape(result.json)) return fail(name, 'Follow-up response did not match Anthropic message shape.', { response: summarizeHttp(result) });
    return pass(name, 'Accepted tool_result follow-up conversation.', { response: summarizeHttp(result) });
  } catch (error) {
    return fail(name, error.message);
  }
}

async function checkCountTokens(ctx) {
  const name = '/v1/messages/count_tokens';
  const missing = requiredMissing(['KIRO_GO_BASE_URL', 'KIRO_GO_API_KEY']);
  if (missing.length) return blocked(name, missing);

  try {
    const body = {
      model: DEFAULT_MODEL,
      messages: [
        {
          role: 'user',
          content: 'Count these tokens for phase 1 UAT.',
        },
      ],
    };
    const result = await postJson(`${ctx.kiroBase}/v1/messages/count_tokens`, anthHeaders(), body, ctx.outDir, 'count-tokens');
    if (!result.ok) return fail(name, `HTTP ${result.status}`, { response: summarizeHttp(result) });
    if (!result.json || typeof result.json.input_tokens !== 'number') {
      return fail(name, 'Response did not include numeric input_tokens.', { response: summarizeHttp(result) });
    }
    return pass(name, 'Received token count.', { response: summarizeHttp(result) });
  } catch (error) {
    return fail(name, error.message);
  }
}

async function checkModels(ctx) {
  const name = '/v1/models';
  const missing = requiredMissing(['KIRO_GO_BASE_URL', 'KIRO_GO_API_KEY']);
  if (missing.length) return blocked(name, missing);

  try {
    const result = await getJson(`${ctx.kiroBase}/v1/models`, anthHeaders(), ctx.outDir, 'models');
    if (!result.ok) return fail(name, `HTTP ${result.status}`, { response: summarizeHttp(result) });
    if (!result.json || !Array.isArray(result.json.data)) return fail(name, 'Response did not include data array.', { response: summarizeHttp(result) });
    return pass(name, 'Received model list.', { response: summarizeHttp(result) });
  } catch (error) {
    return fail(name, error.message);
  }
}

async function checkAdmin(ctx, endpoint, stem) {
  const name = endpoint;
  const missing = requiredMissing(['KIRO_GO_BASE_URL', 'KIRO_GO_ADMIN_PASSWORD']);
  if (missing.length) return blocked(name, missing);

  try {
    const result = await getJson(`${ctx.kiroBase}${endpoint}`, adminHeaders(), ctx.outDir, stem);
    if (!result.ok) return fail(name, `HTTP ${result.status}`, { response: summarizeHttp(result) });
    return pass(name, 'Admin endpoint returned success status.', { response: summarizeHttp(result) });
  } catch (error) {
    return fail(name, error.message);
  }
}

async function checkSub2apiModels(ctx) {
  const name = 'sub2api black-box optional /v1/models';
  const missing = requiredMissing(['SUB2API_BASE_URL', 'SUB2API_API_KEY']);
  if (missing.length) return skipped(name, `Optional sub2api check skipped; missing ${missing.join(', ')}.`);

  try {
    const result = await getJson(`${ctx.sub2apiBase}/v1/models`, sub2apiHeaders(), ctx.outDir, 'sub2api-models');
    if (!result.ok) return fail(name, `HTTP ${result.status}`, { response: summarizeHttp(result) });
    return pass(name, 'sub2api black-box model endpoint responded.', { response: summarizeHttp(result) });
  } catch (error) {
    return fail(name, error.message);
  }
}

async function checkSub2apiMessages(ctx) {
  const name = 'sub2api black-box optional /v1/messages Claude Code headers';
  const missing = requiredMissing(['SUB2API_BASE_URL', 'SUB2API_API_KEY']);
  if (missing.length) return skipped(name, `Optional sub2api check skipped; missing ${missing.join(', ')}.`);

  try {
    const body = baseRequestBody({
      stream: false,
      metadata: {
        user_id: 'phase1-uat',
      },
    });
    const result = await postJson(`${ctx.sub2apiBase}/v1/messages`, sub2apiHeaders(), body, ctx.outDir, 'sub2api-messages-nonstream');
    if (!result.ok) return fail(name, `HTTP ${result.status}`, { response: summarizeHttp(result) });
    if (!jsonHasMessageShape(result.json)) return fail(name, 'Response did not match Anthropic message shape.', { response: summarizeHttp(result) });
    return pass(name, 'sub2api accepted a Claude Code-shaped /v1/messages request.', { response: summarizeHttp(result) });
  } catch (error) {
    return fail(name, error.message);
  }
}

async function checkSub2apiMessagesStream(ctx) {
  const name = 'sub2api black-box optional /v1/messages stream';
  const missing = requiredMissing(['SUB2API_BASE_URL', 'SUB2API_API_KEY']);
  if (missing.length) return skipped(name, `Optional sub2api check skipped; missing ${missing.join(', ')}.`);

  try {
    const body = baseRequestBody({
      stream: true,
      metadata: {
        user_id: 'phase1-uat',
      },
    });
    const result = await postJson(`${ctx.sub2apiBase}/v1/messages`, sub2apiHeaders(), body, ctx.outDir, 'sub2api-messages-stream');
    const events = parseSse(result.text);
    const summary = summarizeEvents(events);
    writeJson(path.join(ctx.outDir, 'sub2api-messages-stream.summary.json'), summary);
    if (!result.ok) return fail(name, `HTTP ${result.status}`, { response: summarizeHttp(result), sse: summary });
    if (!summary.event_order.includes('message_start') || !summary.event_order.includes('message_stop')) {
      return fail(name, 'sub2api stream did not include message_start/message_stop.', { response: summarizeHttp(result), sse: summary });
    }
    return pass(name, 'sub2api accepted a streamed Claude Code-shaped /v1/messages request.', { response: summarizeHttp(result), sse: summary });
  } catch (error) {
    return fail(name, error.message);
  }
}

function summarizeHttp(result) {
  const out = {
    status: result.status,
    ok: result.ok,
    content_type: result.content_type,
    duration_ms: result.duration_ms,
    body_file: result.body_file,
  };
  if (result.json && result.json.id) out.id = result.json.id;
  if (result.json && result.json.type) out.type = result.json.type;
  if (result.json && typeof result.json.input_tokens === 'number') out.input_tokens = result.json.input_tokens;
  if (result.json && Array.isArray(result.json.data)) out.data_count = result.json.data.length;
  return out;
}

function statusCounts(checks) {
  return checks.reduce((acc, check) => {
    acc[check.status] = (acc[check.status] || 0) + 1;
    return acc;
  }, {});
}

function renderResult(summary) {
  const lines = [];
  lines.push('# Phase 1 Claude Code Official Compatibility UAT Result');
  lines.push('');
  lines.push(`Run ID: ${summary.run_id}`);
  lines.push(`Started: ${summary.started_at}`);
  lines.push(`Finished: ${summary.finished_at}`);
  lines.push(`Status: ${summary.overall_status}`);
  lines.push('');
  lines.push('## Environment');
  lines.push('');
  for (const [key, value] of Object.entries(summary.environment)) {
    lines.push(`- ${key}: ${value}`);
  }
  lines.push('');
  lines.push('## Checks');
  lines.push('');
  lines.push('| Check | Status | Detail |');
  lines.push('| --- | --- | --- |');
  for (const check of summary.checks) {
    lines.push(`| ${check.name} | ${check.status} | ${String(check.detail || '').replace(/\|/g, '\\|')} |`);
  }
  lines.push('');
  lines.push('## Artifacts');
  lines.push('');
  lines.push(`- Run directory: runs/${summary.run_id}/`);
  lines.push('- Raw responses are saved with secrets redacted.');
  lines.push('- `summary.json` contains machine-readable results.');
  lines.push('');
  return lines.join('\n');
}

async function main() {
  if (process.argv.includes('--dry-run')) {
    const missing = requiredMissing(REQUIRED_ENV);
    const summary = {
      status: 'DRY_RUN',
      environment: envSnapshot(),
      required_missing: missing,
      checks_planned: [
        '/v1/messages non-stream',
        '/v1/messages stream',
        '/v1/messages tool-use shape',
        '/v1/messages max_tokens=0',
        '/v1/messages assistant prefill',
        '/v1/messages fine-grained tool streaming',
        '/v1/messages tool_reference',
        '/v1/messages cache_control passthrough',
        '/v1/messages tool-result follow-up',
        '/v1/messages/count_tokens',
        '/v1/models',
        '/admin/api/claude-code/readiness',
        '/admin/api/claude-code/model-readiness',
        '/admin/api/request-logs',
        'sub2api black-box optional /v1/models',
        'sub2api black-box optional /v1/messages Claude Code headers',
        'sub2api black-box optional /v1/messages stream',
      ],
    };
    console.log(JSON.stringify(summary, null, 2));
    return;
  }

  const runId = process.env.UAT_RUN_ID || isoCompact(new Date());
  const outDir = path.join(RUNS_DIR, runId);
  ensureDir(outDir);

  const startedAt = new Date().toISOString();
  const ctx = {
    runId,
    outDir,
    kiroBase: cleanBaseUrl(process.env.KIRO_GO_BASE_URL),
    sub2apiBase: cleanBaseUrl(process.env.SUB2API_BASE_URL),
  };

  const checks = [];
  const steps = [
    checkMessagesNonStream,
    checkMessagesStream,
    checkToolUse,
    checkMaxTokensZero,
    checkAssistantPrefill,
    checkFineGrainedToolStreaming,
    checkToolReference,
    checkPromptCacheControl,
    checkToolResultFollowUp,
    checkCountTokens,
    checkModels,
    checkClaudeCodeReadinessSignals,
    (context) => checkAdmin(context, '/admin/api/claude-code/model-readiness', 'admin-claude-code-model-readiness'),
    checkRequestLogSignals,
    checkSub2apiModels,
    checkSub2apiMessages,
    checkSub2apiMessagesStream,
  ];

  for (const step of steps) {
    const result = await step(ctx);
    checks.push(result);
    console.log(`${result.status} ${result.name}`);
  }

  const counts = statusCounts(checks);
  const overallStatus = counts.FAIL ? 'FAIL' : counts.PASS ? 'COMPLETE_WITH_BLOCKERS_ALLOWED' : 'BLOCKED';
  const summary = {
    run_id: runId,
    started_at: startedAt,
    finished_at: new Date().toISOString(),
    overall_status: overallStatus,
    environment: envSnapshot(),
    counts,
    checks,
  };

  writeJson(path.join(outDir, 'summary.json'), summary);
  const rendered = renderResult(summary);
  writeText(path.join(outDir, 'UAT-RESULT.md'), rendered);
  writeText(path.join(ROOT, 'UAT-RESULT.md'), rendered);

  if (counts.FAIL) process.exitCode = 1;
}

main().catch((error) => {
  console.error(redactString(error.stack || error.message));
  process.exitCode = 1;
});
