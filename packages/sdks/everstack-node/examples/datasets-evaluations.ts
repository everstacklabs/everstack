#!/usr/bin/env tsx

import { Everstack } from "../src/index.js";

async function main() {
  const apiKey = process.env.EVERSTACK_API_KEY;
  const baseUrl = process.env.EVERSTACK_GATEWAY_URL ?? "http://localhost:8089";

  if (!apiKey) {
    throw new Error("Set EVERSTACK_API_KEY before running this example.");
  }

  const client = new Everstack({ apiKey, baseUrl });

  const dataset = await client.datasets.create({
    name: "support-regression",
    description: "Regression dataset for support responses",
  });

  const datasetId = dataset.dataset?.id;
  if (!datasetId) {
    throw new Error("Dataset ID missing in response.");
  }

  await client.datasets.items.createBatch({
    datasetId,
    items: [
      { input: { query: "How do I reset my password?" } },
      { input: { query: "How do I update billing info?" } },
    ],
  });

  const run = await client.evaluations.runs.create({
    name: "support-regression-nightly",
    datasetId,
  });

  console.log("Created eval run:", run.evalRun?.id ?? "<unknown>");
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
