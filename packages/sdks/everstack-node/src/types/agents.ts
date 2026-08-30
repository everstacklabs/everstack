/**
 * Ergonomic TypeScript types for the Agents & Sandboxes API.
 *
 * These mirror the proto definitions with snake_case fields and string
 * literal enums so callers don't need to import proto enums directly.
 */

// ---------------------------------------------------------------------------
// Enums as string literals
// ---------------------------------------------------------------------------

export type AgentMode = "primary" | "subagent";
export type LifecycleMode = "ephemeral" | "persistent";

export type LifecycleStatus =
  | "created"
  | "provisioning"
  | "running"
  | "sleeping"
  | "waking"
  | "failed"
  | "terminated"
  | "idle";

export type SessionStatus =
  | "created"
  | "running"
  | "waiting_for_input"
  | "waiting_for_approval"
  | "completed"
  | "failed"
  | "cancelled"
  | "hibernated";

export type TurnStatus = "pending" | "running" | "completed" | "failed";

export type TaskPermission = "ask" | "always" | "deny";

export type SandboxStatusLiteral = "pending" | "running" | "stopped" | "failed";

export type SandboxLifecycleState =
  | "creating"
  | "running"
  | "stopping"
  | "stopped"
  | "archiving"
  | "archived"
  | "restoring"
  | "deleting"
  | "deleted"
  | "failed";

// ---------------------------------------------------------------------------
// Agent Identity & Config sub-types
// ---------------------------------------------------------------------------

export interface AgentIdentityParams {
  soul_md?: string;
  identity_md?: string;
  user_md?: string;
  role_md?: string;
}

export interface SandboxConfig {
  image?: string;
  cpu_limit?: number;
  memory_mb?: number;
  disk_mb?: number;
  timeout_seconds?: number;
  network_mode?: string;
  allowed_hosts?: string[];
  env_vars?: Record<string, string>;
  ssh_enabled?: boolean;
  git_repo_url?: string;
  git_branch?: string;
  linked_session_id?: string;
}

export interface DatabaseConfig {
  sqlite_path?: string;
  lancedb_path?: string;
  redb_path?: string;
}

export interface WorkersConfig {
  max_concurrent_workers?: number;
  pool_config?: Record<string, unknown>;
}

export interface MemoryConfig {
  enabled?: boolean;
  backend?: string;
  [key: string]: unknown;
}

export interface ExecutionPolicy {
  task_permission_mode?: TaskPermission;
  max_steps?: number;
  working_directory?: string;
}

// ---------------------------------------------------------------------------
// Agent CRUD
// ---------------------------------------------------------------------------

export interface CreateAgentParams {
  name: string;
  model: string;
  description?: string;
  system_prompt?: string;
  tools?: string[];
  config?: Record<string, unknown>;
  max_turns?: number;
  max_tool_calls_per_turn?: number;
  mode?: AgentMode;
  max_steps?: number;
  task_permission_mode?: TaskPermission;
  hidden?: boolean;
  color?: string;
  working_directory?: string;
  mention_alias?: string;
  execution_policy?: ExecutionPolicy;
  memory_config?: MemoryConfig;
  lifecycle_mode?: LifecycleMode;
  identity?: AgentIdentityParams;
  sandbox_config?: SandboxConfig;
  database_config?: DatabaseConfig;
  workers_config?: WorkersConfig;
  icon?: string;
  auto_provision?: boolean;
}

export interface UpdateAgentParams {
  id: string;
  name?: string;
  description?: string;
  model?: string;
  system_prompt?: string;
  tools?: string[];
  config?: Record<string, unknown>;
  max_turns?: number;
  max_tool_calls_per_turn?: number;
  enabled?: boolean;
  mode?: AgentMode;
  max_steps?: number;
  task_permission_mode?: TaskPermission;
  hidden?: boolean;
  color?: string;
  working_directory?: string;
  mention_alias?: string;
  execution_policy?: ExecutionPolicy;
  memory_config?: MemoryConfig;
  identity?: AgentIdentityParams;
  sandbox_config?: SandboxConfig;
  database_config?: DatabaseConfig;
  workers_config?: WorkersConfig;
  icon?: string;
  lifecycle_mode?: LifecycleMode;
}

export interface ListAgentsParams {
  enabled?: boolean;
  limit?: number;
  offset?: number;
  include_hidden?: boolean;
  mode?: AgentMode;
  lifecycle_mode?: LifecycleMode;
}

export interface Agent {
  id: string;
  tenant_id: string;
  name: string;
  description: string;
  model: string;
  system_prompt: string;
  tools: string[];
  config: Record<string, unknown> | null;
  max_turns: number;
  max_tool_calls_per_turn: number;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  mode: AgentMode;
  max_steps: number | null;
  task_permission_mode: TaskPermission;
  hidden: boolean;
  color: string | null;
  working_directory: string | null;
  mention_alias: string | null;
  lifecycle_mode: LifecycleMode;
  lifecycle_status: LifecycleStatus;
  icon: string | null;
  sandbox_id: string;
  primary_session_id: string;
  identity: AgentIdentityParams;
  sandbox_config: SandboxConfig;
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

export interface CreateSessionParams {
  agent_id: string;
  metadata?: Record<string, unknown>;
}

export interface Session {
  id: string;
  tenant_id: string;
  agent_id: string;
  status: SessionStatus;
  turn_count: number;
  total_tokens: number;
  metadata: Record<string, unknown> | null;
  created_at: string;
  updated_at: string;
  completed_at: string | null;
  summary: string;
  turns: Turn[];
}

// ---------------------------------------------------------------------------
// Turns
// ---------------------------------------------------------------------------

export interface RunTurnParams {
  session_id: string;
  input: string;
}

export interface RunTurnStreamParams {
  session_id: string;
  input: string;
  enable_streaming?: boolean;
  enable_web_search?: boolean;
}

export interface Turn {
  id: string;
  session_id: string;
  turn_number: number;
  status: TurnStatus;
  user_input: string;
  assistant_output: string;
  tool_calls: string;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  latency_ms: number;
  error: string;
  created_at: string;
  completed_at: string | null;
  cache_read_input_tokens: number;
  cache_write_input_tokens: number;
}

export interface RunTurnResult {
  turn: Turn;
  session_status: SessionStatus;
}

// ---------------------------------------------------------------------------
// Agent Stream Events (discriminated union)
// ---------------------------------------------------------------------------

interface BaseEvent {
  session_id: string;
  turn_number: number;
}

export interface TextDeltaEvent extends BaseEvent {
  type: "text_delta";
  text: string;
}

export interface ToolCallEvent extends BaseEvent {
  type: "tool_call";
  tool_call_id: string;
  tool_name: string;
  tool_args: string;
}

export interface ToolResultEvent extends BaseEvent {
  type: "tool_result";
  tool_call_id: string;
  tool_name: string;
  tool_result: string;
  tool_success: boolean;
  tool_duration_ms: number;
}

export interface ReviewPendingEvent extends BaseEvent {
  type: "review_pending";
  review_id: string;
  pending_tool_calls: Array<{
    tool_call_id: string;
    tool_name: string;
    tool_args: string;
  }>;
}

export interface ReviewResolvedEvent extends BaseEvent {
  type: "review_resolved";
  review_id: string;
  approval_action: string;
}

export interface TurnEndEvent extends BaseEvent {
  type: "turn.end";
  /** Present on servers that embed the persisted turn in the terminal event. */
  turn?: Turn;
  finish_reason: string;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

export interface TurnStartEvent extends BaseEvent {
  type: "turn.start";
}

export interface SandboxExecEvent extends BaseEvent {
  type: "sandbox.exec";
  sandbox_id: string;
  sandbox_exit_code: number;
  sandbox_duration_ms: number;
}

export interface FallbackEvent extends BaseEvent {
  type: "fallback";
  fallback_from_model: string;
  fallback_to_model: string;
  fallback_attempt: number;
}

export interface UserInputRequestEvent extends BaseEvent {
  type: "user_input_request";
  user_input_id: string;
}

export interface ErrorEvent extends BaseEvent {
  type: "error";
  error: string;
  /** Canonical runtime event type, for example `session.error`. */
  source_type?: string;
}

export interface GenericEvent extends BaseEvent {
  type: string;
  [key: string]: unknown;
}

export type AgentStreamEvent =
  | TextDeltaEvent
  | ToolCallEvent
  | ToolResultEvent
  | ReviewPendingEvent
  | ReviewResolvedEvent
  | TurnEndEvent
  | TurnStartEvent
  | SandboxExecEvent
  | FallbackEvent
  | UserInputRequestEvent
  | ErrorEvent
  | GenericEvent;

// ---------------------------------------------------------------------------
// Sandboxes
// ---------------------------------------------------------------------------

export interface CreateSandboxParams {
  session_id: string;
  image?: string;
  template_id?: string;
  name?: string;
  cpu_limit?: number;
  memory_mb?: number;
  disk_mb?: number;
  timeout_seconds?: number;
  network_mode?: string;
  idle_retention_seconds?: number;
  ssh_enabled?: boolean;
  git?: {
    repo_url: string;
    branch?: string;
    installation_id?: number;
  };
}

export interface CreateSandboxResult {
  id: string;
  session_id: string;
  tenant_id: string;
  container_id: string;
  status: string;
  backend: string;
  image: string;
  created_at: string;
  expires_at: string;
  name: string;
}

export interface SandboxInstance {
  id: string;
  session_id: string;
  tenant_id: string;
  backend: string;
  container_id: string;
  image: string;
  status: SandboxStatusLiteral;
  created_at: string;
  expires_at: string;
  name: string;
  last_used_at: string | null;
  idle_retention_secs: number;
  keep_warm: boolean;
  git_repo_url: string;
  git_branch: string;
  git_commit_sha: string;
  lifecycle_state: SandboxLifecycleState;
  ssh_enabled: boolean;
  persistent: boolean;
  agent_id: string;
  short_code: string;
  agent_healthy: boolean;
}

export interface SandboxExecution {
  id: string;
  sandbox_id: string;
  session_id: string;
  tool_name: string;
  tool_call_id: string;
  language: string;
  command: string;
  exit_code: number;
  stdout: string;
  stderr: string;
  duration_ms: number;
  timed_out: boolean;
  created_at: string;
}

export interface SandboxStats {
  cpu_percent: number;
  memory_usage: number;
  memory_limit: number;
  memory_percent: number;
  network_rx_bytes: number;
  network_tx_bytes: number;
  block_read: number;
  block_write: number;
  pids: number;
}

export interface ExposePortParams {
  sandbox_id: string;
  port: number;
  protocol?: string;
  label?: string;
}

// ---------------------------------------------------------------------------
// Reviews (HITL)
// ---------------------------------------------------------------------------

export interface SubmitReviewParams {
  review_id: string;
  action: "approve" | "deny" | "modify";
  reason?: string;
  resolved_by?: string;
  decisions?: Array<{
    tool_call_id: string;
    action: "approve" | "deny" | "modify";
    reason?: string;
  }>;
}

// ---------------------------------------------------------------------------
// Deployments
// ---------------------------------------------------------------------------

export interface DeployAgentParams {
  agent_id: string;
  name?: string;
  description?: string;
  version?: string;
  config?: Record<string, unknown>;
}

// ---------------------------------------------------------------------------
// Triggers
// ---------------------------------------------------------------------------

export interface CreateTriggerParams {
  agent_id: string;
  name: string;
  type: string;
  config: Record<string, unknown>;
  enabled?: boolean;
}
