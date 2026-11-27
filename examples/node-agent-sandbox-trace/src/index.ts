#!/usr/bin/env node

import {
  assertSdkCapabilities,
  makeClient,
  providerNativeModel,
  readConfig,
  resolveIdentityContext,
  resolveRoute,
} from "./config.js";
import { printHelp, printIntro, printResult } from "./output.js";
import {
  countTraceNodes,
  requiredTraceSpans,
  toError,
  traceHeaders,
  truncate,
  waitForTrace,
} from "./trace.js";
import { createRunState } from "./types.js";
import {
  cleanupResources,
  createAgentSession,
  createExecutionBrief,
  createSandboxAgent,
  runAgentTurn,
  validateRunState,
} from "./workflow.js";

if (process.argv.includes("--help") || process.argv.includes("-h")) {
  printHelp();
  process.exit(0);
}

const inputConfig = readConfig();
const unscopedClient = makeClient(inputConfig);
assertSdkCapabilities(unscopedClient);
const config = await resolveIdentityContext(unscopedClient, inputConfig);
const bootstrapClient = makeClient(config);
assertSdkCapabilities(bootstrapClient);
const route = await resolveRoute(bootstrapClient, config);
const routedConfig = {
  ...config,
  provider: config.provider ?? route.provider,
};
const agentModel = providerNativeModel(route.model);

const root = bootstrapClient.traces.startSpan({
  name: "node-sdk.e2e.agent-sandbox",
  kind: "SERVER",
  type: "CHAIN",
  attributes: {
    "everstack.example": "node-agent-sandbox-trace",
    "everstack.gateway_url": config.baseUrl,
    "everstack.requested_model": route.model,
    "everstack.agent_model": agentModel,
    "everstack.provider": routedConfig.provider ?? "instance-routing",
    "everstack.trace_context_propagated": "true",
  },
});

// Every live SDK call receives this root as its W3C remote parent. Everstack's
// native gateway, provider, agent, and sandbox spans therefore join the same
// trace as the application-authored spans below.
const client = makeClient(routedConfig, traceHeaders(root));
assertSdkCapabilities(client);

printIntro(config, route, agentModel);
root.setInput({ prompt: config.prompt });

const state = createRunState();
let runError: Error | undefined;
let rootEmitted = false;

try {
  state.gatewayBrief = await createExecutionBrief(
    client,
    root,
    route.model,
    config.prompt,
    state,
  );

  state.agentId = await createSandboxAgent(
    client,
    root,
    agentModel,
    config.sandboxImage,
    config.keepResources,
    state,
  );
  state.sessionId = await createAgentSession(
    client,
    root,
    state.agentId,
    state,
  );

  const turnResult = await runAgentTurn(
    client,
    root,
    state.sessionId,
    state.gatewayBrief,
    agentModel,
    state,
  );
  state.agentOutput = turnResult.output;
  validateRunState(state, config.benchmarkMode);
} catch (error) {
  runError = toError(error);
  root.setError(runError);
} finally {
  if (!config.keepResources) {
    await cleanupResources(client, root, state, config.orgId);
  }

  if (state.cleanupErrors.length > 0 && !runError) {
    runError = new Error(
      `Run completed, but cleanup failed: ${state.cleanupErrors.join("; ")}`,
    );
    root.setError(runError);
  }

  root.setOutput({
    success: !runError,
    gatewayBrief: state.gatewayBrief,
    gatewayFallback: state.gatewayFallback,
    agentId: state.agentId,
    agentName: state.agentName,
    sessionId: state.sessionId,
    sandboxId: state.sandboxId,
    agentRoutingEvents: state.agentRoutingEvents,
    unexpectedToolNames: state.unexpectedToolNames,
    sandboxLanguage: state.sandboxLanguage,
    sandboxTimeoutSeconds: state.sandboxTimeoutSeconds,
    sandboxToolCallCount: state.sandboxToolCallCount,
    sandboxExecStarted: state.sandboxExecStarted,
    sandboxResultObserved: state.sandboxResultObserved,
    sandboxExecutionSucceeded: state.sandboxExecutionSucceeded,
    sandboxToolDurationMs: state.sandboxToolDurationMs,
    benchmarkMode: config.benchmarkMode,
    benchmarkResult: state.benchmarkResult,
    agentFallbacks: state.agentFallbacks,
    agentOutput: state.agentOutput,
    resourcesKept: config.keepResources,
    cleanupErrors: state.cleanupErrors,
    error: runError?.message,
  });

  try {
    await root.end();
    rootEmitted = true;
  } catch (error) {
    const emitError = toError(error);
    runError ??= emitError;
    console.error(`Trace emission failed: ${emitError.message}`);
  }
}

let traceSpanCount = 0;
let scoreId: string | undefined;

if (rootEmitted) {
  try {
    const traceTree = await waitForTrace(
      client,
      root.traceId,
      root.spanId,
      config.traceWaitMs,
      runError ? [] : requiredTraceSpans(config.keepResources),
    );
    traceSpanCount = countTraceNodes(traceTree.root);
  } catch (error) {
    const traceError = toError(error);
    runError ??= traceError;
    console.error(`Trace verification failed: ${traceError.message}`);
  }

  try {
    const score = await client.traces.scores.create({
      traceId: root.traceId,
      name: "example.e2e_success",
      source: "API",
      dataType: "BOOLEAN",
      booleanValue: !runError,
      comment: runError
        ? `End-to-end example failed: ${truncate(runError.message)}`
        : config.keepResources
          ? "Gateway, provider, agent, sandbox, and trace verification completed; resources were retained by request."
          : "Gateway, provider, agent, sandbox, trace verification, and cleanup completed.",
    });
    scoreId = score.score?.id;
    if (!scoreId) {
      throw new Error("Score creation response did not include a score ID.");
    }
  } catch (error) {
    const scoreError = toError(error);
    runError ??= scoreError;
    console.error(`Evaluation score submission failed: ${scoreError.message}`);
  }
}

printResult(config, route, root.traceId, traceSpanCount, scoreId, state);

if (runError) {
  console.error(`\nFAILED: ${runError.message}`);
  process.exitCode = 1;
} else {
  console.log(
    "\nPASSED: the live end-to-end path completed in one distributed trace and was scored.",
  );
}
