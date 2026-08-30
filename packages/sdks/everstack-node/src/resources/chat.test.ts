import { strict as assert } from "node:assert";
import test from "node:test";

import { Completions } from "./chat.js";

test("chat completions forwards tool schemas", async () => {
  // Only the tool schema is asserted, so capture just that shape rather than
  // restating the whole request type.
  let captured: { tools?: { function: { parameters: unknown } }[] } | undefined;
  const raw = {
    async *chatCompletion(request: unknown) {
      captured = request as typeof captured;
      yield {
        id: "completion-1",
        model: "gpt-5.6",
        choices: [],
        usage: { promptTokens: 1, completionTokens: 1, totalTokens: 2 },
      };
    },
  } as unknown as ConstructorParameters<typeof Completions>[0];

  const completions = new Completions<string>(raw);
  await completions.create({
    model: "gpt-5.6",
    messages: [{ role: "user", content: "Calculate 6 * 7" }],
    tools: [
      {
        type: "function",
        function: {
          name: "calculate",
          parameters: {
            type: "object",
            properties: { expression: { type: "string" } },
            required: ["expression"],
          },
        },
      },
    ],
  });

  assert.deepEqual(captured?.tools?.[0]?.function.parameters, {
    type: "object",
    properties: { expression: { type: "string" } },
    required: ["expression"],
  });
});
