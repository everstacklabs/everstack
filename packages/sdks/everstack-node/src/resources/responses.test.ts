import { strict as assert } from "node:assert";
import test from "node:test";

import { Responses, type ResponseObject } from "./responses.js";

// Frames as the gateway's CreateResponse server stream sends them: full
// snapshots when the response's structure changes, and delta frames (empty
// status and output) carrying only what one item grew by.
function snapshot(status: string, output: unknown[]) {
  return {
    id: "resp_1",
    object: "response",
    createdAt: 1n,
    status,
    model: "gpt-5.5",
    output,
    metadata: {},
    temperature: 0,
    topP: 0,
    maxOutputTokens: 0,
    previousResponseId: "",
  };
}

function deltaFrame(delta: Record<string, unknown>) {
  return {
    id: "resp_1",
    object: "response",
    createdAt: 0n,
    status: "",
    model: "",
    output: [],
    metadata: {},
    temperature: 0,
    topP: 0,
    maxOutputTokens: 0,
    previousResponseId: "",
    delta: { itemId: "", contentDelta: "", argumentsDelta: "", outputIndex: 0, contentIndex: 0, ...delta },
  };
}

const message = (text: string) => ({
  id: "msg_1",
  type: "message",
  status: "in_progress",
  role: 3,
  content: [{ type: "text", data: { case: "text", value: text } }],
  callId: "",
  name: "",
  arguments: "",
  output: "",
});

const call = (args: string) => ({
  id: "call_1",
  type: "function_call",
  status: "in_progress",
  role: 0,
  content: [],
  callId: "call_1",
  name: "get_weather",
  arguments: args,
  output: "",
});

function responsesWith(frames: unknown[]) {
  const raw = {
    async *createResponse() {
      for (const f of frames) yield f;
    },
  } as unknown as ConstructorParameters<typeof Responses>[0];
  return new Responses(raw);
}

async function collect(responses: Responses) {
  const stream = await responses.create({
    model: "gpt-5.5",
    input: [{ role: "user", content: [{ type: "text", text: "hi" }] }],
    stream: true,
  });
  const events: { event: string; body: ResponseObject }[] = [];
  for await (const ev of stream) {
    events.push({ event: ev.event, body: JSON.parse(ev.data) as ResponseObject });
  }
  return events;
}

test("streamed delta frames carry their text and arguments", async () => {
  const events = await collect(
    responsesWith([
      snapshot("in_progress", []),
      snapshot("in_progress", [message("Hel")]),
      deltaFrame({ itemId: "msg_1", outputIndex: 0, contentIndex: 0, contentDelta: "lo" }),
      snapshot("in_progress", [message("Hello"), call("")]),
      deltaFrame({ itemId: "call_1", outputIndex: 1, argumentsDelta: '{"city":' }),
      deltaFrame({ itemId: "call_1", outputIndex: 1, argumentsDelta: '"Paris"}' }),
      snapshot("completed", [message("Hello"), call('{"city":"Paris"}')]),
    ]),
  );

  assert.deepEqual(
    events.map((e) => e.event),
    [
      "response.in_progress",
      "response.in_progress",
      "response.delta",
      "response.in_progress",
      "response.delta",
      "response.delta",
      "response.completed",
    ],
  );

  assert.deepEqual(events[2]!.body.delta, {
    item_id: "msg_1",
    output_index: 0,
    content_index: 0,
    text: "lo",
  });
  assert.deepEqual(events[4]!.body.delta, {
    item_id: "call_1",
    output_index: 1,
    arguments: '{"city":',
  });
  assert.deepEqual(events[5]!.body.delta, {
    item_id: "call_1",
    output_index: 1,
    arguments: '"Paris"}',
  });

  // Snapshots carry no delta, and the final one is the full response.
  for (const i of [0, 1, 3, 6]) assert.equal(events[i]!.body.delta, undefined);
  const final = events[6]!.body;
  assert.equal(final.status, "completed");
  assert.equal(final.output[0]!.content![0]!.text, "Hello");
  assert.equal(final.output[1]!.arguments, '{"city":"Paris"}');
});

test("the text of a streamed reply can be rebuilt from its snapshots and deltas", async () => {
  const events = await collect(
    responsesWith([
      snapshot("in_progress", []),
      snapshot("in_progress", [message("The")]),
      deltaFrame({ itemId: "msg_1", contentDelta: " quick" }),
      deltaFrame({ itemId: "msg_1", contentDelta: " fox" }),
      snapshot("completed", [message("The quick fox")]),
    ]),
  );

  let text = "";
  for (const { body } of events) {
    if (body.delta?.text) {
      text += body.delta.text;
    } else if (body.status !== "completed") {
      text = body.output[0]?.content?.[0]?.text ?? text;
    }
  }
  assert.equal(text, "The quick fox");
});

test("a non-streaming create returns the final snapshot", async () => {
  const responses = responsesWith([
    snapshot("in_progress", []),
    snapshot("completed", [message("done")]),
  ]);
  const res = await responses.create({
    model: "gpt-5.5",
    input: [{ role: "user", content: [{ type: "text", text: "hi" }] }],
  });
  assert.equal(res.status, "completed");
  assert.equal(res.output[0]!.content![0]!.text, "done");
  assert.equal(res.delta, undefined);
});
