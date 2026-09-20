import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const spec = JSON.parse(
  readFileSync(
    new URL("../openapi/openai-agents-rest.source.json", import.meta.url),
    "utf8",
  ),
);

test("OpenAI Agents REST reference matches the custom handler inventory and wire shapes", () => {
  const operations = Object.entries(spec.paths).flatMap(([path, item]) =>
    Object.keys(item as object).map(
      (method) => `${method.toUpperCase()} ${path}`,
    ),
  );
  assert.equal(operations.length, 21);
  for (const expected of [
    "GET /v1/openai-agents/capabilities",
    "POST /v1/openai-agents/connections",
    "PATCH /v1/openai-agents/connections/{connection-id}",
    "POST /v1/openai-agents/sessions",
    "POST /v1/openai-agents/sessions/{session-id}/input",
    "GET /v1/openai-agents/sessions/{session-id}/events",
    "POST /v1/openai-agents/sessions/{session-id}/inputs",
    "POST /v1/openai-agents/sessions/{session-id}/artifacts/export",
    "GET /v1/openai-agents/sessions/{session-id}/artifacts/{artifact-id}/download",
    "POST /v1/openai-agents/approvals/{approval-id}/resolve",
  ])
    assert.ok(operations.includes(expected), expected);

  assert.ok(spec.components.schemas.Upload.properties.content_base64);
  assert.ok(spec.components.schemas.Capabilities.properties.environment_modes);
  assert.equal(
    spec.components.schemas.Usage.properties.total_tokens.nullable,
    true,
  );
  assert.deepEqual(spec.components.schemas.CreateSession.properties.input, {});
  assert.deepEqual(spec.components.schemas.Input.properties.input, {});
  for (const item of Object.values(spec.paths) as Array<Record<string, any>>) {
    for (const operation of Object.values(item) as Array<any>) {
      if (operation.requestBody)
        assert.equal(
          operation.requestBody["x-max-body-bytes"],
          2 * 1024 * 1024,
        );
    }
  }
  assert.equal(
    spec.paths["/v1/openai-agents/sessions"].post.responses["201"].description,
    "Success",
  );
  assert.equal(
    spec.paths["/v1/openai-agents/sessions/{session-id}/input"].post.responses[
      "202"
    ].description,
    "Success",
  );
  assert.ok(
    spec.paths[
      "/v1/openai-agents/sessions/{session-id}/events"
    ].get.parameters.some((p: { name: string }) => p.name === "after"),
  );

  const handler = readFileSync(
    new URL("../../../../internal/agents/openaiapi/http.go", import.meta.url),
    "utf8",
  );
  for (const token of [
    'case "connections"',
    'case "sessions"',
    'case "approvals"',
    'p[1] == "input"',
    'p[1] == "inputs"',
    'p[1] == "artifacts"',
    'p[1] == "usage"',
  ])
    assert.ok(handler.includes(token), token);
});
