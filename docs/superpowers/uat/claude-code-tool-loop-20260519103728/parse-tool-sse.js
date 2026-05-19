const fs = require('fs');

const [inputPath, outputPath] = process.argv.slice(2);
const raw = fs.readFileSync(inputPath, 'utf8');
const frames = raw.split(/\n\n+/).filter(Boolean).map(frame => {
  const lines = frame.split(/\n/);
  const eventLine = lines.find(line => line.startsWith('event: '));
  const dataLine = lines.find(line => line.startsWith('data: '));
  let data = null;
  if (dataLine) {
    try {
      data = JSON.parse(dataLine.slice('data: '.length));
    } catch (err) {
      data = { parseError: String(err), raw: dataLine.slice('data: '.length) };
    }
  }
  return { event: eventLine ? eventLine.slice('event: '.length) : '', data };
});

const toolsByIndex = new Map();
for (const frame of frames) {
  if (frame.event === 'content_block_start' && frame.data?.content_block?.type === 'tool_use') {
    toolsByIndex.set(frame.data.index, {
      id: frame.data.content_block.id,
      name: frame.data.content_block.name,
      startInput: frame.data.content_block.input ?? null,
      partialJson: ''
    });
  }
  if (frame.event === 'content_block_delta' && frame.data?.delta?.type === 'input_json_delta') {
    const tool = toolsByIndex.get(frame.data.index);
    if (tool) tool.partialJson += frame.data.delta.partial_json || '';
  }
}

const tools = Array.from(toolsByIndex.values()).map(tool => {
  let reconstructedInput = null;
  let reconstructedError = null;
  if (tool.partialJson) {
    try {
      reconstructedInput = JSON.parse(tool.partialJson);
    } catch (err) {
      reconstructedError = String(err);
    }
  }
  return { ...tool, reconstructedInput, reconstructedError };
});

const messageDelta = frames.find(frame => frame.event === 'message_delta');
const summary = {
  httpError: raw.trim().startsWith('{') && raw.includes('"error"'),
  frameCount: frames.length,
  events: frames.map(frame => frame.event),
  stopReason: messageDelta?.data?.delta?.stop_reason ?? null,
  toolUseCount: tools.length,
  toolUses: tools,
  hasMessageStop: frames.some(frame => frame.event === 'message_stop')
};

fs.writeFileSync(outputPath, JSON.stringify(summary, null, 2));
