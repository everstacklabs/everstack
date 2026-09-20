import { APIError } from "../errors.js";

export interface OpenAIAgentConnection {
  id: string;
  name: string;
  provider: "openai_agents";
  project_id?: string;
  organization_id?: string;
  principal_id?: string;
  allowed_models?: string[];
  status: string;
  last_error?: string;
}

export interface OpenAIAgentConnectionParams {
  name: string;
  api_key: string;
  executor_key?: string;
  project_id?: string;
  organization_id?: string;
  principal_id?: string;
  allowed_models?: string[];
}

export interface OpenAIAgentSession {
  id: string;
  provider: "openai_agents";
  connection_id: string;
  upstream_session_id?: string;
  upstream_environment_id?: string;
  sandbox_id?: string;
  model: string;
  environment: { type: "none" | "self_hosted"; workspace_directory?: string };
  status: string;
  readiness: string;
  cleanup_status: string;
  last_error?: string;
  created_at: string;
  updated_at: string;
}

export interface OpenAIAgentSessionParams {
  connection_id: string;
  /** Persist and reuse this key after an ambiguous response. */
  idempotency_key: string;
  model: string;
  instructions?: string;
  environment: OpenAIAgentSession["environment"];
  input?: string;
  tools?: OpenAIAgentTool[];
}

export interface OpenAIAgentTool {
  type: "function" | "mcp";
  resource_id: string;
  name: string;
}

export interface OpenAIAgentArtifact {
  object_id: string;
  kind: "input" | "output";
  filename: string;
  content_type: string;
  size_bytes: number;
  sha256: string;
  created_at: string;
}

export interface OpenAIAgentApproval {
  id: string;
  session_id: string;
  turn_id: string;
  call_id: string;
  tool_name: string;
  resource_kind: string;
  resource_id: string;
  arguments: Record<string, unknown>;
  requested_by: string;
  status: string;
  requested_at: string;
  expires_at: string;
}

export interface OpenAIAgentUsage {
  status: "unavailable" | "reported";
  source: "openai";
  input_tokens?: number | null;
  output_tokens?: number | null;
  total_tokens?: number | null;
  updated_at?: string;
}

export interface OpenAIAgentCapabilities {
  enabled: boolean;
  environment_modes: string[];
  executor_version?: string;
  tool_types: string[];
  file_operations: string[];
  can_manage_connections: boolean;
  can_run_sessions: boolean;
  can_resolve_approvals: boolean;
}

export interface OpenAIAgentEvent {
  cursor: number;
  event_id: string;
  type: string;
  version: string;
  payload: Record<string, unknown>;
  tool_failed?: boolean;
  created_at: string;
}

export interface OpenAIAgentEventPage {
  data: OpenAIAgentEvent[];
  next_cursor: number;
  has_more: boolean;
}

export interface OpenAIAgentsOptions {
  baseUrl: string;
  /** Instance-scoped API keys permit reads. Use an authenticated user token for writes. */
  apiKey?: string;
  /** Device-login bearer token, or a provider that refreshes it before each request. */
  accessToken?: string | (() => Promise<string>);
  orgId?: string;
  headers?: Record<string, string>;
  timeout?: number;
}

type Request = <T>(method: string, path: string, body?: unknown) => Promise<T>;
const idPath = (id: string) => {
  if (!id || id === "." || id === "..")
    throw new Error("A resource ID is required");
  return encodeURIComponent(id);
};

export class OpenAIAgentConnections {
  constructor(private readonly request: Request) {}
  list() {
    return this.request<{ data: OpenAIAgentConnection[] }>(
      "GET",
      "/connections",
    );
  }
  get(id: string) {
    return this.request<OpenAIAgentConnection>(
      "GET",
      `/connections/${idPath(id)}`,
    );
  }
  create(params: OpenAIAgentConnectionParams) {
    return this.request<OpenAIAgentConnection>("POST", "/connections", params);
  }
  update(id: string, params: Partial<OpenAIAgentConnectionParams>) {
    return this.request<OpenAIAgentConnection>(
      "PATCH",
      `/connections/${idPath(id)}`,
      params,
    );
  }
  delete(id: string) {
    return this.request<{ deleted: boolean }>(
      "DELETE",
      `/connections/${idPath(id)}`,
    );
  }
}

export class OpenAIAgentSessions {
  constructor(private readonly request: Request) {}
  list() {
    return this.request<{ data: OpenAIAgentSession[] }>("GET", "/sessions");
  }
  get(id: string) {
    return this.request<OpenAIAgentSession>("GET", `/sessions/${idPath(id)}`);
  }
  create(params: OpenAIAgentSessionParams) {
    return this.request<OpenAIAgentSession>("POST", "/sessions", params);
  }
  input(id: string, params: { idempotency_key: string; input: string }) {
    return this.request<{ accepted: boolean }>(
      "POST",
      `/sessions/${idPath(id)}/input`,
      params,
    );
  }
  cancel(id: string) {
    return this.request<{ accepted: boolean }>(
      "POST",
      `/sessions/${idPath(id)}/cancel`,
    );
  }
  delete(id: string) {
    return this.request<{ deleted: boolean }>(
      "DELETE",
      `/sessions/${idPath(id)}`,
    );
  }
  /** Everstack's durable cursor, not an upstream SSE replay cursor. */
  events(id: string, options: { after?: number; limit?: number } = {}) {
    const query = new URLSearchParams();
    if (options.after !== undefined) query.set("after", String(options.after));
    if (options.limit !== undefined) query.set("limit", String(options.limit));
    return this.request<OpenAIAgentEventPage>(
      "GET",
      `/sessions/${idPath(id)}/events?${query}`,
    );
  }
  artifacts(id: string) {
    return this.request<{ data: OpenAIAgentArtifact[] }>(
      "GET",
      `/sessions/${idPath(id)}/artifacts`,
    );
  }
  /** Omit destination to retain only; supplying it attaches to a connected sandbox. */
  uploadInput(
    id: string,
    params: {
      filename: string;
      content_type?: string;
      content_base64: string;
      destination?: string;
    },
  ) {
    return this.request<OpenAIAgentArtifact>(
      "POST",
      `/sessions/${idPath(id)}/inputs`,
      params,
    );
  }
  exportArtifact(id: string, path: string) {
    return this.request<OpenAIAgentArtifact>(
      "POST",
      `/sessions/${idPath(id)}/artifacts/export`,
      { path },
    );
  }
  artifactDownload(id: string, objectId: string) {
    return this.request<
      OpenAIAgentArtifact & { url: string; expires_at: string }
    >("GET", `/sessions/${idPath(id)}/artifacts/${idPath(objectId)}/download`);
  }
  deleteArtifact(id: string, objectId: string) {
    return this.request<{ deleted: boolean }>(
      "DELETE",
      `/sessions/${idPath(id)}/artifacts/${idPath(objectId)}`,
    );
  }
  usage(id: string) {
    return this.request<OpenAIAgentUsage>(
      "GET",
      `/sessions/${idPath(id)}/usage`,
    );
  }
}

export class OpenAIAgentApprovals {
  constructor(private readonly request: Request) {}
  list() {
    return this.request<{ data: OpenAIAgentApproval[] }>("GET", "/approvals");
  }
  resolve(id: string, decision: "approved" | "denied") {
    return this.request<{ resolved: boolean }>(
      "POST",
      `/approvals/${idPath(id)}/resolve`,
      { decision },
    );
  }
}

/** OpenAI-managed sessions on an Everstack instance. Mutations are never retried automatically. */
export class OpenAIAgents {
  readonly connections: OpenAIAgentConnections;
  readonly sessions: OpenAIAgentSessions;
  readonly approvals: OpenAIAgentApprovals;
  private readonly request: Request;

  capabilities() {
    return this.request<OpenAIAgentCapabilities>("GET", "/capabilities");
  }

  constructor(options: OpenAIAgentsOptions) {
    const request: Request = async <T>(
      method: string,
      path: string,
      body?: unknown,
    ): Promise<T> => {
      const controller = new AbortController();
      const timeout = setTimeout(
        () => controller.abort(),
        options.timeout ?? 60_000,
      );
      try {
        const accessToken =
          typeof options.accessToken === "function"
            ? await options.accessToken()
            : options.accessToken;
        const response = await fetch(
          `${options.baseUrl.replace(/\/$/, "")}/v1/openai-agents${path}`,
          {
            method,
            redirect: "error",
            signal: controller.signal,
            headers: {
              ...options.headers,
              "Content-Type": "application/json",
              ...(accessToken
                ? { Authorization: `Bearer ${accessToken}` }
                : options.apiKey
                  ? { "x-evs-api-key": options.apiKey }
                  : {}),
              ...(options.orgId ? { "x-evs-org-id": options.orgId } : {}),
            },
            body: body === undefined ? undefined : JSON.stringify(body),
          },
        );
        if (!response.ok) {
          const payload: unknown = await response
            .json()
            .catch(() => ({ message: `Request failed (${response.status})` }));
          throw APIError.fromResponse(response.status, payload);
        }
        if (response.status === 204) return undefined as T;
        return (await response.json()) as T;
      } finally {
        clearTimeout(timeout);
      }
    };
    this.connections = new OpenAIAgentConnections(request);
    this.sessions = new OpenAIAgentSessions(request);
    this.approvals = new OpenAIAgentApprovals(request);
    this.request = request;
  }
}
