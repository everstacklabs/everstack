import assert from "node:assert/strict";
import test from "node:test";

import { getModelMetadata, isValidModel, type AllModels } from "./models.js";

test("deprecated published model literals remain compatible", () => {
  const legacyModels: Array<[AllModels, "deprecated" | "stable"]> = [
    ["@openai/gpt-4", "deprecated"],
    ["@azure-openai/gpt-4.1", "deprecated"],
    ["@qwen/qwen2.5-72b-instruct", "stable"],
  ];

  for (const [model, status] of legacyModels) {
    assert.equal(isValidModel(model), true);
    assert.equal(getModelMetadata(model)?.status, status);
  }
});
