import { strict as assert } from "node:assert";
import test from "node:test";

import { Datasets, Evaluations } from "./datasets.js";

test("datasets.items.createBatch forwards to createDatasetItemBatch", async () => {
  const expected = { items: [] } as const;
  const raw = {
    createDatasetItemBatch: async () => expected,
  } as unknown as ConstructorParameters<typeof Datasets>[0];

  const datasets = new Datasets(raw);
  const result = await datasets.items.createBatch({} as never);

  assert.deepEqual(result, expected);
});

test("datasets.scoreConfigs.list forwards to listScoreConfigs", async () => {
  const raw = {
    listScoreConfigs: async () => ({ scoreConfigs: [] }),
  } as unknown as ConstructorParameters<typeof Datasets>[0];

  const datasets = new Datasets(raw);
  const result = await datasets.scoreConfigs.list({} as never);

  assert.deepEqual(result.scoreConfigs, []);
});

test("evaluations.runs.compare forwards to compareEvalRuns", async () => {
  const expected = { evalRuns: [], comparison: {} } as const;
  const raw = {
    compareEvalRuns: async () => expected,
  } as unknown as ConstructorParameters<typeof Evaluations>[0];

  const evaluations = new Evaluations(raw);
  const result = await evaluations.runs.compare({} as never);

  assert.deepEqual(result, expected);
});
