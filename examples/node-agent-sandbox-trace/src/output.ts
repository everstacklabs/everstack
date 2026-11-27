import { BENCHMARK_PROFILE } from "./benchmark.js";
import { traceUrl } from "./trace.js";
import type { ExampleConfig, Route, RunState } from "./types.js";

export function printIntro(
  config: ExampleConfig,
  route: Route,
  agentModel: string,
): void {
  console.log("Everstack Node SDK agent and sandbox trace example");
  console.log(`  Instance:    ${config.baseUrl}`);
  console.log(`  Request:     ${route.model}`);
  console.log(`  Agent model: ${agentModel}`);
  console.log(
    `  Provider:    ${config.provider ?? route.provider ?? "instance routing"}`,
  );
  console.log(
    `  Workload:     ${config.benchmarkMode ? "sandbox qualification suite" : config.prompt}`,
  );
  console.log(
    `  Sandbox:      ${BENCHMARK_PROFILE.sandbox.name} (${BENCHMARK_PROFILE.sandbox.cpuLimit} vCPU, ${BENCHMARK_PROFILE.sandbox.memoryMb} MiB RAM, ${BENCHMARK_PROFILE.sandbox.diskMb} MiB disk), network denied`,
  );
  console.log("");
}

export function printResult(
  config: ExampleConfig,
  route: Route,
  traceId: string,
  spanCount: number,
  scoreId: string | undefined,
  state: RunState,
): void {
  console.log("\nRun summary");
  console.log(`  Requested model: ${route.model}`);
  if (state.gatewayFallback) {
    console.log(
      `  Gateway route:   ${state.gatewayFallback.requested_model} -> ${state.gatewayFallback.actual_model}`,
    );
  } else {
    console.log("  Gateway route:   completed without reported fallback");
  }
  for (const fallback of state.agentFallbacks) {
    console.log(
      `  Agent fallback:  ${fallback.from} -> ${fallback.to} (attempt ${fallback.attempt})`,
    );
  }
  if (state.agentRoutingEvents.length > 0) {
    console.log(`  Agent routing:   ${state.agentRoutingEvents.join(", ")}`);
  }
  console.log(`  Agent name:      ${state.agentName ?? "not created"}`);
  console.log(`  Agent ID:        ${state.agentId ?? "not created"}`);
  console.log(`  Session ID:      ${state.sessionId ?? "not created"}`);
  console.log(`  Sandbox ID:      ${state.sandboxId ?? "not observed"}`);
  console.log(
    `  Sandbox result:  ${state.sandboxExecutionSucceeded && state.sandboxResultObserved ? "success" : "not successful"} (${state.sandboxToolCallCount} call${state.sandboxToolCallCount === 1 ? "" : "s"})`,
  );
  if (state.sandboxToolDurationMs !== undefined) {
    console.log(
      `  Tool duration:   ${formatDuration(state.sandboxToolDurationMs)}`,
    );
  }
  if (state.sandboxTimeoutSeconds !== undefined) {
    console.log(
      `  Tool timeout:    ${state.sandboxTimeoutSeconds} s requested`,
    );
  }
  console.log(`  Trace ID:        ${traceId}`);
  console.log(`  Trace spans:     ${spanCount || "not verified"}`);
  console.log(`  Trace URL:       ${traceUrl(config.baseUrl, traceId)}`);
  console.log(`  Score ID:        ${scoreId ?? "not submitted"}`);
  console.log(
    `  Cleanup:         ${config.keepResources ? "agent and session retained; ephemeral sandbox may be reclaimed" : state.cleanupErrors.length ? "failed" : "complete"}`,
  );
  if (state.benchmarkResult) {
    printBenchmarkResult(state.benchmarkResult);
  }
  if (state.agentOutput) {
    console.log(`\nAgent result\n${state.agentOutput}`);
  }
}

export function printHelp(): void {
  console.log(`
Usage:
  npm start
  npm start -- "Run a custom JavaScript workload and report its result."

Required environment:
  EVERSTACK_GATEWAY_URL   Live Everstack instance base URL
  EVERSTACK_API_KEY       Everstack API key

Optional environment:
  EVERSTACK_MODEL         Initial chat model, for example gpt-5.6
  EVERSTACK_PROVIDER      Explicit provider header, for example @openai
  EVERSTACK_ORG_ID        Organization or tenant ID
  EVERSTACK_USER_ID       User attribution ID
  EVERSTACK_SANDBOX_IMAGE Sandbox image
  EVERSTACK_PROMPT        Custom prompt, disables default benchmark checks
  TRACE_WAIT_MS           Trace query timeout, default 30000
  KEEP_RESOURCES          true to retain a visible agent and its session
`);
}

function printBenchmarkResult(result: RunState["benchmarkResult"]): void {
  if (!result) return;

  console.log("\nSandbox qualification");
  console.log(
    `  Overall:          PASS in ${formatDuration(result.totalDurationMs)}`,
  );
  console.log(
    `  Environment:      Node ${result.environment.node}, ${result.environment.platform}/${result.environment.arch}, ${result.environment.availableParallelism} available threads`,
  );
  console.log(
    `  Filesystem:       ${formatMib(result.storage.availableBytesBefore)} usable of ${formatMib(result.storage.totalBytes)} total`,
  );
  console.log(
    `  CPU sieve:        ${result.cpu.primeCount.toLocaleString()} primes through ${result.cpu.sieveLimit.toLocaleString()} in ${formatDuration(result.cpu.durationMs)}`,
  );
  console.log(
    `  Memory + SHA-256: ${formatMib(result.memory.bytes)} in ${formatDuration(result.memory.durationMs)} (${formatRate(result.memory.bytes, result.memory.durationMs)})`,
  );
  console.log(
    `  Disk + gzip:      ${formatMib(result.filesystem.bytes)} verified in ${formatDuration(result.filesystem.durationMs)} (${formatRate(result.filesystem.bytes, result.filesystem.durationMs)})`,
  );
  console.log(
    `  Small-file I/O:   ${result.smallFiles.count.toLocaleString()} files in ${formatDuration(result.smallFiles.durationMs)}`,
  );
  console.log(
    `  Worker threads:   ${result.workers.count} x ${formatMib(result.workers.bytesPerWorker)} in ${formatDuration(result.workers.durationMs)} (${formatRate(result.workers.count * result.workers.bytesPerWorker, result.workers.durationMs)})`,
  );
  console.log(
    `  Child process:    PASS in ${formatDuration(result.subprocess.durationMs)}`,
  );
  console.log(
    `  Network deny:     PASS in ${formatDuration(result.network.durationMs)} (${result.network.error.slice(0, 100)})`,
  );
  console.log(
    `  Peak RSS:         ${(result.resources.maxRssKb / 1024).toFixed(1)} MiB`,
  );
  console.log(
    `  Process CPU:      ${((result.resources.userCpuMicros + result.resources.systemCpuMicros) / 1_000_000).toFixed(2)} CPU-seconds`,
  );
}

function formatDuration(durationMs: number): string {
  return durationMs >= 1_000
    ? `${(durationMs / 1_000).toFixed(2)} s`
    : `${durationMs.toFixed(1)} ms`;
}

function formatMib(bytes: number): string {
  return `${(bytes / (1024 * 1024)).toFixed(0)} MiB`;
}

function formatRate(bytes: number, durationMs: number): string {
  if (durationMs <= 0) return "n/a";
  const mibPerSecond = bytes / (1024 * 1024) / (durationMs / 1_000);
  return `${mibPerSecond.toFixed(1)} MiB/s`;
}
