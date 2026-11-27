import assert from "node:assert/strict";
import test from "node:test";

import {
  formatTraceparent,
  missingSpanRequirements,
  spanNameMatches,
  traceUrl,
} from "./trace.js";

test("formats a sampled W3C traceparent", () => {
  assert.equal(
    formatTraceparent("0123456789ABCDEF0123456789ABCDEF", "0123456789ABCDEF"),
    "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
  );
});

test("rejects invalid or all-zero trace identifiers", () => {
  assert.throws(
    () => formatTraceparent("0".repeat(32), "1".repeat(16)),
    /Trace ID/,
  );
  assert.throws(
    () => formatTraceparent("1".repeat(32), "0".repeat(16)),
    /Span ID/,
  );
});

test("derives the dashboard trace URL from the instance URL", () => {
  assert.equal(
    traceUrl("https://dev.example.com", "trace/id"),
    "https://dev.example.com/observability/traces?trace=trace%2Fid",
  );
});

test("matches span requirements with embedded wildcards and counts", () => {
  assert.equal(
    spanNameMatches("provider.openai.chat", "provider.*.chat"),
    true,
  );
  assert.equal(
    spanNameMatches("provider.openai.embed", "provider.*.chat"),
    false,
  );

  assert.deepEqual(
    missingSpanRequirements(
      ["provider.openai.chat", "provider.anthropic.chat", "agent.session"],
      [
        { pattern: "provider.*.chat", minimum: 2 },
        { pattern: "agent.session" },
      ],
    ),
    [],
  );
  assert.deepEqual(
    missingSpanRequirements(
      ["provider.openai.chat"],
      [{ pattern: "provider.*.chat", minimum: 2 }],
    ),
    ["provider.*.chat (1/2)"],
  );
});
