/**
 * Chat resource
 *
 * Provides chat completion functionality with support for streaming.
 */

import type { Client } from "@connectrpc/connect";
import type { GatewayService } from "@everstack/proto/everstack/gateway/v1/gateway_service_pb.js";
import { create, type JsonObject } from "@bufbuild/protobuf";
import {
    ChatCompletionRequestSchema,
    MessageSchema,
    SamplingParamsSchema,
    ToolSchema,
    ToolFunctionDefSchema,
    ToolChoiceSchema,
    Role as ProtoRole,
    type ChatCompletionResponse as ProtoChatResponse,
    type ToolCall as ProtoToolCall,
} from "@everstack/proto/everstack/gateway/v1/chat_pb.js";

import type {
    ChatCompletionChunk,
    ChatCompletionDelta,
    ChatCompletionParams,
    ChatCompletionResponse,
    Message,
} from "../types/chat.js";
import type { ToolCall } from "../types/common.js";
import type { AllModels } from "../types/models.js";
import { fromConnectError } from "../errors.js";

/**
 * Parse a model string that may be in @provider/model format
 * Returns just the model name for the API call
 */
function parseModelString(model: string): { provider?: string; modelName: string } {
    // Match @provider/model-name format
    const match = model.match(/^@([^/]+)\/(.+)$/);
    if (match && match[1] && match[2]) {
        return { provider: match[1], modelName: match[2] };
    }
    // Return as-is if not in @provider/model format
    return { modelName: model };
}

/**
 * Map string role to proto enum
 */
function toProtoRole(role: string): ProtoRole {
    switch (role) {
        case "system":
            return ProtoRole.SYSTEM;
        case "user":
            return ProtoRole.USER;
        case "assistant":
            return ProtoRole.ASSISTANT;
        case "function":
            return ProtoRole.FUNCTION;
        case "tool":
            return ProtoRole.TOOL;
        default:
            return ProtoRole.UNSPECIFIED;
    }
}

/**
 * Map proto enum to string role
 */
function fromProtoRole(role: ProtoRole): "system" | "user" | "assistant" | "tool" | "function" {
    switch (role) {
        case ProtoRole.SYSTEM:
            return "system";
        case ProtoRole.USER:
            return "user";
        case ProtoRole.ASSISTANT:
            return "assistant";
        case ProtoRole.FUNCTION:
            return "function";
        case ProtoRole.TOOL:
            return "tool";
        default:
            return "user";
    }
}

/**
 * Extract text content from a message
 */
function getMessageContent(msg: Message): string {
    if (typeof msg.content === "string") {
        return msg.content;
    }
    if (Array.isArray(msg.content)) {
        return msg.content
            .filter((part) => part.type === "text")
            .map((part) => (part as { type: "text"; text: string }).text)
            .join("");
    }
    return "";
}

/**
 * Transform proto response to OpenAI-compatible format
 */
function transformToolCalls(calls: ProtoToolCall[]): ToolCall[] | undefined {
    if (!calls || calls.length === 0) return undefined;
    return calls.map((tc) => ({
        id: tc.id,
        type: "function" as const,
        function: {
            name: tc.function?.name ?? "",
            arguments: tc.function?.arguments ?? "",
        },
    }));
}

function transformResponse(proto: ProtoChatResponse): ChatCompletionResponse {
    return {
        id: proto.id,
        object: "chat.completion",
        created: Number(proto.created),
        model: proto.model,
        choices: proto.choices.map((c) => ({
            index: c.index,
            message: {
                role: "assistant" as const,
                content: c.message?.content?.[0]?.data?.case === "text"
                    ? c.message.content[0].data.value
                    : null,
                tool_calls: transformToolCalls(c.message?.toolCalls ?? []),
            },
            finish_reason: (c.finishReason as "stop" | "length" | "tool_calls" | "content_filter") || null,
            logprobs: null,
        })),
        usage: {
            prompt_tokens: proto.usage?.promptTokens ?? 0,
            completion_tokens: proto.usage?.completionTokens ?? 0,
            total_tokens: proto.usage?.totalTokens ?? 0,
        },
        fallback_info: proto.fallbackInfo
            ? {
                fallback_used: proto.fallbackInfo.fallbackUsed,
                requested_model: proto.fallbackInfo.requestedModel,
                actual_model: proto.fallbackInfo.actualModel,
                fallback_reason: proto.fallbackInfo.fallbackReason || undefined,
                fallback_attempts: proto.fallbackInfo.fallbackAttempts || undefined,
            }
            : undefined,
    };
}

/**
 * Transform proto chunk to streaming format
 */
function transformChunkToolCalls(calls: ProtoToolCall[]): ChatCompletionDelta["tool_calls"] {
    if (!calls || calls.length === 0) return undefined;
    return calls.map((tc, index) => ({
        index,
        id: tc.id || undefined,
        type: "function" as const,
        function: {
            name: tc.function?.name || undefined,
            arguments: tc.function?.arguments || undefined,
        },
    }));
}

function transformChunk(proto: ProtoChatResponse, isFirst: boolean): ChatCompletionChunk {
    return {
        id: proto.id,
        object: "chat.completion.chunk",
        created: Number(proto.created),
        model: proto.model,
        choices: proto.choices.map((c) => ({
            index: c.index,
            delta: {
                role: isFirst ? fromProtoRole(c.message?.role ?? ProtoRole.ASSISTANT) : undefined,
                content: c.message?.content?.[0]?.data?.case === "text"
                    ? c.message.content[0].data.value
                    : undefined,
                tool_calls: transformChunkToolCalls(c.message?.toolCalls ?? []),
            },
            finish_reason: c.finishReason ? (c.finishReason as "stop" | "length" | "tool_calls") : null,
            logprobs: null,
        })),
        usage: proto.usage
            ? {
                prompt_tokens: proto.usage.promptTokens,
                completion_tokens: proto.usage.completionTokens,
                total_tokens: proto.usage.totalTokens,
            }
            : undefined,
    };
}

/**
 * Chat completions resource
 */
export class Completions<TModels extends string = AllModels> {
    /** @internal */
    constructor(private readonly _client: Client<typeof GatewayService>) { }

    /**
     * Creates a chat completion (non-streaming)
     *
     * @example
     * ```typescript
     * const response = await client.chat.completions.create({
     *   model: "@openai/gpt-4o",
     *   messages: [{ role: "user", content: "Hello!" }],
     * });
     * console.log(response.choices[0].message.content);
     * ```
     */
    async create(params: ChatCompletionParams<TModels> & { stream?: false }): Promise<ChatCompletionResponse>;

    /**
     * Creates a streaming chat completion
     *
     * @example
     * ```typescript
     * const stream = await client.chat.completions.create({
     *   model: "@openai/gpt-4o",
     *   messages: [{ role: "user", content: "Hello!" }],
     *   stream: true,
     * });
     *
     * for await (const chunk of stream) {
     *   process.stdout.write(chunk.choices[0]?.delta?.content ?? "");
     * }
     * ```
     */
    async create(params: ChatCompletionParams<TModels> & { stream: true }): Promise<AsyncIterable<ChatCompletionChunk>>;

    /**
     * Creates a chat completion
     */
    async create(
        params: ChatCompletionParams<TModels>
    ): Promise<ChatCompletionResponse | AsyncIterable<ChatCompletionChunk>> {
        try {
            // Build proto messages
            const messages = params.messages.map((msg) => {
                const m = create(MessageSchema, {
                    role: toProtoRole(msg.role),
                    content: [
                        {
                            type: "text",
                            data: { case: "text", value: getMessageContent(msg) },
                        },
                    ],
                });
                if ("tool_call_id" in msg && msg.tool_call_id) {
                    m.toolCallId = msg.tool_call_id;
                }
                return m;
            });

            // Parse model string to extract provider and model name
            // The gateway expects just the model name (e.g., "gpt-4o") not "@provider/model"
            const { modelName } = parseModelString(params.model);

            // Build request
            const request = create(ChatCompletionRequestSchema, {
                model: modelName,
                messages,
                stream: params.stream ?? false,
            });

            // Add tools if provided
            if (params.tools && params.tools.length > 0) {
                request.tools = params.tools.map((tool) =>
                    create(ToolSchema, {
                        type: tool.type,
                        function: create(ToolFunctionDefSchema, {
                            name: tool.function.name,
                            description: tool.function.description ?? "",
                            parameters: (tool.function.parameters ?? {}) as JsonObject,
                        }),
                    })
                );
            }

            // Add tool_choice if provided
            if (params.tool_choice !== undefined) {
                if (typeof params.tool_choice === "string") {
                    request.toolChoice = create(ToolChoiceSchema, {
                        choice: { case: "mode", value: params.tool_choice },
                    });
                } else if (typeof params.tool_choice === "object" && params.tool_choice.type === "function") {
                    request.toolChoice = create(ToolChoiceSchema, {
                        choice: {
                            case: "specificTool",
                            value: create(ToolSchema, {
                                type: "function",
                                function: create(ToolFunctionDefSchema, {
                                    name: params.tool_choice.function.name,
                                }),
                            }),
                        },
                    });
                }
            }

            // Add sampling params if provided
            if (
                params.temperature !== undefined ||
                params.top_p !== undefined ||
                params.max_tokens !== undefined ||
                params.max_completion_tokens !== undefined ||
                params.stop !== undefined
            ) {
                request.sampling = create(SamplingParamsSchema, {
                    temperature: params.temperature ?? 0,
                    topP: params.top_p ?? 0,
                    maxTokens: params.max_tokens ?? 0,
                    maxCompletionTokens: params.max_completion_tokens ?? 0,
                    stop: Array.isArray(params.stop) ? params.stop : params.stop ? [params.stop] : [],
                });
            }

            if (params.stream) {
                return this._createStream(request);
            }

            // Non-streaming: collect all chunks
            const chunks: ProtoChatResponse[] = [];
            for await (const chunk of this._client.chatCompletion(request)) {
                chunks.push(chunk);
            }

            // Return the last chunk transformed
            const lastChunk = chunks[chunks.length - 1];
            if (!lastChunk) {
                throw new Error("No response received from server");
            }

            return transformResponse(lastChunk);
        } catch (error) {
            throw fromConnectError(error);
        }
    }

    /**
     * Creates a streaming response
     */
    private async *_createStream(request: ReturnType<typeof create<typeof ChatCompletionRequestSchema>>): AsyncIterable<ChatCompletionChunk> {
        let isFirst = true;
        try {
            for await (const chunk of this._client.chatCompletion(request)) {
                yield transformChunk(chunk, isFirst);
                isFirst = false;
            }
        } catch (error) {
            throw fromConnectError(error);
        }
    }
}

/**
 * Chat resource with completions sub-resource
 */
export class Chat<TModels extends string = AllModels> {
    readonly completions: Completions<TModels>;

    /** @internal */
    constructor(client: Client<typeof GatewayService>) {
        this.completions = new Completions<TModels>(client);
    }
}
