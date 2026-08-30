/**
 * Responses resource
 *
 * Provides the agentic Responses API with built-in tool calling orchestration.
 */

import type { Client } from "@connectrpc/connect";
import type { GatewayService } from "@everstack/proto/everstack/gateway/v1/gateway_service_pb.js";
import { create } from "@bufbuild/protobuf";
import {
  CreateResponseRequestSchema,
  ResponseInputSchema,
  type CreateResponseResponse as ProtoCreateResponseResponse,
  type ResponseOutputItem as ProtoResponseOutputItem,
} from "@everstack/proto/everstack/gateway/v1/responses_pb.js";
import {
  ContentPartSchema,
  ToolChoiceSchema,
  Role as ProtoRole,
} from "@everstack/proto/everstack/gateway/v1/chat_pb.js";

import { fromConnectError } from "../errors.js";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ResponseInput {
  type?: "message" | "item_reference";
  role?: "user" | "assistant" | "system" | "developer";
  content?: Array<{ type: string; text?: string }>;
  item_id?: string;
}

export interface BuiltInTool {
  type: "web_search" | "file_search" | "code_interpreter" | "computer_use" | "mcp";
  [key: string]: unknown;
}

export interface ReasoningConfig {
  effort?: "low" | "medium" | "high";
  generate_summary?: boolean;
}

export interface ResponseTextConfig {
  format?: {
    type: "text" | "json_schema" | "json_object";
    json_schema?: Record<string, unknown>;
  };
}

export interface TruncationStrategy {
  type: "auto" | "disabled";
  last_messages?: number;
}

export interface CreateResponseParams {
  /** Model to use */
  model: string;
  /** System instructions */
  instructions?: string;
  /** Input messages / item references */
  input: ResponseInput[];
  /** Function tool definitions (JSON Schema) */
  tools?: Array<Record<string, unknown>>;
  /** Built-in tools (web_search, file_search, code_interpreter, etc.) */
  builtin_tools?: BuiltInTool[];
  /** Tool choice strategy ("auto", "none", "required") */
  tool_choice?: string;
  /** Whether to run tool calls in parallel */
  parallel_tool_calls?: boolean;
  /** Text output configuration */
  text?: ResponseTextConfig;
  /** Reasoning / chain-of-thought configuration */
  reasoning?: ReasoningConfig;
  /** Maximum output tokens */
  max_output_tokens?: number;
  /** Sampling temperature */
  temperature?: number;
  /** Nucleus sampling */
  top_p?: number;
  /** Truncation strategy for long contexts */
  truncation?: TruncationStrategy;
  /** Whether to store the response for later retrieval */
  store?: boolean;
  /** Continue from a previous response */
  previous_response_id?: string;
  /** Whether to stream the response */
  stream?: boolean;
  /** Arbitrary metadata */
  metadata?: Record<string, string>;
}

export interface ResponseOutputItem {
  id: string;
  type: string;
  status: string;
  role?: string;
  content?: Array<{ type: string; text?: string }>;
  /** For function_call items */
  call_id?: string;
  name?: string;
  arguments?: string;
  output?: string;
}

export interface ResponseUsage {
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
}

export interface ResponseObject {
  id: string;
  object: string;
  created_at: number;
  status: string;
  model: string;
  output: ResponseOutputItem[];
  usage?: ResponseUsage;
  metadata: Record<string, string>;
  temperature: number;
  top_p: number;
  max_output_tokens: number;
  previous_response_id?: string;
}

export interface ResponseStreamEvent {
  event: string;
  data: string;
}

export interface DeleteResponseResult {
  id: string;
  object: string;
  deleted: boolean;
}

export interface ListResponsesParams {
  status?: string;
  limit?: number;
  after?: string;
  before?: string;
  order?: string;
}

export interface ListResponsesResult {
  data: ResponseObject[];
  first_id: string;
  last_id: string;
  has_more: boolean;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function toProtoRole(role: string): ProtoRole {
  switch (role) {
    case "system":
      return ProtoRole.SYSTEM;
    case "assistant":
      return ProtoRole.ASSISTANT;
    case "tool":
      return ProtoRole.TOOL;
    case "function":
      return ProtoRole.FUNCTION;
    case "user":
    default:
      return ProtoRole.USER;
  }
}

function fromProtoRole(role: ProtoRole): string {
  switch (role) {
    case ProtoRole.SYSTEM:
      return "system";
    case ProtoRole.ASSISTANT:
      return "assistant";
    case ProtoRole.TOOL:
      return "tool";
    case ProtoRole.FUNCTION:
      return "function";
    case ProtoRole.USER:
    default:
      return "user";
  }
}

function transformOutputItem(item: ProtoResponseOutputItem): ResponseOutputItem {
  return {
    id: item.id,
    type: item.type,
    status: item.status,
    role: item.role ? fromProtoRole(item.role as unknown as ProtoRole) : undefined,
    content: item.content.map((c) => ({
      type: c.type,
      text:
        c.data?.case === "text" ? c.data.value : undefined,
    })),
    call_id: item.callId || undefined,
    name: item.name || undefined,
    arguments: item.arguments || undefined,
    output: item.output || undefined,
  };
}

function transformResponse(proto: ProtoCreateResponseResponse): ResponseObject {
  return {
    id: proto.id,
    object: proto.object,
    created_at: Number(proto.createdAt),
    status: proto.status,
    model: proto.model,
    output: proto.output.map(transformOutputItem),
    usage: proto.usage
      ? {
          input_tokens: proto.usage.inputTokens,
          output_tokens: proto.usage.outputTokens,
          total_tokens: proto.usage.totalTokens,
        }
      : undefined,
    metadata: Object.fromEntries(Object.entries(proto.metadata)),
    temperature: proto.temperature,
    top_p: proto.topP,
    max_output_tokens: proto.maxOutputTokens,
    previous_response_id: proto.previousResponseId || undefined,
  };
}

// ---------------------------------------------------------------------------
// Resource
// ---------------------------------------------------------------------------

/**
 * Responses resource for agentic orchestration
 */
export class Responses {
  /** @internal */
  constructor(private readonly _client: Client<typeof GatewayService>) {}

  /**
   * Create a response (non-streaming)
   *
   * @example
   * ```typescript
   * const response = await client.responses.create({
   *   model: '@openai/gpt-4o',
   *   input: [{ role: 'user', content: [{ type: 'text', text: 'Hello!' }] }],
   * });
   * console.log(response.output);
   * ```
   */
  async create(
    params: CreateResponseParams & { stream?: false },
  ): Promise<ResponseObject>;

  /**
   * Create a streaming response
   *
   * @example
   * ```typescript
   * const stream = await client.responses.create({
   *   model: '@openai/gpt-4o',
   *   input: [{ role: 'user', content: [{ type: 'text', text: 'Hello!' }] }],
   *   stream: true,
   * });
   * for await (const event of stream) {
   *   console.log(event);
   * }
   * ```
   */
  async create(
    params: CreateResponseParams & { stream: true },
  ): Promise<AsyncIterable<ResponseStreamEvent>>;

  async create(
    params: CreateResponseParams,
  ): Promise<ResponseObject | AsyncIterable<ResponseStreamEvent>> {
    try {
      const inputMessages = (params.input ?? []).map((i) =>
        create(ResponseInputSchema, {
          type: i.type ?? "message",
          role: toProtoRole(i.role ?? "user"),
          content: (i.content ?? []).map((c) =>
            create(ContentPartSchema, {
              type: c.type,
              data: c.text != null ? { case: "text" as const, value: c.text } : { case: undefined, value: undefined },
            }),
          ),
          itemId: i.item_id ?? "",
        }),
      );

      const request = create(CreateResponseRequestSchema, {
        model: params.model,
        instructions: params.instructions ?? "",
        input: inputMessages,
        stream: params.stream ?? false,
        temperature: params.temperature ?? 0,
        topP: params.top_p ?? 0,
        maxOutputTokens: params.max_output_tokens ?? 0,
        store: params.store ?? false,
        previousResponseId: params.previous_response_id ?? "",
        parallelToolCalls: params.parallel_tool_calls ?? false,
        metadata: params.metadata ?? {},
      });

      // Set tool choice if provided
      if (params.tool_choice) {
        request.toolChoice = create(ToolChoiceSchema, {
          choice: { case: "mode" as const, value: params.tool_choice },
        });
      }

      if (params.stream) {
        return this._createStream(request);
      }

      // Non-streaming: collect all chunks and return the final one
      const chunks: ProtoCreateResponseResponse[] = [];
      for await (const chunk of this._client.createResponse(request)) {
        chunks.push(chunk);
      }

      const lastChunk = chunks[chunks.length - 1];
      if (!lastChunk) {
        throw new Error("No response received from server");
      }

      return transformResponse(lastChunk);
    } catch (error) {
      throw fromConnectError(error);
    }
  }

  private async *_createStream(
    request: ReturnType<typeof create<typeof CreateResponseRequestSchema>>,
  ): AsyncIterable<ResponseStreamEvent> {
    try {
      for await (const chunk of this._client.createResponse(request)) {
        yield {
          event: chunk.status ? `response.${chunk.status}` : "response.delta",
          data: JSON.stringify(transformResponse(chunk)),
        };
      }
    } catch (error) {
      throw fromConnectError(error);
    }
  }

  /**
   * Get a response by ID
   */
  async get(responseId: string): Promise<ResponseObject> {
    try {
      const response = await this._client.getResponse({
        responseId,
        includeInput: false,
      });
      if (!response.response) {
        throw new Error("Response not found");
      }
      return transformResponse(response.response);
    } catch (error) {
      throw fromConnectError(error);
    }
  }

  /**
   * Cancel an in-progress response
   */
  async cancel(responseId: string): Promise<ResponseObject> {
    try {
      const response = await this._client.cancelResponse({ responseId });
      if (!response.response) {
        throw new Error("Response not found");
      }
      return transformResponse(response.response);
    } catch (error) {
      throw fromConnectError(error);
    }
  }

  /**
   * Delete a response
   */
  async del(responseId: string): Promise<DeleteResponseResult> {
    try {
      const response = await this._client.deleteResponse({ responseId });
      return {
        id: response.id,
        object: response.object,
        deleted: response.deleted,
      };
    } catch (error) {
      throw fromConnectError(error);
    }
  }

  /**
   * List responses
   */
  async list(params?: ListResponsesParams): Promise<ListResponsesResult> {
    try {
      const response = await this._client.listResponses({
        status: params?.status ?? "",
        limit: params?.limit ?? 0,
        after: params?.after ?? "",
        before: params?.before ?? "",
        order: params?.order ?? "",
      });
      return {
        data: response.data.map(transformResponse),
        first_id: response.firstId,
        last_id: response.lastId,
        has_more: response.hasMore,
      };
    } catch (error) {
      throw fromConnectError(error);
    }
  }
}
