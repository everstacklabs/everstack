# Gateway to agent to sandbox qualification, in one trace

This live example proves the complete Everstack path with the latest Node SDK in this checkout:

1. The application sends a normal OpenAI-compatible `chat.completions.create` request.
2. Everstack routes the request through the instance's configured provider and fallback policy.
3. The response becomes a task for a temporary Everstack agent.
4. The agent lazily provisions an offline, resource-bounded ephemeral sandbox on its first tool call.
5. The agent generates a multi-dimensional qualification program and runs it once with `sandbox_execute` inside that sandbox.
6. The application propagates one W3C trace context through the SDK, reads the combined application and native Everstack span tree back, and attaches an API evaluation score.
7. The application validates the program's machine-readable measurements and invariants before it scores the run.
8. The temporary session and agent are removed by default.

Only the instance URL and API key are required. The SDK resolves the API key's organization before it creates tenant-scoped agent and sandbox resources.

The example uses `packages/sdks/everstack-node` directly, so it exercises the SDK implementation from the same checkout and commit as the gateway runtime.

Like `examples/sdk-tests/node`, this is deliberately a standalone npm consumer with its own lockfile. That catches packaging and consumer-resolution failures that a pnpm workspace link can hide.

## Run it

Use a real development instance. The example does not emulate provider, agent, sandbox, trace, or evaluation behavior locally.

```bash
pnpm install
cd examples/node-agent-sandbox-trace
cp .env.example .env
# Add EVERSTACK_GATEWAY_URL and EVERSTACK_API_KEY to .env
npm install
# Build the current workspace SDK without regenerating catalog files.
npm run sdk:build
npm run verify
npm start
```

The SDK build intentionally skips its catalog-generation prebuild step, so running the example does not rewrite tracked model metadata.

With no prompt, the default qualification suite tests all of these capabilities in one generated JavaScript execution:

- CPU correctness with a Sieve of Eratosthenes through 50,000,000, including an exact prime count and largest-prime assertion.
- 768 MiB of physically touched memory plus a full SHA-256 pass.
- 512 MiB of deterministic streaming file I/O, gzip, gunzip, and round-trip SHA-256 verification.
- Creation, read-back, hashing, and deletion of 1,000 small files.
- Four concurrent worker threads hashing 64 MiB each.
- A child Node process launched directly with `execFile`.
- An HTTPS attempt that must fail under the sandbox network-deny policy.
- Peak RSS, process CPU time, per-stage timings, and integrity digests.

The sandbox requests Everstack's managed `small` profile: 1 vCPU, 1 GiB RAM, 20 GiB root disk, and a 240-second execution ceiling. This is the largest profile available on the Basic plan and also works on higher plans. The workload deliberately touches 768 MiB of memory and performs 512 MiB of verified disk I/O, so it remains a meaningful capacity test rather than a toy example.

Pass a different application request after `--` to replace the qualification suite with a custom workload:

```bash
npm start -- "Run a matrix multiplication workload and report its checksum and timing."
```

Custom prompts still use the same isolated sandbox and generic execution checks, but the application does not expect the qualification suite's result schema.

On success, the process prints the selected model, provider routing details when present, agent and sandbox IDs, the agent result, the trace ID, a dashboard URL, and the submitted score ID.

The script fails immediately if the installed SDK does not expose the APIs this workflow requires. Rebuild the workspace SDK and reinstall dependencies if that happens.

## Configuration

| Variable                  | Required | Purpose                                                                                                               |
| ------------------------- | -------- | --------------------------------------------------------------------------------------------------------------------- |
| `EVERSTACK_GATEWAY_URL`   | Yes      | Base URL of the development instance.                                                                                 |
| `EVERSTACK_API_KEY`       | Yes      | API key sent only to Everstack. It is never logged or copied into the sandbox.                                        |
| `EVERSTACK_MODEL`         | No       | Model for the initial OpenAI-compatible request. Supports `@provider/model`; otherwise the example discovers a model. |
| `EVERSTACK_PROVIDER`      | No       | Explicit SDK provider header, such as `@openai`. When omitted, it is inferred from `EVERSTACK_MODEL` when possible.   |
| `EVERSTACK_ORG_ID`        | No       | Organization or tenant override. By default, the SDK resolves it from the API key.                                    |
| `EVERSTACK_USER_ID`       | No       | User attribution for traces and usage.                                                                                |
| `EVERSTACK_SANDBOX_IMAGE` | No       | Sandbox image. Defaults to `ghcr.io/everstacklabs/sandbox:node`.                                                      |
| `EVERSTACK_PROMPT`        | No       | Replaces the default qualification suite with a custom request.                                                       |
| `TRACE_WAIT_MS`           | No       | Maximum time to wait for the complete distributed trace to become queryable. Defaults to 30 seconds.                  |
| `KEEP_RESOURCES`          | No       | Set to `true` to retain a visible agent and its session for inspection. Defaults to cleanup.                          |

The observability dashboard is served by the same instance URL. The example derives `/observability/traces?trace=<trace-id>` from `EVERSTACK_GATEWAY_URL`, so there is no separate dashboard setting.

Provider fallback is configured on the Everstack instance, not in this script. If fallback occurs, the initial completion's `fallback_info` and the agent stream's routing event types are included in the application spans and terminal output.

The development instance must have agents, sandboxes, OTLP ingest, trace queries, and provider-call tracing enabled. The one-shot agent uses an ephemeral sandbox, so it does not consume a persistent-agent slot. The image, CPU, memory, disk, timeout, network-deny, empty environment, and SSH-disabled policy is stored in the agent config before generated code runs. Completing the session destroys its sandbox.

When `KEEP_RESOURCES=true`, the example makes the generated agent visible in the Agents list and skips session and agent cleanup. The runtime can still reclaim the ephemeral sandbox after the turn, so this flag retains the inspectable agent definition and session history, not a guaranteed live sandbox allocation.

The application-facing request uses the OpenAI-compatible Chat Completions API. The temporary agent sets `api_type: responses` because sandbox execution requires a function tool, and OpenAI rejects `gpt-5.6` function tools on Chat Completions when reasoning effort is enabled.

## What success proves

The process exits successfully only when all of these are true:

- the live gateway returns an OpenAI-compatible completion;
- the live agent completes a turn;
- the agent calls only `sandbox_execute`, exactly once, using JavaScript;
- the generated tool call cannot request more than the 240-second sandbox policy, and the default suite must request exactly 240 seconds;
- the stream emits both pre-execution `sandbox.exec` and post-execution `sandbox.result`, followed by a successful tool result containing exit code 0 and non-empty stdout;
- for the default workload, stdout contains exactly one `EVERSTACK_BENCHMARK_RESULT=` marker with valid JSON;
- the host independently validates every workload size, exact CPU result, file integrity hash, worker result, child-process result, network-deny result, timing, and resource measurement in that JSON;
- the SDK emits the application spans and reads them back under the exact root span created by this process;
- native `gateway.chat.completion`, provider, agent session, agent turn, tool, and sandbox execution spans are present under that same distributed trace ID;
- Everstack accepts the `example.e2e_success` boolean score for that trace;
- default cleanup succeeds, unless `KEEP_RESOURCES=true` was requested.

The trace contains prompts, generated code, tool results, and the final answer. Do not put secrets in the request text.

## Trace shape

```text
node-sdk.e2e.agent-sandbox
├── app.gateway.chat.completions
├── gateway.chat.completion
│   └── provider.<provider>.chat
├── app.agent.create
├── app.agent.session.create
├── app.agent.turn
│   └── app.agent.tool.sandbox_execute
├── agents.turn.stream
│   └── agent.session
│       └── agent.turn.<number>
│           ├── provider.<provider>.chat
│           ├── agent.tool.sandbox_execute
│           └── sandbox.exec.node
├── app.cleanup.session.complete
└── app.cleanup.agent.delete
```

The exact parentage can include Connect transport spans, but every required application and native execution span must resolve under the application root. The example fails instead of scoring success if the server-side spans split into another trace.
