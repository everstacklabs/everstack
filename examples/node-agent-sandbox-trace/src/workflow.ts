import { randomUUID } from "node:crypto";

import {
  type AgentStreamEvent,
  type CreateAgentParams,
  type ErrorEvent,
  type FallbackEvent,
  type Message,
  type OtelSpan,
  type SandboxExecEvent,
  type TextDeltaEvent,
  type ToolCallEvent,
  type ToolResultEvent,
  type Turn,
  type TurnEndEvent,
} from "@everstack/node";

import { BENCHMARK_PROFILE, parseBenchmarkResult } from "./benchmark.js";
import { parseJson, toError, tracedStep, truncate } from "./trace.js";
import type { EverstackClient, RunState } from "./types.js";

export async function createExecutionBrief(
  client: EverstackClient,
  parent: OtelSpan,
  model: string,
  prompt: string,
  state: RunState,
): Promise<string> {
  const span = parent.startChild({
    name: "app.gateway.chat.completions",
    kind: "CLIENT",
    type: "GENERATION",
    model,
  });
  const messages: Message[] = [
    {
      role: "system",
      content:
        "Act as the gateway planner for a downstream code-execution agent. " +
        "In one sentence of at most 50 words, direct the agent to implement and execute the user's authoritative request exactly, preserving every requirement. " +
        "Do not restate the requirements, solve the task, or include code.",
    },
    { role: "user", content: prompt },
  ];
  span.setMessages(messages);

  try {
    const response = await client.chat.completions.create({
      model,
      messages,
      temperature: 0,
      max_tokens: 120,
    });
    const brief = response.choices[0]?.message?.content?.trim();
    if (!brief) {
      throw new Error("Gateway returned an empty execution brief.");
    }

    span.setChoices(response.choices);
    span.setUsage({
      inputTokens: response.usage.prompt_tokens,
      outputTokens: response.usage.completion_tokens,
      totalTokens: response.usage.total_tokens,
    });
    span.setModel(response.model || model);
    span.setAttribute("everstack.response_id", response.id);

    if (response.fallback_info) {
      state.gatewayFallback = response.fallback_info;
      span.setAttribute(
        "everstack.fallback_used",
        String(response.fallback_info.fallback_used),
      );
      span.setAttribute(
        "everstack.actual_model",
        response.fallback_info.actual_model || response.model,
      );
      if (response.fallback_info.fallback_reason) {
        span.setAttribute(
          "everstack.fallback_reason",
          response.fallback_info.fallback_reason,
        );
      }
    }

    const executionBrief = `${brief}\n\nAuthoritative application request (follow exactly):\n${prompt}`;
    span.setOutput(executionBrief);
    return executionBrief;
  } catch (error) {
    span.setError(error);
    throw error;
  } finally {
    await span.end();
  }
}

export async function createSandboxAgent(
  client: EverstackClient,
  parent: OtelSpan,
  model: string,
  sandboxImage: string,
  keepResources: boolean,
  state: RunState,
): Promise<string> {
  return tracedStep(
    parent,
    { name: "app.agent.create", type: "SPAN", kind: "CLIENT" },
    async (span) => {
      const sandboxPolicy = {
        enabled: true,
        image: sandboxImage,
        cpu_limit: BENCHMARK_PROFILE.sandbox.cpuLimit,
        memory_mb: BENCHMARK_PROFILE.sandbox.memoryMb,
        disk_mb: BENCHMARK_PROFILE.sandbox.diskMb,
        timeout_seconds: BENCHMARK_PROFILE.sandbox.timeoutSeconds,
        network_mode: "deny",
        allowed_hosts: [],
        env_vars: {},
        tools: ["sandbox_execute"],
        ssh_enabled: false,
      };
      const request: CreateAgentParams = {
        name: `sdk-sandbox-qualification-${Date.now()}-${randomUUID().slice(0, 8)}`,
        description:
          "Agent created by the Node SDK sandbox qualification example",
        model,
        system_prompt:
          "You are a deterministic sandbox qualification agent. Generate one compact, self-contained JavaScript program that implements every requirement in the task. " +
          "Use only Node.js built-in modules. Call sandbox_execute exactly once with language=javascript, then report its exact stdout. " +
          `Never request a tool timeout above ${BENCHMARK_PROFILE.sandbox.timeoutSeconds} seconds. ` +
          "Do not use external packages, a shell, or any other tool. A direct child_process.execFile call is allowed when the task requires it. " +
          "The task may require one outbound request specifically to confirm that the sandbox blocks it. Never claim that code ran unless sandbox_execute succeeded.",
        tools: ["sandbox_execute"],
        config: {
          api_type: "responses",
          sandbox: sandboxPolicy,
        },
        max_turns: 1,
        max_tool_calls_per_turn: 2,
        max_steps: 6,
        task_permission_mode: "always",
        lifecycle_mode: "ephemeral",
        hidden: !keepResources,
        auto_provision: false,
        sandbox_config: {
          ...sandboxPolicy,
        },
      };

      span.setInput({
        ...request,
        system_prompt: truncate(request.system_prompt),
      });
      const agent = await client.agents.create(request);
      if (!agent.id) {
        throw new Error("Agent creation response did not include an ID.");
      }
      state.agentId = agent.id;
      state.agentName = agent.name;
      span.setOutput({
        agentId: agent.id,
        name: agent.name,
        model: agent.model,
      });
      return agent.id;
    },
  );
}

export async function createAgentSession(
  client: EverstackClient,
  parent: OtelSpan,
  agentId: string,
  state: RunState,
): Promise<string> {
  return tracedStep(
    parent,
    { name: "app.agent.session.create", type: "SPAN", kind: "CLIENT" },
    async (span) => {
      span.setInput({ agentId });
      const session = await client.agents.createSession({ agent_id: agentId });
      if (!session.id) {
        throw new Error("Session creation response did not include an ID.");
      }
      state.sessionId = session.id;
      span.setOutput({ sessionId: session.id, status: session.status });
      return session.id;
    },
  );
}

export async function runAgentTurn(
  client: EverstackClient,
  parent: OtelSpan,
  sessionId: string,
  brief: string,
  agentModel: string,
  state: RunState,
): Promise<{ output: string; turn: Turn }> {
  const span = parent.startChild({
    name: "app.agent.turn",
    type: "CHAIN",
    kind: "CLIENT",
    model: agentModel,
  });
  span.setInput({ sessionId, brief });

  const toolSpans = new Map<string, OtelSpan>();
  let output = "";
  let finalTurn: Turn | undefined;
  let terminalTurnNumber: number | undefined;

  try {
    const stream = client.agents.runTurnStream({
      session_id: sessionId,
      input: brief,
      enable_streaming: true,
      enable_web_search: false,
    });

    for await (const streamEvent of stream) {
      const event = streamEvent as AgentStreamEvent;
      switch (event.type) {
        case "text_delta": {
          const textEvent = event as TextDeltaEvent;
          output += textEvent.text;
          process.stdout.write(textEvent.text);
          break;
        }

        case "tool_call": {
          const toolCallEvent = event as ToolCallEvent;
          const args = parseJson(toolCallEvent.tool_args);
          const isSandbox = toolCallEvent.tool_name === "sandbox_execute";
          const language =
            isSandbox && typeof args?.language === "string"
              ? args.language.toLowerCase()
              : "unknown";
          const timeoutSeconds =
            isSandbox && typeof args?.timeout_seconds === "number"
              ? args.timeout_seconds
              : undefined;
          const toolSpan = span.startChild({
            name: isSandbox
              ? "app.agent.tool.sandbox_execute"
              : `app.agent.tool.${toolCallEvent.tool_name || "unknown"}`,
            type: "TOOL",
            kind: "CLIENT",
          });
          toolSpan.setInput(args ?? toolCallEvent.tool_args);
          toolSpan.setAttribute(
            "agent.tool_call_id",
            toolCallEvent.tool_call_id,
          );
          toolSpan.setAttribute("agent.tool_name", toolCallEvent.tool_name);
          if (isSandbox) {
            toolSpan.setAttribute("sandbox.language", language);
            if (timeoutSeconds !== undefined) {
              toolSpan.setAttribute("sandbox.timeout_seconds", timeoutSeconds);
            }
          }
          toolSpans.set(toolCallEvent.tool_call_id, toolSpan);
          if (isSandbox) {
            state.sandboxToolCallCount += 1;
            state.sandboxLanguage = language;
            state.sandboxTimeoutSeconds = timeoutSeconds;
          } else {
            state.unexpectedToolNames.push(
              toolCallEvent.tool_name || "unknown",
            );
          }
          break;
        }

        case "sandbox.exec": {
          const sandboxEvent = event as SandboxExecEvent;
          state.sandboxExecStarted = true;
          if (sandboxEvent.sandbox_id) {
            if (
              state.sandboxId &&
              state.sandboxId !== sandboxEvent.sandbox_id
            ) {
              throw new Error(
                `Agent executed in sandbox ${sandboxEvent.sandbox_id}, expected ${state.sandboxId}.`,
              );
            }
            state.sandboxId = sandboxEvent.sandbox_id;
            for (const toolSpan of toolSpans.values()) {
              toolSpan.setAttribute("sandbox.id", sandboxEvent.sandbox_id);
            }
          }
          break;
        }

        case "sandbox.result": {
          state.sandboxResultObserved = true;
          for (const toolSpan of toolSpans.values()) {
            toolSpan.setAttribute("sandbox.result_observed", "true");
          }
          break;
        }

        case "tool_result": {
          const toolResultEvent = event as ToolResultEvent;
          const toolSpan = toolSpans.get(toolResultEvent.tool_call_id);
          if (toolSpan) {
            toolSpan.setOutput(truncate(toolResultEvent.tool_result));
            toolSpan.setAttribute(
              "agent.tool_success",
              String(toolResultEvent.tool_success),
            );
            toolSpan.setAttribute(
              "agent.tool_duration_ms",
              toolResultEvent.tool_duration_ms,
            );
            if (!toolResultEvent.tool_success) {
              toolSpan.setError(
                new Error(
                  toolResultEvent.tool_result ||
                    `${toolResultEvent.tool_name} failed`,
                ),
              );
            }
            await toolSpan.end();
            toolSpans.delete(toolResultEvent.tool_call_id);
          }
          if (toolResultEvent.tool_name === "sandbox_execute") {
            state.sandboxToolResult = toolResultEvent.tool_result;
            state.sandboxExecutionSucceeded = toolResultEvent.tool_success;
            if (
              typeof toolResultEvent.tool_duration_ms === "number" &&
              Number.isFinite(toolResultEvent.tool_duration_ms)
            ) {
              state.sandboxToolDurationMs = toolResultEvent.tool_duration_ms;
            }
          }
          break;
        }

        case "fallback": {
          const fallbackEvent = event as FallbackEvent;
          const fallback = {
            from: fallbackEvent.fallback_from_model,
            to: fallbackEvent.fallback_to_model,
            attempt: fallbackEvent.fallback_attempt,
          };
          state.agentFallbacks.push(fallback);
          span.setAttribute(
            `everstack.agent_fallback.${state.agentFallbacks.length}`,
            JSON.stringify(fallback),
          );
          break;
        }

        case "fallback.triggered":
        case "fallback.succeeded":
        case "fallback.failed": {
          state.agentRoutingEvents.push(event.type);
          span.setAttribute(
            `everstack.agent_routing_event.${state.agentRoutingEvents.length}`,
            event.type,
          );
          break;
        }

        case "turn.end": {
          const turnEndEvent = event as TurnEndEvent;
          terminalTurnNumber = turnEndEvent.turn_number;
          finalTurn = turnEndEvent.turn;
          output = turnEndEvent.turn?.assistant_output || output;
          span.setUsage({
            inputTokens: turnEndEvent.prompt_tokens,
            outputTokens: turnEndEvent.completion_tokens,
            totalTokens: turnEndEvent.total_tokens,
          });
          span.setAttribute(
            "agent.finish_reason",
            turnEndEvent.finish_reason || "",
          );
          break;
        }

        case "error": {
          const errorEvent = event as ErrorEvent;
          throw new Error(errorEvent.error || "Agent stream failed.");
        }
      }
    }

    if (!finalTurn) {
      if (terminalTurnNumber === undefined) {
        throw new Error("Agent stream ended without a turn.end event.");
      }
      finalTurn = await waitForPersistedTurn(
        client,
        sessionId,
        terminalTurnNumber,
      );
      output = finalTurn.assistant_output || output;
    }
    if (!output.trim()) {
      throw new Error("Agent completed without an assistant response.");
    }
    if (finalTurn.status !== "completed") {
      throw new Error(`Agent turn ended with status ${finalTurn.status}.`);
    }

    span.setOutput({
      output: truncate(output),
      status: finalTurn.status,
      toolCalls: finalTurn.tool_calls,
    });
    return { output, turn: finalTurn };
  } catch (error) {
    span.setError(error);
    throw error;
  } finally {
    for (const toolSpan of toolSpans.values()) {
      toolSpan.setError(new Error("Tool call ended without a result event."));
      await toolSpan.end();
    }
    await span.end();
    process.stdout.write("\n");
  }
}

async function waitForPersistedTurn(
  client: EverstackClient,
  sessionId: string,
  turnNumber: number,
): Promise<Turn> {
  const deadline = Date.now() + 10_000;
  let lastStatus = "not found";

  do {
    const session = await client.agents.getSession(sessionId);
    const turn = session.turns.find(
      (candidate) => candidate.turn_number === turnNumber,
    );
    if (turn) {
      lastStatus = turn.status;
      if (turn.status === "completed" || turn.status === "failed") return turn;
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  } while (Date.now() < deadline);

  throw new Error(
    `Turn ${turnNumber} was not persisted within 10000ms (last status: ${lastStatus}).`,
  );
}

export function validateRunState(
  state: RunState,
  requireBenchmark = false,
): void {
  if (state.sandboxToolCallCount !== 1) {
    throw new Error(
      `Expected exactly one sandbox_execute call, observed ${state.sandboxToolCallCount}.`,
    );
  }
  if (state.unexpectedToolNames.length > 0) {
    throw new Error(
      `Agent called unexpected tools: ${state.unexpectedToolNames.join(", ")}.`,
    );
  }
  if (state.sandboxLanguage !== "javascript") {
    throw new Error(
      `Expected sandbox_execute language=javascript, observed ${state.sandboxLanguage ?? "none"}.`,
    );
  }
  if (
    state.sandboxTimeoutSeconds !== undefined &&
    state.sandboxTimeoutSeconds > BENCHMARK_PROFILE.sandbox.timeoutSeconds
  ) {
    throw new Error(
      `sandbox_execute requested ${state.sandboxTimeoutSeconds} seconds, above the ${BENCHMARK_PROFILE.sandbox.timeoutSeconds}-second policy.`,
    );
  }
  if (!state.sandboxExecStarted) {
    throw new Error("Agent did not emit the pre-execution sandbox.exec event.");
  }
  if (!state.sandboxResultObserved) {
    throw new Error(
      "Agent did not emit the post-execution sandbox.result event.",
    );
  }
  if (!state.sandboxExecutionSucceeded) {
    throw new Error("sandbox_execute did not report a successful execution.");
  }
  if (!state.sandboxToolResult) {
    throw new Error("sandbox_execute returned no result payload.");
  }
  if (!/^Exit code: 0$/m.test(state.sandboxToolResult)) {
    throw new Error("sandbox_execute did not report exit code 0.");
  }
  if (!/--- stdout ---\n[\s\S]*\S/.test(state.sandboxToolResult)) {
    throw new Error("sandbox_execute did not return non-empty stdout.");
  }
  if (requireBenchmark) {
    if (
      state.sandboxTimeoutSeconds !== BENCHMARK_PROFILE.sandbox.timeoutSeconds
    ) {
      throw new Error(
        `Expected benchmark timeout_seconds=${BENCHMARK_PROFILE.sandbox.timeoutSeconds}, observed ${state.sandboxTimeoutSeconds ?? "none"}.`,
      );
    }
    state.benchmarkResult = parseBenchmarkResult(state.sandboxToolResult);
  }
}

export async function cleanupResources(
  client: EverstackClient,
  parent: OtelSpan,
  state: RunState,
  tenantId?: string,
): Promise<void> {
  const sessionId = state.sessionId;
  if (sessionId) {
    try {
      await tracedStep(
        parent,
        {
          name: "app.cleanup.session.complete",
          type: "SPAN",
          kind: "CLIENT",
        },
        async (span) => {
          span.setInput({ sessionId });
          const result = await client.agents.sessions.complete({
            tenantId: tenantId ?? "",
            sessionId,
          });
          span.setOutput({ success: result.success, message: result.message });
          if (!result.success) {
            throw new Error(result.message || "Session completion failed.");
          }
        },
      );
    } catch (error) {
      try {
        await tracedStep(
          parent,
          {
            name: "app.cleanup.session.cancel",
            type: "SPAN",
            kind: "CLIENT",
          },
          async (span) => {
            span.setInput({ sessionId });
            const result = await client.agents.sessions.cancel({ sessionId });
            span.setOutput({
              success: result.success,
              message: result.message,
              completionError: toError(error).message,
            });
            if (!result.success) {
              throw new Error(result.message || "Session cancellation failed.");
            }
          },
        );
      } catch (cancelError) {
        state.cleanupErrors.push(
          `session: ${toError(error).message}; cancel: ${toError(cancelError).message}`,
        );
      }
    }
  }

  const agentId = state.agentId;
  if (agentId) {
    try {
      await tracedStep(
        parent,
        { name: "app.cleanup.agent.delete", type: "SPAN", kind: "CLIENT" },
        async (span) => {
          span.setInput({ agentId });
          const result = await client.agents.delete(agentId);
          span.setOutput(result);
          if (!result.success) {
            throw new Error(result.message || "Agent deletion failed.");
          }
        },
      );
    } catch (error) {
      state.cleanupErrors.push(`agent: ${toError(error).message}`);
    }
  }
}
