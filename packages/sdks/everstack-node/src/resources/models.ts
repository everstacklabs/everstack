/**
 * Models resource
 *
 * Provides access to the list of available models.
 */

import type { Client } from "@connectrpc/connect";
import type { GatewayService } from "@everstack/proto/everstack/gateway/v1/gateway_service_pb.js";
import { create } from "@bufbuild/protobuf";
import { ListModelsRequestSchema } from "@everstack/proto/everstack/gateway/v1/gateway_pb.js";

import type { Model, ModelsListResponse } from "../types/models.js";
import { fromConnectError } from "../errors.js";

/**
 * Models resource for listing available models
 */
export class Models {
    /** @internal */
    constructor(private readonly _client: Client<typeof GatewayService>) { }

    /**
     * Lists available models for the authenticated user
     *
     * @example
     * ```typescript
     * const models = await client.models.list();
     * console.log(models.data.map(m => m.id));
     * ```
     */
    async list(): Promise<ModelsListResponse> {
        try {
            const request = create(ListModelsRequestSchema, {});
            const response = await this._client.listModels(request);

            // Transform proto response to OpenAI-compatible format
            // The response has providers, each with their models
            const models: Model[] = [];
            for (const provider of response.providers ?? []) {
                for (const modelName of provider.models ?? []) {
                    models.push({
                        id: `@${provider.provider}/${modelName}`,
                        object: "model" as const,
                        created: Math.floor(Date.now() / 1000),
                        owned_by: provider.provider || "everstack",
                    });
                }
            }

            return {
                object: "list",
                data: models,
            };
        } catch (error) {
            throw fromConnectError(error);
        }
    }

    /**
     * Retrieves a specific model by ID
     *
     * @param modelId - The model ID to retrieve
     */
    async retrieve(modelId: string): Promise<Model> {
        const models = await this.list();
        const model = models.data.find((m) => m.id === modelId);

        if (!model) {
            throw new Error(`Model not found: ${modelId}`);
        }

        return model;
    }
}
