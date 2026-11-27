import { strict as assert } from "node:assert";
import test from "node:test";
import { createClient, createRouterTransport } from "@connectrpc/connect";
import { toJson } from "@bufbuild/protobuf";
import { ValueSchema } from "@bufbuild/protobuf/wkt";
import { GatewayService } from "@everstack/proto/everstack/gateway/v1/gateway_service_pb.js";
import { Decisions } from "./decisions.js";
import { Completions } from "./chat.js";

test("decisions preserve arbitrary JSON state, criteria and zero-valued results", async () => {
  const transport = createRouterTransport((router) => {
    router.service(GatewayService, {
      systemOne(request) {
        assert.equal(request.model, "@laya/typed-decisions");
        assert.deepEqual(toJson(ValueSchema, request.state!), { ticket: "hi", count: 0 });
        assert.ok(request.questions.q);
        assert.deepEqual(toJson(ValueSchema, request.questions.q.criteria!), ["low", "high"]);
        return {
          model: request.model,
          answers: { q: { type: "score", score: 0, confidence: 0, probabilities: { "0": 1, "1": 0 } } },
          usage: { inputTokens: 4, outputTokens: 0 },
        };
      },
    });
  });
  const decisions = new Decisions(createClient(GatewayService, transport));
  const result = await decisions.create({
    model: "@laya/typed-decisions", state: { ticket: "hi", count: 0 },
    questions: { q: { type: "score", criteria: ["low", "high"] } },
  });
  assert.ok(result.answers.q);
  assert.equal(result.answers.q.score, 0);
  assert.equal(result.answers.q.confidence, 0);
  assert.equal(result.usage.input_tokens, 4);
});


test("typed chat clients accept and forward automatic routing", async () => {
  const transport = createRouterTransport((router) => {
    router.service(GatewayService, {
      async *chatCompletion(request) {
        assert.equal(request.model, "auto");
        yield { id: "chat-test", model: "gpt-6-sol", choices: [] };
      },
    });
  });
  const completions = new Completions(createClient(GatewayService, transport));
  const result = await completions.create({ model: "auto", stream: false, messages: [{ role: "user", content: "hi" }] });
  assert.equal(result.model, "gpt-6-sol");
});
