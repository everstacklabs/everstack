import assert from "node:assert/strict";
import test from "node:test";

import { createRunState } from "./types.js";
import { validateRunState } from "./workflow.js";

function successfulState() {
  return {
    ...createRunState(),
    sandboxToolCallCount: 1,
    sandboxLanguage: "javascript",
    sandboxTimeoutSeconds: 240,
    sandboxExecStarted: true,
    sandboxResultObserved: true,
    sandboxExecutionSucceeded: true,
    sandboxToolResult:
      "Exit code: 0\nDuration: 12ms\n\n--- stdout ---\nresult=376\n",
  };
}

test("accepts only a completed JavaScript sandbox execution", () => {
  assert.doesNotThrow(() => validateRunState(successfulState()));
});

test("requires the qualification marker only in benchmark mode", () => {
  const state = successfulState();
  assert.doesNotThrow(() => validateRunState(state, false));
  assert.throws(
    () => validateRunState(state, true),
    /EVERSTACK_BENCHMARK_RESULT/,
  );
});

test("does not mistake a pre-execution tool success for completed code", () => {
  const state = successfulState();
  state.sandboxResultObserved = false;
  state.sandboxToolResult = "Failed to create sandbox: unavailable";

  assert.throws(() => validateRunState(state), /post-execution sandbox.result/);
});

test("rejects an unexpected tool or unsuccessful process result", () => {
  const unexpectedTool = successfulState();
  unexpectedTool.unexpectedToolNames.push("web_fetch");
  assert.throws(() => validateRunState(unexpectedTool), /unexpected tools/);

  const badExit = successfulState();
  badExit.sandboxToolResult =
    "Exit code: 1\nDuration: 8ms\n\n--- stderr ---\nboom\n";
  assert.throws(() => validateRunState(badExit), /exit code 0/);
});

test("rejects a tool timeout above the sandbox policy", () => {
  const state = successfulState();
  state.sandboxTimeoutSeconds = 300;

  assert.throws(() => validateRunState(state), /above the 240-second policy/);
});
