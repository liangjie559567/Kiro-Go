#!/usr/bin/env node
'use strict';

const fs = require('fs');
const path = require('path');

function usage() {
  console.error('Usage: node parse-anthropic-sse.js <sse-file>');
}

function parseSse(text) {
  const events = [];
  let eventName = 'message';
  let dataLines = [];

  function flush() {
    if (eventName === 'message' && dataLines.length === 0) return;
    const dataText = dataLines.join('\n');
    let parsed = null;
    if (dataText && dataText !== '[DONE]') {
      try {
        parsed = JSON.parse(dataText);
      } catch (error) {
        parsed = { parse_error: error.message, raw: dataText };
      }
    }
    events.push({
      index: events.length,
      event: eventName,
      data: dataText,
      parsed,
    });
    eventName = 'message';
    dataLines = [];
  }

  for (const rawLine of text.split(/\r?\n/)) {
    if (rawLine === '') {
      flush();
      continue;
    }
    if (rawLine.startsWith(':')) continue;

    const separator = rawLine.indexOf(':');
    const field = separator === -1 ? rawLine : rawLine.slice(0, separator);
    let value = separator === -1 ? '' : rawLine.slice(separator + 1);
    if (value.startsWith(' ')) value = value.slice(1);

    if (field === 'event') eventName = value || 'message';
    if (field === 'data') dataLines.push(value);
  }
  flush();

  return events;
}

function appendText(block, delta) {
  if (!delta) return;
  if (typeof delta.text === 'string') block.text = (block.text || '') + delta.text;
  if (typeof delta.partial_json === 'string') block.input_json = (block.input_json || '') + delta.partial_json;
}

function summarizeEvents(events) {
  const content = [];
  const contentByIndex = new Map();
  const eventOrder = events.map((event) => event.event);
  let messageId = null;
  let stopReason = null;
  let usage = null;

  for (const event of events) {
    const parsed = event.parsed;
    if (!parsed || typeof parsed !== 'object') continue;

    if (parsed.type === 'message_start' && parsed.message) {
      messageId = parsed.message.id || messageId;
      usage = parsed.message.usage || usage;
    }

    if (parsed.type === 'content_block_start' && parsed.content_block) {
      const block = {
        index: parsed.index,
        type: parsed.content_block.type,
      };
      if (parsed.content_block.id) block.id = parsed.content_block.id;
      if (parsed.content_block.name) block.name = parsed.content_block.name;
      if (typeof parsed.content_block.text === 'string') block.text = parsed.content_block.text;
      if (parsed.content_block.input && typeof parsed.content_block.input === 'object') {
        block.input = parsed.content_block.input;
      }
      contentByIndex.set(parsed.index, block);
      content.push(block);
    }

    if (parsed.type === 'content_block_delta') {
      let block = contentByIndex.get(parsed.index);
      if (!block) {
        block = { index: parsed.index, type: 'unknown' };
        contentByIndex.set(parsed.index, block);
        content.push(block);
      }
      appendText(block, parsed.delta);
    }

    if (parsed.type === 'message_delta') {
      if (parsed.delta && parsed.delta.stop_reason) stopReason = parsed.delta.stop_reason;
      usage = parsed.usage || usage;
    }
  }

  for (const block of content) {
    if (block.input_json) {
      try {
        block.input = JSON.parse(block.input_json);
        delete block.input_json;
      } catch (error) {
        block.input_parse_error = error.message;
      }
    }
  }

  return {
    event_count: events.length,
    event_order: eventOrder,
    message_id: messageId,
    stop_reason: stopReason,
    usage,
    content,
    tool_uses: content.filter((block) => block.type === 'tool_use'),
    text: content
      .filter((block) => block.type === 'text' && block.text)
      .map((block) => block.text)
      .join(''),
  };
}

function main() {
  if (process.argv.includes('--self-test')) {
    const sample = [
      'event: message_start',
      'data: {"type":"message_start","message":{"id":"msg_test","usage":{"input_tokens":1,"output_tokens":0}}}',
      '',
      'event: content_block_start',
      'data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_file","input":{}}}',
      '',
      'event: content_block_delta',
      'data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\\"path\\":\\"README.md\\"}"}}',
      '',
      'event: content_block_stop',
      'data: {"type":"content_block_stop","index":0}',
      '',
      'event: message_delta',
      'data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"input_tokens":1,"output_tokens":1}}',
      '',
      'event: message_stop',
      'data: {"type":"message_stop"}',
      '',
    ].join('\n');
    const summary = summarizeEvents(parseSse(sample));
    if (!summary.event_order.includes('message_start') || summary.tool_uses.length !== 1 || summary.tool_uses[0].input.path !== 'README.md') {
      console.error('self-test failed');
      process.exitCode = 1;
      return;
    }
    console.log(JSON.stringify({ status: 'PASS', summary }, null, 2));
    return;
  }

  const file = process.argv[2];
  if (!file) {
    usage();
    process.exitCode = 2;
    return;
  }

  const input = fs.readFileSync(file, 'utf8');
  const events = parseSse(input);
  const summary = summarizeEvents(events);
  const result = {
    source: path.resolve(file),
    summary,
    events: events.map((event) => ({
      index: event.index,
      event: event.event,
      type: event.parsed && event.parsed.type ? event.parsed.type : null,
    })),
  };

  console.log(JSON.stringify(result, null, 2));
}

if (require.main === module) main();

module.exports = {
  parseSse,
  summarizeEvents,
};
