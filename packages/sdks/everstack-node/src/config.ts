/**
 * Gateway configuration types and utilities
 *
 * Used for configuring the Everstack gateway with type-safe model selection.
 *
 * @example
 * ```typescript
 * import { GatewayConfig, allModels } from '@everstack/everstack/config';
 *
 * const config: GatewayConfig = {
 *   models: [
 *     '@openai/gpt-4o',
 *     '@anthropic/claude-3-opus',
 *   ],
 *   defaults: {
 *     temperature: 0.7,
 *     maxTokens: 1000,
 *   },
 * };
 * ```
 */

// Import and re-export model types for configuration
import {
    type AllModels,
    type AnthropicModel,
    type CohereModel,
    type GoogleModel,
    type HuggingfaceModel,
    type MistralModel,
    type ModelMetadata,
    type OpenaiModel,
    type OpenrouterModel,
    type Provider,
    allModels,
    getModelMetadata,
    getModelsByProvider,
    isValidModel,
    modelMetadata,
    parseModelId,
    providers,
} from "./generated/models.js";

export {
    type AllModels,
    type AnthropicModel,
    type CohereModel,
    type GoogleModel,
    type HuggingfaceModel,
    type MistralModel,
    type ModelMetadata,
    type OpenaiModel,
    type OpenrouterModel,
    type Provider,
    allModels,
    getModelMetadata,
    getModelsByProvider,
    isValidModel,
    modelMetadata,
    parseModelId,
    providers,
};

/**
 * Default sampling parameters for the gateway
 */
export interface SamplingDefaults {
    /** Default temperature (0-2) */
    temperature?: number;
    /** Default top_p (0-1) */
    topP?: number;
    /** Default max tokens */
    maxTokens?: number;
    /** Default stop sequences */
    stop?: string[];
}

/**
 * Rate limiting configuration
 */
export interface RateLimitConfig {
    /** Requests per minute */
    requestsPerMinute?: number;
    /** Tokens per minute */
    tokensPerMinute?: number;
    /** Concurrent requests */
    concurrentRequests?: number;
}

/**
 * Model configuration for the gateway
 */
export interface ModelConfig {
    /** Model ID in @provider/model format */
    id: string;
    /** Optional alias for the model */
    alias?: string;
    /** Model-specific sampling defaults */
    defaults?: SamplingDefaults;
    /** Model-specific rate limits */
    rateLimits?: RateLimitConfig;
    /** Whether this model is enabled */
    enabled?: boolean;
}

/**
 * Gateway configuration
 */
export interface GatewayConfig {
    /** List of enabled models (can be model IDs or ModelConfig objects) */
    models: (string | ModelConfig)[];
    /** Global sampling defaults */
    defaults?: SamplingDefaults;
    /** Global rate limits */
    rateLimits?: RateLimitConfig;
    /** Fallback configuration */
    fallback?: {
        /** Enable automatic fallback */
        enabled: boolean;
        /** Fallback model IDs in priority order */
        models?: string[];
    };
}

/**
 * Validates a gateway configuration
 */
export function validateConfig(config: GatewayConfig): { valid: boolean; errors: string[] } {
    const errors: string[] = [];

    if (!config.models || config.models.length === 0) {
        errors.push("At least one model must be configured");
    }

    for (const model of config.models) {
        const modelId = typeof model === "string" ? model : model.id;
        if (!isValidModel(modelId)) {
            errors.push(`Unknown model: ${modelId}`);
        }
    }

    return {
        valid: errors.length === 0,
        errors,
    };
}
