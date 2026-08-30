/**
 * Embeddings resource
 *
 * Provides embeddings functionality.
 */

import type { Client } from "@connectrpc/connect";
import type { GatewayService } from "@everstack/proto/everstack/gateway/v1/gateway_service_pb.js";
import { create } from "@bufbuild/protobuf";
import {
    EmbeddingsRequestSchema,
    type EmbeddingsResponse as ProtoEmbeddingsResponse,
} from "@everstack/proto/everstack/gateway/v1/embedding_pb.js";

import type { EmbeddingsParams, EmbeddingsResponse } from "../types/embeddings.js";
import type { AllModels } from "../types/models.js";
import { fromConnectError } from "../errors.js";

/**
 * Parse a model string that may be in @provider/model format
 * Returns just the model name for the API call
 */
function parseModelString(model: string): string {
    const match = model.match(/^@([^/]+)\/(.+)$/);
    if (match && match[2]) {
        return match[2];
    }
    return model;
}

/**
 * Transform proto response to OpenAI-compatible format
 */
function transformResponse(proto: ProtoEmbeddingsResponse): EmbeddingsResponse {
    return {
        object: "list",
        data: proto.data.map((d) => ({
            object: "embedding",
            embedding: Array.from(d.embedding),
            index: d.index,
        })),
        model: proto.model,
        usage: {
            prompt_tokens: proto.usage?.promptTokens ?? 0,
            total_tokens: proto.usage?.totalTokens ?? 0,
        },
    };
}

/**
 * Embeddings resource
 */
export class Embeddings<TModels extends string = AllModels> {
    /** @internal */
    constructor(private readonly _client: Client<typeof GatewayService>) { }

    /**
     * Creates embeddings for the given input
     *
     * @example
     * ```typescript
     * const response = await client.embeddings.create({
     *   model: "@openai/text-embedding-3-small",
     *   input: "Hello, world!",
     * });
     * console.log(response.data[0].embedding);
     * ```
     */
    async create(params: EmbeddingsParams<TModels>): Promise<EmbeddingsResponse> {
        try {
            // Normalize input to string
            const input = Array.isArray(params.input) ? params.input.join(" ") : params.input;

            // Parse model string to extract just the model name
            // The gateway expects just the model name (e.g., "text-embedding-3-small") not "@provider/model"
            const modelName = parseModelString(params.model);

            const request = create(EmbeddingsRequestSchema, {
                model: modelName,
                input,
                // Note: metadata is omitted as it requires JsonObject type
            });

            // The embeddings endpoint is streaming in the proto, collect all chunks
            const chunks: ProtoEmbeddingsResponse[] = [];
            for await (const chunk of this._client.embeddings(request)) {
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
}
