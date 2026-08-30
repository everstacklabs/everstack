/**
 * Agents resource
 *
 * Provides typed access to agent, session, sandbox, deployment,
 * trigger, and integration APIs.
 *
 * Two usage modes:
 *  1. Ergonomic — plain JS objects with snake_case fields:
 *       const agent = await client.agents.create({ name: "Coder", model: "gpt-4o" });
 *       const stream = await client.agents.stream(agent.id, "Fix the bug");
 *
 *  2. Raw proto — full proto types via sub-resources (definitions, sessions, etc.):
 *       const res = await client.agents.definitions.create({ tenantId: "...", ... });
 */

import type { Client } from "@connectrpc/connect";
import type { JsonObject } from "@bufbuild/protobuf";
import { AgentsService } from "@everstack/proto/everstack/agents/v1/agents_service_pb.js";
import {
  AgentMode as ProtoAgentMode,
  AgentLifecycleMode as ProtoLifecycleMode,
  TaskPermissionMode as ProtoTaskPermission,
  SessionStatus as ProtoSessionStatus,
  TurnStatus as ProtoTurnStatus,
  SandboxStatus as ProtoSandboxStatus,
  type AgentDefinition as ProtoAgentDef,
  type AgentSession as ProtoSession,
  type AgentSessionTurn as ProtoTurn,
  type AgentEvent as ProtoAgentEvent,
  type SandboxInstance as ProtoSandboxInstance,
} from "@everstack/proto/everstack/agents/v1/agents_pb.js";

import { fromConnectError } from "../errors.js";
import type {
  Agent,
  AgentMode,
  AgentStreamEvent,
  CreateAgentParams,
  CreateSandboxParams,
  CreateSandboxResult,
  CreateSessionParams,
  LifecycleMode,
  LifecycleStatus,
  ListAgentsParams,
  RunTurnParams,
  RunTurnResult,
  RunTurnStreamParams,
  SandboxLifecycleState,
  SandboxStatusLiteral,
  Session,
  SessionStatus,
  TextDeltaEvent,
  Turn,
  TurnEndEvent,
  TurnStatus,
  TaskPermission,
  UpdateAgentParams,
  SandboxInstance as SandboxInstanceType,
} from "../types/agents.js";

// ---------------------------------------------------------------------------
// Proto ↔ Ergonomic mapping helpers
// ---------------------------------------------------------------------------

function toProtoAgentMode(mode?: AgentMode): ProtoAgentMode {
  switch (mode) {
    case "primary":
      return ProtoAgentMode.PRIMARY;
    case "subagent":
      return ProtoAgentMode.SUBAGENT;
    default:
      return ProtoAgentMode.UNSPECIFIED;
  }
}

function fromProtoAgentMode(mode: ProtoAgentMode): AgentMode {
  switch (mode) {
    case ProtoAgentMode.PRIMARY:
      return "primary";
    case ProtoAgentMode.SUBAGENT:
      return "subagent";
    default:
      return "primary";
  }
}

function toProtoLifecycleMode(mode?: LifecycleMode): ProtoLifecycleMode {
  switch (mode) {
    case "ephemeral":
      return ProtoLifecycleMode.EPHEMERAL;
    case "persistent":
      return ProtoLifecycleMode.PERSISTENT;
    default:
      return ProtoLifecycleMode.UNSPECIFIED;
  }
}

function fromProtoLifecycleMode(mode: ProtoLifecycleMode): LifecycleMode {
  switch (mode) {
    case ProtoLifecycleMode.PERSISTENT:
      return "persistent";
    default:
      return "ephemeral";
  }
}

function toProtoTaskPermission(mode?: TaskPermission): ProtoTaskPermission {
  switch (mode) {
    case "ask":
      return ProtoTaskPermission.ASK;
    case "always":
      return ProtoTaskPermission.ALWAYS;
    case "deny":
      return ProtoTaskPermission.DENY;
    default:
      return ProtoTaskPermission.UNSPECIFIED;
  }
}

function fromProtoTaskPermission(mode: ProtoTaskPermission): TaskPermission {
  switch (mode) {
    case ProtoTaskPermission.ASK:
      return "ask";
    case ProtoTaskPermission.ALWAYS:
      return "always";
    case ProtoTaskPermission.DENY:
      return "deny";
    default:
      return "ask";
  }
}

function fromProtoSessionStatus(s: ProtoSessionStatus): SessionStatus {
  switch (s) {
    case ProtoSessionStatus.CREATED:
      return "created";
    case ProtoSessionStatus.RUNNING:
      return "running";
    case ProtoSessionStatus.WAITING_FOR_INPUT:
      return "waiting_for_input";
    case ProtoSessionStatus.WAITING_FOR_APPROVAL:
      return "waiting_for_approval";
    case ProtoSessionStatus.COMPLETED:
      return "completed";
    case ProtoSessionStatus.FAILED:
      return "failed";
    case ProtoSessionStatus.CANCELLED:
      return "cancelled";
    case ProtoSessionStatus.HIBERNATED:
      return "hibernated";
    default:
      return "created";
  }
}

function fromProtoTurnStatus(s: ProtoTurnStatus): TurnStatus {
  switch (s) {
    case ProtoTurnStatus.PENDING:
      return "pending";
    case ProtoTurnStatus.RUNNING:
      return "running";
    case ProtoTurnStatus.COMPLETED:
      return "completed";
    case ProtoTurnStatus.FAILED:
      return "failed";
    default:
      return "pending";
  }
}

function fromProtoSandboxStatus(s: ProtoSandboxStatus): SandboxStatusLiteral {
  switch (s) {
    case ProtoSandboxStatus.PENDING:
      return "pending";
    case ProtoSandboxStatus.RUNNING:
      return "running";
    case ProtoSandboxStatus.STOPPED:
      return "stopped";
    case ProtoSandboxStatus.FAILED:
      return "failed";
    default:
      return "pending";
  }
}

function fromProtoSandboxLifecycleState(
  s: string,
  status: SandboxStatusLiteral,
): SandboxLifecycleState {
  switch ((s || "").trim().toLowerCase()) {
    case "pending":
    case "provisioning":
    case "creating":
      return "creating";
    case "running":
      return "running";
    case "stopping":
      return "stopping";
    case "sleeping":
    case "stopped":
      return "stopped";
    case "archiving":
      return "archiving";
    case "archived":
      return "archived";
    case "reviving":
    case "restoring":
      return "restoring";
    case "terminating":
    case "deleting":
      return "deleting";
    case "terminated":
    case "deleted":
      return "deleted";
    case "failed":
    case "error":
      return "failed";
    default:
      switch (status) {
        case "running":
          return "running";
        case "stopped":
          return "stopped";
        case "failed":
          return "failed";
        default:
          return "creating";
      }
  }
}

const LIFECYCLE_STATUS_MAP: Record<number, LifecycleStatus> = {
  1: "created",
  2: "provisioning",
  3: "running",
  4: "sleeping",
  5: "waking",
  6: "failed",
  7: "terminated",
  8: "idle",
};

function ts(t: { seconds?: bigint } | undefined): string {
  if (!t || !t.seconds) return "";
  return new Date(Number(t.seconds) * 1000).toISOString();
}

function tsOrNull(t: { seconds?: bigint } | undefined): string | null {
  if (!t || !t.seconds || t.seconds === 0n) return null;
  return new Date(Number(t.seconds) * 1000).toISOString();
}

function structToRecord(s: unknown): Record<string, unknown> | null {
  if (!s) return null;
  if (typeof s === "object" && s !== null && "fields" in s) {
    const fields = (s as { fields: Record<string, unknown> }).fields;
    const result: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(fields)) {
      result[k] = v;
    }
    return result;
  }
  if (typeof s === "object" && s !== null) {
    return s as Record<string, unknown>;
  }
  return null;
}

function recordToJsonObject(
  value: Record<string, unknown> | undefined,
): JsonObject | undefined {
  if (value === undefined) return undefined;
  return value as JsonObject;
}

// ---------------------------------------------------------------------------
// Transform proto → ergonomic
// ---------------------------------------------------------------------------

function fromProtoAgent(p: ProtoAgentDef): Agent {
  return {
    id: p.id,
    tenant_id: p.tenantId,
    name: p.name,
    description: p.description,
    model: p.model,
    system_prompt: p.systemPrompt,
    tools: [...p.tools],
    config: structToRecord(p.config),
    max_turns: p.maxTurns,
    max_tool_calls_per_turn: p.maxToolCallsPerTurn,
    enabled: p.enabled,
    created_at: ts(p.createdAt),
    updated_at: ts(p.updatedAt),
    mode: fromProtoAgentMode(p.mode),
    max_steps: p.maxSteps ?? null,
    task_permission_mode: fromProtoTaskPermission(p.taskPermissionMode),
    hidden: p.hidden,
    color: p.color ?? null,
    working_directory: p.workingDirectory ?? null,
    mention_alias: p.mentionAlias ?? null,
    lifecycle_mode: fromProtoLifecycleMode(p.lifecycleMode),
    lifecycle_status: LIFECYCLE_STATUS_MAP[p.lifecycleStatus] ?? "created",
    icon: p.icon ?? null,
    sandbox_id: p.sandboxId,
    primary_session_id: p.primarySessionId,
    identity: p.identity
      ? {
          soul_md: p.identity.soulMd,
          identity_md: p.identity.identityMd,
          user_md: p.identity.userMd,
          role_md: p.identity.roleMd,
        }
      : { soul_md: "", identity_md: "", user_md: "", role_md: "" },
    sandbox_config: p.sandboxConfig
      ? {
          image: p.sandboxConfig.image,
          cpu_limit: p.sandboxConfig.cpuLimit,
          memory_mb: Number(p.sandboxConfig.memoryMb),
          disk_mb: Number(p.sandboxConfig.diskMb),
          timeout_seconds: p.sandboxConfig.timeoutSeconds,
          network_mode: p.sandboxConfig.networkMode,
          ssh_enabled: p.sandboxConfig.sshEnabled,
          git_repo_url: p.sandboxConfig.gitRepoUrl,
          git_branch: p.sandboxConfig.gitBranch,
        }
      : {},
  };
}

function fromProtoTurn(p: ProtoTurn): Turn {
  return {
    id: p.id,
    session_id: p.sessionId,
    turn_number: p.turnNumber,
    status: fromProtoTurnStatus(p.status),
    user_input: p.userInput,
    assistant_output: p.assistantOutput,
    tool_calls: p.toolCalls,
    prompt_tokens: p.promptTokens,
    completion_tokens: p.completionTokens,
    total_tokens: p.totalTokens,
    latency_ms: Number(p.latencyMs),
    error: p.error,
    created_at: ts(p.createdAt),
    completed_at: tsOrNull(p.completedAt),
    cache_read_input_tokens: p.cacheReadInputTokens,
    cache_write_input_tokens: p.cacheWriteInputTokens,
  };
}

function fromProtoSession(p: ProtoSession): Session {
  return {
    id: p.id,
    tenant_id: p.tenantId,
    agent_id: p.agentId,
    status: fromProtoSessionStatus(p.status),
    turn_count: p.turnCount,
    total_tokens: p.totalTokens,
    metadata: structToRecord(p.metadata),
    created_at: ts(p.createdAt),
    updated_at: ts(p.updatedAt),
    completed_at: tsOrNull(p.completedAt),
    summary: p.summary,
    turns: p.turns.map(fromProtoTurn),
  };
}

function fromProtoSandboxInstance(
  p: ProtoSandboxInstance,
): SandboxInstanceType {
  const status = fromProtoSandboxStatus(p.status);
  return {
    id: p.id,
    session_id: p.sessionId,
    tenant_id: p.tenantId,
    backend: p.backend,
    container_id: p.containerId,
    image: p.image,
    status,
    created_at: ts(p.createdAt),
    expires_at: ts(p.expiresAt),
    name: p.name,
    last_used_at: tsOrNull(p.lastUsedAt),
    idle_retention_secs: p.idleRetentionSecs,
    keep_warm: p.keepWarm,
    git_repo_url: p.gitRepoUrl,
    git_branch: p.gitBranch,
    git_commit_sha: p.gitCommitSha,
    lifecycle_state: fromProtoSandboxLifecycleState(p.lifecycleState, status),
    ssh_enabled: p.sshEnabled,
    persistent: p.persistent,
    agent_id: p.agentId,
    short_code: p.shortCode,
    agent_healthy:
      ((p as Record<string, unknown>).agentHealthy as boolean) ?? true,
  };
}

// ---------------------------------------------------------------------------
// AgentStream — typed event stream wrapper
// ---------------------------------------------------------------------------

function classifyEvent(raw: ProtoAgentEvent): AgentStreamEvent {
  const base = {
    session_id: raw.sessionId,
    turn_number: raw.turnNumber,
  };
  const t = raw.type;

  if (t === "text_delta" || t === "text.delta") {
    return { ...base, type: "text_delta" as const, text: raw.textDelta };
  }
  if (t === "tool_call" || t === "tool_call.start") {
    return {
      ...base,
      type: "tool_call" as const,
      tool_call_id: raw.toolCallId,
      tool_name: raw.toolName,
      tool_args: raw.toolArgs,
    };
  }
  if (t === "tool_result" || t === "tool_call.end") {
    return {
      ...base,
      type: "tool_result" as const,
      tool_call_id: raw.toolCallId,
      tool_name: raw.toolName,
      tool_result: raw.toolResult,
      tool_success: raw.toolSuccess,
      tool_duration_ms: Number(raw.toolDurationMs),
    };
  }
  if (t === "review_pending" || t === "approval.requested") {
    return {
      ...base,
      type: "review_pending" as const,
      review_id: raw.reviewId,
      pending_tool_calls: raw.pendingToolCalls.map((tc) => ({
        tool_call_id: tc.toolCallId,
        tool_name: tc.toolName,
        tool_args: tc.toolArgs,
      })),
    };
  }
  if (t === "review_resolved" || t === "approval.resolved") {
    return {
      ...base,
      type: "review_resolved" as const,
      review_id: raw.reviewId,
      approval_action: raw.approvalAction,
    };
  }
  if (t === "turn.end") {
    return {
      ...base,
      type: "turn.end" as const,
      turn: raw.turn ? fromProtoTurn(raw.turn) : undefined,
      finish_reason: raw.finishReason,
      prompt_tokens: raw.promptTokens,
      completion_tokens: raw.completionTokens,
      total_tokens: raw.totalTokens,
      cache_read_tokens: raw.cacheReadTokens,
      cache_write_tokens: raw.cacheWriteTokens,
    };
  }
  if (t === "turn.start") {
    return { ...base, type: "turn.start" as const };
  }
  if (t === "sandbox.exec") {
    return {
      ...base,
      type: "sandbox.exec" as const,
      sandbox_id: raw.sandboxId,
      sandbox_exit_code: raw.sandboxExitCode,
      sandbox_duration_ms: Number(raw.sandboxDurationMs),
    };
  }
  if (t === "fallback") {
    return {
      ...base,
      type: "fallback" as const,
      fallback_from_model: raw.fallbackFromModel,
      fallback_to_model: raw.fallbackToModel,
      fallback_attempt: raw.fallbackAttempt,
    };
  }
  if (t === "user_input_request" || t === "ask_user") {
    return {
      ...base,
      type: "user_input_request" as const,
      user_input_id: raw.userInputId,
    };
  }
  if (t === "error" || t.endsWith(".error")) {
    return {
      ...base,
      type: "error" as const,
      error: raw.error,
      source_type: t,
    };
  }
  return { ...base, type: t } as AgentStreamEvent;
}

/**
 * Typed wrapper around the raw `RunTurnStream` AsyncIterable.
 *
 * Yields discriminated-union `AgentStreamEvent` objects instead of
 * the flat proto `AgentEvent` message.
 *
 * @example
 * ```ts
 * const stream = client.agents.stream(agentId, "hello");
 * for await (const event of stream) {
 *   if (event.type === "text_delta") process.stdout.write(event.text);
 * }
 * // or just get the text:
 * for await (const text of stream.text()) process.stdout.write(text);
 * // or await the final turn:
 * const turn = await stream.finalTurn();
 * ```
 */
export class AgentStream implements AsyncIterable<AgentStreamEvent> {
  private _raw: AsyncIterable<ProtoAgentEvent>;
  private _buffer: AgentStreamEvent[] = [];
  private _done = false;
  private _finalTurnResolve?: (turn: Turn) => void;
  private _finalTurnReject?: (err: unknown) => void;
  private _finalTurnPromise: Promise<Turn>;
  private _consumed = false;
  private _finalTurnSettled = false;

  /** @internal */
  constructor(raw: AsyncIterable<ProtoAgentEvent>) {
    this._raw = raw;
    this._finalTurnPromise = new Promise<Turn>((resolve, reject) => {
      this._finalTurnResolve = resolve;
      this._finalTurnReject = reject;
    });
    // Suppress unhandled-rejection warnings when callers only iterate events.
    // The original promise still rejects for callers of finalTurn().
    void this._finalTurnPromise.catch(() => undefined);
  }

  async *[Symbol.asyncIterator](): AsyncIterator<AgentStreamEvent> {
    if (this._consumed) {
      for (const evt of this._buffer) yield evt;
      return;
    }
    this._consumed = true;
    try {
      for await (const raw of this._raw) {
        const evt = classifyEvent(raw);
        this._buffer.push(evt);
        if (evt.type === "turn.end" && (evt as TurnEndEvent).turn) {
          this._finalTurnSettled = true;
          this._finalTurnResolve?.((evt as TurnEndEvent).turn!);
        }
        yield evt;
      }
      this._done = true;
      if (!this._finalTurnSettled) {
        this._finalTurnSettled = true;
        this._finalTurnReject?.(
          new Error("Agent stream ended without an embedded final turn."),
        );
      }
    } catch (err) {
      if (!this._finalTurnSettled) {
        this._finalTurnSettled = true;
        this._finalTurnReject?.(err);
      }
      throw fromConnectError(err);
    }
  }

  /**
   * Yields only text delta strings — ideal for simple display loops.
   */
  async *text(): AsyncIterable<string> {
    for await (const evt of this) {
      if (evt.type === "text_delta") yield (evt as TextDeltaEvent).text;
    }
  }

  /**
   * Resolves to the completed Turn when the stream ends.
   * Can be awaited in parallel with iteration.
   */
  finalTurn(): Promise<Turn> {
    return this._finalTurnPromise;
  }
}

// ---------------------------------------------------------------------------
// Raw proto types (unchanged from original)
// ---------------------------------------------------------------------------

type AgentsClient = Client<typeof AgentsService>;

type MethodInput<K extends keyof AgentsClient> = Parameters<AgentsClient[K]>[0];
type MethodOptions<K extends keyof AgentsClient> = Parameters<
  AgentsClient[K]
>[1];
type MethodOutput<K extends keyof AgentsClient> = Awaited<
  ReturnType<AgentsClient[K]>
>;

/** REST transport options for the non-RPC sandbox surfaces. */
export interface AgentsRestOptions {
  baseUrl: string;
  apiKey: string;
  tenantId?: string;
  headers?: Record<string, string>;
}

/** A directory entry returned by sandboxes.fs.list. */
export interface SandboxFileInfo {
  name: string;
  path?: string;
  size?: number;
  is_dir?: boolean;
  mode?: string;
  mod_time?: string;
}

/** Result of a synchronous session command execution. */
export interface SandboxSessionCommandResult {
  session_id: string;
  command_id: string;
  exit_code?: number;
  timed_out?: boolean;
  output?: string;
  running?: boolean;
  pid?: string;
}

/** Status of an asynchronous session command. */
export interface SandboxSessionCommandStatus {
  session_id: string;
  command_id: string;
  running: boolean;
  exit_code?: string;
}

// ---------------------------------------------------------------------------
// Agents resource class
// ---------------------------------------------------------------------------

export class Agents {
  /** Raw generated Connect client for advanced usage */
  readonly raw: AgentsClient;

  /** @internal tenant_id from client options */
  private _tenantId: string;

  // -----------------------------------------------------------------------
  // Sub-resource objects — raw proto passthrough (unchanged)
  // -----------------------------------------------------------------------

  readonly definitions = {
    create: (
      request: MethodInput<"createAgent">,
      options?: MethodOptions<"createAgent">,
    ) => this._callRaw("createAgent", request, options),
    get: (
      request: MethodInput<"getAgent">,
      options?: MethodOptions<"getAgent">,
    ) => this._callRaw("getAgent", request, options),
    list: (
      request: MethodInput<"listAgents">,
      options?: MethodOptions<"listAgents">,
    ) => this._callRaw("listAgents", request, options),
    update: (
      request: MethodInput<"updateAgent">,
      options?: MethodOptions<"updateAgent">,
    ) => this._callRaw("updateAgent", request, options),
    delete: (
      request: MethodInput<"deleteAgent">,
      options?: MethodOptions<"deleteAgent">,
    ) => this._callRaw("deleteAgent", request, options),
    importFromOpencode: (
      request: MethodInput<"importAgentFromOpencode">,
      options?: MethodOptions<"importAgentFromOpencode">,
    ) => this._callRaw("importAgentFromOpencode", request, options),
    exportToOpencode: (
      request: MethodInput<"exportAgentToOpencode">,
      options?: MethodOptions<"exportAgentToOpencode">,
    ) => this._callRaw("exportAgentToOpencode", request, options),
  };

  readonly sessions = {
    create: (
      request: MethodInput<"createSession">,
      options?: MethodOptions<"createSession">,
    ) => this._callRaw("createSession", request, options),
    get: (
      request: MethodInput<"getSession">,
      options?: MethodOptions<"getSession">,
    ) => this._callRaw("getSession", request, options),
    list: (
      request: MethodInput<"listSessions">,
      options?: MethodOptions<"listSessions">,
    ) => this._callRaw("listSessions", request, options),
    runTurn: (
      request: MethodInput<"runTurn">,
      options?: MethodOptions<"runTurn">,
    ) => this._callRaw("runTurn", request, options),
    runTurnStream: (
      request: MethodInput<"runTurnStream">,
      options?: MethodOptions<"runTurnStream">,
    ) => this.raw.runTurnStream(request, options),
    steer: (
      request: MethodInput<"steerSession">,
      options?: MethodOptions<"steerSession">,
    ) => this._callRaw("steerSession", request, options),
    cancel: (
      request: MethodInput<"cancelSession">,
      options?: MethodOptions<"cancelSession">,
    ) => this._callRaw("cancelSession", request, options),
    complete: (
      request: MethodInput<"completeSession">,
      options?: MethodOptions<"completeSession">,
    ) => this._callRaw("completeSession", request, options),
  };

  readonly reviews = {
    submit: (
      request: MethodInput<"submitReview">,
      options?: MethodOptions<"submitReview">,
    ) => this._callRaw("submitReview", request, options),
    get: (
      request: MethodInput<"getReview">,
      options?: MethodOptions<"getReview">,
    ) => this._callRaw("getReview", request, options),
    list: (
      request: MethodInput<"listReviews">,
      options?: MethodOptions<"listReviews">,
    ) => this._callRaw("listReviews", request, options),
  };

  readonly sandboxes = {
    create: (
      request: MethodInput<"createSandbox">,
      options?: MethodOptions<"createSandbox">,
    ) => this._callRaw("createSandbox", request, options),
    getOverview: (
      request: MethodInput<"getSandboxOverview">,
      options?: MethodOptions<"getSandboxOverview">,
    ) => this._callRaw("getSandboxOverview", request, options),
    listInstances: (
      request: MethodInput<"listSandboxInstances">,
      options?: MethodOptions<"listSandboxInstances">,
    ) => this._callRaw("listSandboxInstances", request, options),
    getInstance: (
      request: MethodInput<"getSandboxInstance">,
      options?: MethodOptions<"getSandboxInstance">,
    ) => this._callRaw("getSandboxInstance", request, options),
    destroy: (
      request: MethodInput<"destroySandbox">,
      options?: MethodOptions<"destroySandbox">,
    ) => this._callRaw("destroySandbox", request, options),
    listExecutions: (
      request: MethodInput<"listSandboxExecutions">,
      options?: MethodOptions<"listSandboxExecutions">,
    ) => this._callRaw("listSandboxExecutions", request, options),
    getStats: (
      request: MethodInput<"getSandboxStats">,
      options?: MethodOptions<"getSandboxStats">,
    ) => this._callRaw("getSandboxStats", request, options),
    recreate: (
      request: MethodInput<"recreateSandbox">,
      options?: MethodOptions<"recreateSandbox">,
    ) => this._callRaw("recreateSandbox", request, options),
    listTemplates: (
      request: MethodInput<"listSandboxTemplates">,
      options?: MethodOptions<"listSandboxTemplates">,
    ) => this._callRaw("listSandboxTemplates", request, options),
    getTemplate: (
      request: MethodInput<"getSandboxTemplate">,
      options?: MethodOptions<"getSandboxTemplate">,
    ) => this._callRaw("getSandboxTemplate", request, options),
    listEvents: (
      request: MethodInput<"listSandboxEvents">,
      options?: MethodOptions<"listSandboxEvents">,
    ) => this._callRaw("listSandboxEvents", request, options),
    getSpawnTree: (
      request: MethodInput<"getSpawnTree">,
      options?: MethodOptions<"getSpawnTree">,
    ) => this._callRaw("getSpawnTree", request, options),
    listSpawnNodes: (
      request: MethodInput<"listSpawnNodes">,
      options?: MethodOptions<"listSpawnNodes">,
    ) => this._callRaw("listSpawnNodes", request, options),
    exposePort: (
      request: MethodInput<"exposePort">,
      options?: MethodOptions<"exposePort">,
    ) => this._callRaw("exposePort", request, options),
    unexposePort: (
      request: MethodInput<"unexposePort">,
      options?: MethodOptions<"unexposePort">,
    ) => this._callRaw("unexposePort", request, options),
    listExposedPorts: (
      request: MethodInput<"listExposedPorts">,
      options?: MethodOptions<"listExposedPorts">,
    ) => this._callRaw("listExposedPorts", request, options),
    detectListeningPorts: (
      request: MethodInput<"detectListeningPorts">,
      options?: MethodOptions<"detectListeningPorts">,
    ) => this._callRaw("detectListeningPorts", request, options),
    stop: (
      request: MethodInput<"stopSandbox">,
      options?: MethodOptions<"stopSandbox">,
    ) => this._callRaw("stopSandbox", request, options),
    revive: (
      request: MethodInput<"reviveSandbox">,
      options?: MethodOptions<"reviveSandbox">,
    ) => this._callRaw("reviveSandbox", request, options),
    terminate: (
      request: MethodInput<"terminateSandbox">,
      options?: MethodOptions<"terminateSandbox">,
    ) => this._callRaw("terminateSandbox", request, options),
    updateAutoIntervals: (
      request: MethodInput<"updateSandboxAutoIntervals">,
      options?: MethodOptions<"updateSandboxAutoIntervals">,
    ) => this._callRaw("updateSandboxAutoIntervals", request, options),

    // Daytona-style lifecycle verbs (REST). `start` resumes a stopped
    // or archived sandbox; `archive` moves a stopped sandbox's
    // filesystem to object storage; `delete` destroys it; `recover`
    // re-enters convergence from the error state. All are async:
    // the response carries the accepted state and convergence follows.
    start: (sandboxId: string) =>
      this._restJSON("POST", `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/start`),
    archive: (sandboxId: string) =>
      this._restJSON("POST", `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/archive`),
    delete: (sandboxId: string) =>
      this._restJSON("DELETE", `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}`),
    recover: (sandboxId: string) =>
      this._restJSON("POST", `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/recover`),

    // File system operations inside a sandbox (Daytona fs parity).
    fs: {
      list: (sandboxId: string, path?: string) =>
        this._restJSON(
          "GET",
          `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/fs/list${path ? `?path=${encodeURIComponent(path)}` : ""}`,
        ) as unknown as Promise<{ path: string; files: SandboxFileInfo[] }>,
      upload: (sandboxId: string, path: string, content: Uint8Array | string) =>
        this._restJSON(
          "POST",
          `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/fs/upload?path=${encodeURIComponent(path)}`,
          { rawBody: typeof content === "string" ? new TextEncoder().encode(content) : content },
        ) as unknown as Promise<{ path: string; size_bytes: number }>,
      download: async (sandboxId: string, path: string): Promise<Uint8Array> => {
        const res = await this._restFetch(
          "GET",
          `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/fs/download?path=${encodeURIComponent(path)}`,
        );
        return new Uint8Array(await res.arrayBuffer());
      },
      mkdir: (sandboxId: string, path: string, mode?: string) =>
        this._restJSON("POST", `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/fs/mkdir`, {
          json: { path, mode },
        }),
      delete: (sandboxId: string, path: string, recursive = false) =>
        this._restJSON("POST", `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/fs/delete`, {
          json: { path, recursive },
        }),
      move: (sandboxId: string, source: string, destination: string) =>
        this._restJSON("POST", `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/fs/move`, {
          json: { source, destination },
        }),
      setPermissions: (
        sandboxId: string,
        path: string,
        opts: { mode?: string; owner?: string; group?: string },
      ) =>
        this._restJSON("POST", `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/fs/permissions`, {
          json: { path, ...opts },
        }),
    },

    // Persistent exec sessions (Daytona process API). Shell state
    // (cwd + exported env) carries across commands within a session.
    process: {
      createSession: (sandboxId: string, sessionId?: string) =>
        this._restJSON("POST", `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/process/sessions`, {
          json: sessionId ? { session_id: sessionId } : {},
        }) as unknown as Promise<{ session_id: string }>,
      listSessions: (sandboxId: string) =>
        this._restJSON("GET", `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/process/sessions`) as unknown as Promise<{
          sessions: string[];
        }>,
      deleteSession: (sandboxId: string, sessionId: string) =>
        this._restJSON(
          "DELETE",
          `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/process/sessions/${encodeURIComponent(sessionId)}`,
        ),
      executeSessionCommand: (
        sandboxId: string,
        sessionId: string,
        command: string,
        opts?: { runAsync?: boolean; timeoutSeconds?: number; env?: Record<string, string>; cwd?: string },
      ) =>
        this._restJSON(
          "POST",
          `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/process/sessions/${encodeURIComponent(sessionId)}/exec`,
          {
            json: {
              command,
              run_async: opts?.runAsync ?? false,
              timeout_seconds: opts?.timeoutSeconds,
              env: opts?.env,
              cwd: opts?.cwd,
            },
          },
        ) as unknown as Promise<SandboxSessionCommandResult>,
      getCommandStatus: (sandboxId: string, sessionId: string, commandId: string) =>
        this._restJSON(
          "GET",
          `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/process/sessions/${encodeURIComponent(sessionId)}/commands/${encodeURIComponent(commandId)}`,
        ) as unknown as Promise<SandboxSessionCommandStatus>,
      getCommandLogs: async (sandboxId: string, sessionId: string, commandId: string): Promise<string> => {
        const res = await this._restFetch(
          "GET",
          `/v1/sandbox/instances/${encodeURIComponent(sandboxId)}/process/sessions/${encodeURIComponent(sessionId)}/commands/${encodeURIComponent(commandId)}/logs`,
        );
        return res.text();
      },
    },
  };

  readonly crons = {
    create: (
      request: MethodInput<"createCron">,
      options?: MethodOptions<"createCron">,
    ) => this._callRaw("createCron", request, options),
    update: (
      request: MethodInput<"updateCron">,
      options?: MethodOptions<"updateCron">,
    ) => this._callRaw("updateCron", request, options),
    delete: (
      request: MethodInput<"deleteCron">,
      options?: MethodOptions<"deleteCron">,
    ) => this._callRaw("deleteCron", request, options),
    list: (
      request: MethodInput<"listCrons">,
      options?: MethodOptions<"listCrons">,
    ) => this._callRaw("listCrons", request, options),
  };

  readonly webhooks = {
    create: (
      request: MethodInput<"createWebhook">,
      options?: MethodOptions<"createWebhook">,
    ) => this._callRaw("createWebhook", request, options),
    delete: (
      request: MethodInput<"deleteWebhook">,
      options?: MethodOptions<"deleteWebhook">,
    ) => this._callRaw("deleteWebhook", request, options),
    list: (
      request: MethodInput<"listWebhooks">,
      options?: MethodOptions<"listWebhooks">,
    ) => this._callRaw("listWebhooks", request, options),
    listTriggerHistory: (
      request: MethodInput<"listTriggers">,
      options?: MethodOptions<"listTriggers">,
    ) => this._callRaw("listTriggers", request, options),
  };

  readonly integrations = {
    github: {
      listInstallations: (
        request: MethodInput<"listGitHubInstallations">,
        options?: MethodOptions<"listGitHubInstallations">,
      ) => this._callRaw("listGitHubInstallations", request, options),
      removeInstallation: (
        request: MethodInput<"removeGitHubInstallation">,
        options?: MethodOptions<"removeGitHubInstallation">,
      ) => this._callRaw("removeGitHubInstallation", request, options),
      linkInstallation: (
        request: MethodInput<"linkGitHubInstallation">,
        options?: MethodOptions<"linkGitHubInstallation">,
      ) => this._callRaw("linkGitHubInstallation", request, options),
      listRepositories: (
        request: MethodInput<"listGitHubRepositories">,
        options?: MethodOptions<"listGitHubRepositories">,
      ) => this._callRaw("listGitHubRepositories", request, options),
      listBranches: (
        request: MethodInput<"listGitHubBranches">,
        options?: MethodOptions<"listGitHubBranches">,
      ) => this._callRaw("listGitHubBranches", request, options),
    },
  };

  readonly ssh = {
    addKey: (
      request: MethodInput<"addSSHKey">,
      options?: MethodOptions<"addSSHKey">,
    ) => this._callRaw("addSSHKey", request, options),
    listKeys: (
      request: MethodInput<"listSSHKeys">,
      options?: MethodOptions<"listSSHKeys">,
    ) => this._callRaw("listSSHKeys", request, options),
    deleteKey: (
      request: MethodInput<"deleteSSHKey">,
      options?: MethodOptions<"deleteSSHKey">,
    ) => this._callRaw("deleteSSHKey", request, options),
    grantSandboxAccess: (
      request: MethodInput<"grantSandboxSSHAccess">,
      options?: MethodOptions<"grantSandboxSSHAccess">,
    ) => this._callRaw("grantSandboxSSHAccess", request, options),
    revokeSandboxAccess: (
      request: MethodInput<"revokeSandboxSSHAccess">,
      options?: MethodOptions<"revokeSandboxSSHAccess">,
    ) => this._callRaw("revokeSandboxSSHAccess", request, options),
    getSandboxInfo: (
      request: MethodInput<"getSandboxSSHInfo">,
      options?: MethodOptions<"getSandboxSSHInfo">,
    ) => this._callRaw("getSandboxSSHInfo", request, options),
  };

  readonly memories = {
    setupBackend: (
      request: MethodInput<"setupMemory">,
      options?: MethodOptions<"setupMemory">,
    ) => this._callRaw("setupMemory", request, options),
    list: (
      request: MethodInput<"listAgentMemories">,
      options?: MethodOptions<"listAgentMemories">,
    ) => this._callRaw("listAgentMemories", request, options),
    create: (
      request: MethodInput<"createAgentMemory">,
      options?: MethodOptions<"createAgentMemory">,
    ) => this._callRaw("createAgentMemory", request, options),
    update: (
      request: MethodInput<"updateAgentMemory">,
      options?: MethodOptions<"updateAgentMemory">,
    ) => this._callRaw("updateAgentMemory", request, options),
    deactivate: (
      request: MethodInput<"deactivateAgentMemory">,
      options?: MethodOptions<"deactivateAgentMemory">,
    ) => this._callRaw("deactivateAgentMemory", request, options),
    delete: (
      request: MethodInput<"deleteAgentMemory">,
      options?: MethodOptions<"deleteAgentMemory">,
    ) => this._callRaw("deleteAgentMemory", request, options),
  };

  readonly deployments = {
    create: (
      request: MethodInput<"deployAgent">,
      options?: MethodOptions<"deployAgent">,
    ) => this._callRaw("deployAgent", request, options),
    list: (
      request: MethodInput<"listDeployments">,
      options?: MethodOptions<"listDeployments">,
    ) => this._callRaw("listDeployments", request, options),
    get: (
      request: MethodInput<"getDeployment">,
      options?: MethodOptions<"getDeployment">,
    ) => this._callRaw("getDeployment", request, options),
    update: (
      request: MethodInput<"updateDeployment">,
      options?: MethodOptions<"updateDeployment">,
    ) => this._callRaw("updateDeployment", request, options),
    createKey: (
      request: MethodInput<"createDeploymentKey">,
      options?: MethodOptions<"createDeploymentKey">,
    ) => this._callRaw("createDeploymentKey", request, options),
    listKeys: (
      request: MethodInput<"listDeploymentKeys">,
      options?: MethodOptions<"listDeploymentKeys">,
    ) => this._callRaw("listDeploymentKeys", request, options),
    revokeKey: (
      request: MethodInput<"revokeDeploymentKey">,
      options?: MethodOptions<"revokeDeploymentKey">,
    ) => this._callRaw("revokeDeploymentKey", request, options),
    listInvocations: (
      request: MethodInput<"listDeploymentInvocations">,
      options?: MethodOptions<"listDeploymentInvocations">,
    ) => this._callRaw("listDeploymentInvocations", request, options),
  };

  readonly triggers = {
    create: (
      request: MethodInput<"createAgentTrigger">,
      options?: MethodOptions<"createAgentTrigger">,
    ) => this._callRaw("createAgentTrigger", request, options),
    list: (
      request: MethodInput<"listAgentTriggers">,
      options?: MethodOptions<"listAgentTriggers">,
    ) => this._callRaw("listAgentTriggers", request, options),
    get: (
      request: MethodInput<"getAgentTrigger">,
      options?: MethodOptions<"getAgentTrigger">,
    ) => this._callRaw("getAgentTrigger", request, options),
    update: (
      request: MethodInput<"updateAgentTrigger">,
      options?: MethodOptions<"updateAgentTrigger">,
    ) => this._callRaw("updateAgentTrigger", request, options),
    delete: (
      request: MethodInput<"deleteAgentTrigger">,
      options?: MethodOptions<"deleteAgentTrigger">,
    ) => this._callRaw("deleteAgentTrigger", request, options),
    test: (
      request: MethodInput<"testAgentTrigger">,
      options?: MethodOptions<"testAgentTrigger">,
    ) => this._callRaw("testAgentTrigger", request, options),
    listExecutions: (
      request: MethodInput<"listAgentTriggerExecutions">,
      options?: MethodOptions<"listAgentTriggerExecutions">,
    ) => this._callRaw("listAgentTriggerExecutions", request, options),
  };

  readonly lifecycle = {
    provision: (
      request: MethodInput<"provisionAgent">,
      options?: MethodOptions<"provisionAgent">,
    ) => this._callRaw("provisionAgent", request, options),
    sleep: (
      request: MethodInput<"sleepAgent">,
      options?: MethodOptions<"sleepAgent">,
    ) => this._callRaw("sleepAgent", request, options),
    wake: (
      request: MethodInput<"wakeAgent">,
      options?: MethodOptions<"wakeAgent">,
    ) => this._callRaw("wakeAgent", request, options),
  };

  readonly links = {
    create: (
      request: MethodInput<"createAgentLink">,
      options?: MethodOptions<"createAgentLink">,
    ) => this._callRaw("createAgentLink", request, options),
    list: (
      request: MethodInput<"listAgentLinks">,
      options?: MethodOptions<"listAgentLinks">,
    ) => this._callRaw("listAgentLinks", request, options),
    delete: (
      request: MethodInput<"deleteAgentLink">,
      options?: MethodOptions<"deleteAgentLink">,
    ) => this._callRaw("deleteAgentLink", request, options),
  };

  readonly channels = {
    bind: (
      request: MethodInput<"bindAgentChannel">,
      options?: MethodOptions<"bindAgentChannel">,
    ) => this._callRaw("bindAgentChannel", request, options),
    unbind: (
      request: MethodInput<"unbindAgentChannel">,
      options?: MethodOptions<"unbindAgentChannel">,
    ) => this._callRaw("unbindAgentChannel", request, options),
    list: (
      request: MethodInput<"listAgentChannelBindings">,
      options?: MethodOptions<"listAgentChannelBindings">,
    ) => this._callRaw("listAgentChannelBindings", request, options),
  };

  // -----------------------------------------------------------------------
  // Constructor
  // -----------------------------------------------------------------------

  /** @internal */
  constructor(client: AgentsClient, tenantId?: string, rest?: AgentsRestOptions) {
    this.raw = client;
    this._tenantId = tenantId ?? "";
    this._rest = rest;
  }

  /** @internal REST transport config for the non-RPC sandbox surfaces
   * (fs, process sessions, lifecycle verbs). Mirrors the Memory
   * resource's pattern. */
  private _rest?: AgentsRestOptions;

  /** @internal */
  private async _restFetch(
    method: string,
    path: string,
    opts?: { json?: unknown; rawBody?: Uint8Array },
  ): Promise<Response> {
    if (!this._rest) {
      throw new Error(
        "REST sandbox APIs are unavailable: client was constructed without REST options",
      );
    }
    const headers: Record<string, string> = {
      "x-evs-api-key": this._rest.apiKey,
    };
    if (this._rest.tenantId) {
      headers["x-evs-tenant-id"] = this._rest.tenantId;
    }
    if (this._rest.headers) {
      Object.assign(headers, this._rest.headers);
    }
    let body: BodyInit | undefined;
    if (opts?.json !== undefined) {
      headers["Content-Type"] = "application/json";
      body = JSON.stringify(opts.json);
    } else if (opts?.rawBody !== undefined) {
      headers["Content-Type"] = "application/octet-stream";
      body = opts.rawBody as unknown as BodyInit;
    }
    const res = await fetch(`${this._rest.baseUrl}${path}`, { method, headers, body });
    if (!res.ok) {
      let detail = "";
      try {
        detail = await res.text();
      } catch {
        // body unavailable; status alone will have to do
      }
      throw new Error(`sandbox API ${method} ${path} failed (${res.status}): ${detail}`);
    }
    return res;
  }

  /** @internal */
  private async _restJSON(
    method: string,
    path: string,
    opts?: { json?: unknown; rawBody?: Uint8Array },
  ): Promise<Record<string, unknown>> {
    const res = await this._restFetch(method, path, opts);
    return (await res.json()) as Record<string, unknown>;
  }

  // -----------------------------------------------------------------------
  // Private helpers
  // -----------------------------------------------------------------------

  private async _callRaw<K extends keyof AgentsClient>(
    method: K,
    request: MethodInput<K>,
    options?: MethodOptions<K>,
  ): Promise<MethodOutput<K>> {
    try {
      const fn = this.raw[method] as (
        req: MethodInput<K>,
        opt?: MethodOptions<K>,
      ) => Promise<MethodOutput<K>>;
      return await fn(request, options);
    } catch (error) {
      throw fromConnectError(error);
    }
  }

  private get tid(): string {
    return this._tenantId;
  }

  // -----------------------------------------------------------------------
  // Ergonomic Agent CRUD
  // -----------------------------------------------------------------------

  /**
   * Create a new agent definition.
   *
   * @example
   * ```ts
   * const agent = await client.agents.create({
   *   name: "Code Reviewer",
   *   model: "gpt-4o",
   *   system_prompt: "You are a code review assistant.",
   *   mode: "primary",
   * });
   * ```
   */
  async create(params: CreateAgentParams): Promise<Agent> {
    const res = await this._callRaw("createAgent", {
      tenantId: this.tid,
      name: params.name,
      model: params.model,
      description: params.description,
      systemPrompt: params.system_prompt,
      tools: params.tools ?? [],
      config: recordToJsonObject(params.config),
      maxTurns: params.max_turns,
      maxToolCallsPerTurn: params.max_tool_calls_per_turn,
      mode: toProtoAgentMode(params.mode),
      maxSteps: params.max_steps,
      taskPermissionMode: toProtoTaskPermission(params.task_permission_mode),
      hidden: params.hidden,
      color: params.color,
      workingDirectory: params.working_directory,
      mentionAlias: params.mention_alias,
      lifecycleMode: toProtoLifecycleMode(params.lifecycle_mode),
      icon: params.icon,
      autoProvision: params.auto_provision ?? false,
      executionPolicy: params.execution_policy
        ? {
            taskPermissionMode: toProtoTaskPermission(
              params.execution_policy.task_permission_mode,
            ),
            maxSteps: params.execution_policy.max_steps,
            workingDirectory: params.execution_policy.working_directory,
          }
        : undefined,
      identity: params.identity
        ? {
            soulMd: params.identity.soul_md ?? "",
            identityMd: params.identity.identity_md ?? "",
            userMd: params.identity.user_md ?? "",
            roleMd: params.identity.role_md ?? "",
          }
        : undefined,
      sandboxConfig: params.sandbox_config
        ? {
            image: params.sandbox_config.image ?? "",
            cpuLimit: params.sandbox_config.cpu_limit ?? 0,
            memoryMb: BigInt(params.sandbox_config.memory_mb ?? 0),
            diskMb: BigInt(params.sandbox_config.disk_mb ?? 0),
            timeoutSeconds: params.sandbox_config.timeout_seconds ?? 0,
            networkMode: params.sandbox_config.network_mode ?? "",
            allowedHosts: params.sandbox_config.allowed_hosts ?? [],
            envVars: params.sandbox_config.env_vars ?? {},
            sshEnabled: params.sandbox_config.ssh_enabled ?? false,
            gitRepoUrl: params.sandbox_config.git_repo_url ?? "",
            gitBranch: params.sandbox_config.git_branch ?? "",
            linkedSessionId: params.sandbox_config.linked_session_id,
          }
        : undefined,
      databaseConfig: params.database_config
        ? {
            sqlitePath: params.database_config.sqlite_path ?? "",
            lancedbPath: params.database_config.lancedb_path ?? "",
            redbPath: params.database_config.redb_path ?? "",
          }
        : undefined,
      workersConfig: params.workers_config
        ? {
            maxConcurrentWorkers:
              params.workers_config.max_concurrent_workers ?? 0,
          }
        : undefined,
    });
    return fromProtoAgent(res.agent!);
  }

  /**
   * Get an agent by ID.
   */
  async get(id: string): Promise<Agent> {
    const res = await this._callRaw("getAgent", { tenantId: this.tid, id });
    return fromProtoAgent(res.agent!);
  }

  /**
   * List agents.
   */
  async list(
    params?: ListAgentsParams,
  ): Promise<{ agents: Agent[]; total: number }> {
    const res = await this._callRaw("listAgents", {
      tenantId: this.tid,
      enabled: params?.enabled,
      limit: params?.limit,
      offset: params?.offset,
      includeHidden: params?.include_hidden ?? false,
      mode: toProtoAgentMode(params?.mode),
      lifecycleMode: toProtoLifecycleMode(params?.lifecycle_mode),
    });
    return {
      agents: res.agents.map(fromProtoAgent),
      total: res.total,
    };
  }

  /**
   * Update an agent definition.
   */
  async update(params: UpdateAgentParams): Promise<Agent> {
    const res = await this._callRaw("updateAgent", {
      tenantId: this.tid,
      id: params.id,
      name: params.name,
      description: params.description,
      model: params.model,
      systemPrompt: params.system_prompt,
      tools: params.tools ?? [],
      clearTools: params.tools !== undefined && params.tools.length === 0,
      config: recordToJsonObject(params.config),
      maxTurns: params.max_turns,
      maxToolCallsPerTurn: params.max_tool_calls_per_turn,
      enabled: params.enabled,
      mode: params.mode ? toProtoAgentMode(params.mode) : undefined,
      maxSteps: params.max_steps,
      taskPermissionMode: params.task_permission_mode
        ? toProtoTaskPermission(params.task_permission_mode)
        : undefined,
      hidden: params.hidden,
      color: params.color,
      workingDirectory: params.working_directory,
      mentionAlias: params.mention_alias,
      icon: params.icon,
      lifecycleMode: params.lifecycle_mode
        ? toProtoLifecycleMode(params.lifecycle_mode)
        : undefined,
      identity: params.identity
        ? {
            soulMd: params.identity.soul_md ?? "",
            identityMd: params.identity.identity_md ?? "",
            userMd: params.identity.user_md ?? "",
            roleMd: params.identity.role_md ?? "",
          }
        : undefined,
      sandboxConfig: params.sandbox_config
        ? {
            image: params.sandbox_config.image ?? "",
            cpuLimit: params.sandbox_config.cpu_limit ?? 0,
            memoryMb: BigInt(params.sandbox_config.memory_mb ?? 0),
            diskMb: BigInt(params.sandbox_config.disk_mb ?? 0),
            timeoutSeconds: params.sandbox_config.timeout_seconds ?? 0,
            networkMode: params.sandbox_config.network_mode ?? "",
            sshEnabled: params.sandbox_config.ssh_enabled ?? false,
            gitRepoUrl: params.sandbox_config.git_repo_url ?? "",
            gitBranch: params.sandbox_config.git_branch ?? "",
          }
        : undefined,
    });
    return fromProtoAgent(res.agent!);
  }

  /**
   * Delete an agent.
   */
  async delete(id: string): Promise<{ success: boolean; message: string }> {
    const res = await this._callRaw("deleteAgent", { tenantId: this.tid, id });
    return { success: res.success, message: res.message };
  }

  // -----------------------------------------------------------------------
  // Ergonomic Session & Turn methods
  // -----------------------------------------------------------------------

  /**
   * Create a new session for an agent.
   */
  async createSession(params: CreateSessionParams): Promise<Session> {
    const res = await this._callRaw("createSession", {
      tenantId: this.tid,
      agentId: params.agent_id,
    });
    return fromProtoSession(res.session!);
  }

  /**
   * Get a session by ID.
   */
  async getSession(id: string): Promise<Session> {
    const res = await this._callRaw("getSession", { tenantId: this.tid, id });
    return fromProtoSession(res.session!);
  }

  /**
   * Run a turn (non-streaming). Returns the completed turn and session status.
   */
  async runTurn(params: RunTurnParams): Promise<RunTurnResult> {
    const res = await this._callRaw("runTurn", {
      tenantId: this.tid,
      sessionId: params.session_id,
      userInput: params.input,
    });
    return {
      turn: fromProtoTurn(res.turn!),
      session_status: fromProtoSessionStatus(res.sessionStatus),
    };
  }

  /**
   * Run a turn with streaming. Returns an `AgentStream` with typed events.
   *
   * @example
   * ```ts
   * const stream = client.agents.runTurnStream({
   *   session_id: "s_...",
   *   input: "Refactor the auth module",
   * });
   * for await (const event of stream) {
   *   if (event.type === "text_delta") process.stdout.write(event.text);
   * }
   * ```
   */
  runTurnStream(params: RunTurnStreamParams): AgentStream {
    const raw = this.raw.runTurnStream({
      tenantId: this.tid,
      sessionId: params.session_id,
      userInput: params.input,
      enableStreaming: params.enable_streaming ?? true,
      enableWebSearch: params.enable_web_search ?? false,
    });
    return new AgentStream(raw);
  }

  // -----------------------------------------------------------------------
  // Ergonomic Sandbox methods
  // -----------------------------------------------------------------------

  /**
   * Create a sandbox environment.
   *
   * @example
   * ```ts
   * const sandbox = await client.agents.createSandbox({
   *   session_id: session.id,
   *   image: "ubuntu:22.04",
   *   memory_mb: 512,
   *   git: { repo_url: "https://github.com/user/repo", branch: "main" },
   * });
   * ```
   */
  async createSandbox(
    params: CreateSandboxParams,
  ): Promise<CreateSandboxResult> {
    const res = await this._callRaw("createSandbox", {
      tenantId: this.tid,
      sessionId: params.session_id,
      image: params.image ?? "",
      cpuLimit: params.cpu_limit ?? 0,
      memoryMb: BigInt(params.memory_mb ?? 0),
      diskMb: BigInt(params.disk_mb ?? 0),
      timeoutSeconds: params.timeout_seconds ?? 0,
      networkMode: params.network_mode ?? "",
      idleRetentionSeconds: params.idle_retention_seconds ?? 0,
      templateId: params.template_id ?? "",
      name: params.name ?? "",
      gitRepoUrl: params.git?.repo_url ?? "",
      gitBranch: params.git?.branch ?? "",
      gitInstallationId: BigInt(params.git?.installation_id ?? 0),
      sshEnabled: params.ssh_enabled ?? false,
    });
    return {
      id: res.id,
      session_id: res.sessionId,
      tenant_id: res.tenantId,
      container_id: res.containerId,
      status: res.status,
      backend: res.backend,
      image: res.image,
      created_at: res.createdAt,
      expires_at: res.expiresAt,
      name: res.name,
    };
  }

  /**
   * List all sandbox instances.
   */
  async listSandboxes(params?: {
    status?: SandboxStatusLiteral;
    limit?: number;
    offset?: number;
  }): Promise<{ instances: SandboxInstanceType[]; total: number }> {
    const statusMap: Record<string, ProtoSandboxStatus> = {
      pending: ProtoSandboxStatus.PENDING,
      running: ProtoSandboxStatus.RUNNING,
      stopped: ProtoSandboxStatus.STOPPED,
      failed: ProtoSandboxStatus.FAILED,
    };
    const res = await this._callRaw("listSandboxInstances", {
      tenantId: this.tid,
      status: params?.status ? statusMap[params.status] : undefined,
      limit: params?.limit,
      offset: params?.offset,
    });
    return {
      instances: res.instances.map(fromProtoSandboxInstance),
      total: res.total,
    };
  }

  /**
   * Get a sandbox instance by ID.
   */
  async getSandbox(sandboxId: string): Promise<SandboxInstanceType> {
    const res = await this._callRaw("getSandboxInstance", {
      tenantId: this.tid,
      sandboxId,
    });
    return fromProtoSandboxInstance(res.instance!);
  }

  // -----------------------------------------------------------------------
  // Convenience: one-shot run / stream
  // -----------------------------------------------------------------------

  /**
   * One-shot: create a session, run a turn, return the result.
   *
   * @example
   * ```ts
   * const result = await client.agents.run("agent_abc", "Fix the auth bug");
   * console.log(result.turn.assistant_output);
   * ```
   */
  async run(agentId: string, input: string): Promise<RunTurnResult> {
    const session = await this.createSession({ agent_id: agentId });
    return this.runTurn({ session_id: session.id, input });
  }

  /**
   * One-shot streaming: create a session, stream a turn.
   *
   * @example
   * ```ts
   * const stream = client.agents.stream("agent_abc", "Fix the auth bug");
   * for await (const text of stream.text()) process.stdout.write(text);
   * ```
   */
  stream(agentId: string, input: string): AgentStream {
    // An async generator cannot be an arrow function, so capture the members
    // the generator needs rather than aliasing `this` into its closure.
    const tenantId = this.tid;
    const raw = this.raw;
    const callRaw = this._callRaw.bind(this);
    async function* gen(): AsyncGenerator<ProtoAgentEvent> {
      const session = await callRaw("createSession", {
        tenantId,
        agentId,
      });
      const rawStream = raw.runTurnStream({
        tenantId,
        sessionId: session.session!.id,
        userInput: input,
        enableStreaming: true,
        enableWebSearch: false,
      });
      yield* rawStream;
    }
    return new AgentStream(gen());
  }
}
