/**
 * OpenAI SDK Compatibility Layer
 *
 * Provides utilities for using the official OpenAI SDK with the Everstack gateway.
 *
 * @example
 * ```typescript
 * import OpenAI from 'openai';
 * import { createHeaders, EVERSTACK_GATEWAY_URL } from '@everstack/everstack';
 *
 * const openai = new OpenAI({
 *   baseURL: EVERSTACK_GATEWAY_URL,
 *   defaultHeaders: createHeaders({
 *     apiKey: 'pk_...',
 *     provider: '@openai',
 *   }),
 * });
 *
 * const response = await openai.chat.completions.create({
 *   model: 'gpt-4o',
 *   messages: [{ role: 'user', content: 'Hello!' }],
 * });
 * ```
 */

/**
 * Default Everstack gateway URL
 */
export const EVERSTACK_GATEWAY_URL = "https://gateway.everstack.ai";

/**
 * Options for creating Everstack headers
 */
export interface HeaderOptions {
    /** Everstack API key (required) */
    apiKey: string;
    /** Default provider for routing (e.g., "@openai", "@anthropic") */
    provider?: string;
    /** Organization ID for multi-tenant setups */
    orgId?: string;
    /** User ID for tracking/attribution */
    userId?: string;
    /** Tenant ID */
    tenantId?: string;
}

/**
 * Creates headers for use with the OpenAI SDK or any HTTP client.
 *
 * @example
 * ```typescript
 * // With OpenAI SDK
 * import OpenAI from 'openai';
 * import { createHeaders, EVERSTACK_GATEWAY_URL } from '@everstack/everstack';
 *
 * const openai = new OpenAI({
 *   baseURL: EVERSTACK_GATEWAY_URL,
 *   defaultHeaders: createHeaders({ apiKey: 'pk_...' }),
 * });
 * ```
 *
 * @example
 * ```typescript
 * // With fetch
 * const response = await fetch(`${EVERSTACK_GATEWAY_URL}/v1/chat/completions`, {
 *   method: 'POST',
 *   headers: {
 *     'Content-Type': 'application/json',
 *     ...createHeaders({ apiKey: 'pk_...', provider: '@openai' }),
 *   },
 *   body: JSON.stringify({ model: 'gpt-4o', messages: [...] }),
 * });
 * ```
 */
export function createHeaders(options: HeaderOptions): Record<string, string> {
    const headers: Record<string, string> = {
        "x-evs-api-key": options.apiKey,
    };

    if (options.provider) {
        headers["x-evs-provider"] = options.provider;
    }
    if (options.orgId) {
        headers["x-evs-org-id"] = options.orgId;
    }
    if (options.userId) {
        headers["x-evs-user-id"] = options.userId;
    }
    if (options.tenantId) {
        headers["x-evs-tenant-id"] = options.tenantId;
    }

    return headers;
}

/**
 * Creates a configuration object for the OpenAI SDK
 *
 * @example
 * ```typescript
 * import OpenAI from 'openai';
 * import { createOpenAIConfig } from '@everstack/everstack';
 *
 * const openai = new OpenAI(createOpenAIConfig({
 *   apiKey: 'pk_...',
 *   provider: '@openai',
 * }));
 * ```
 */
export function createOpenAIConfig(options: HeaderOptions & { baseURL?: string }) {
    return {
        baseURL: options.baseURL ?? EVERSTACK_GATEWAY_URL,
        apiKey: "everstack", // OpenAI SDK requires an API key, but we use headers
        defaultHeaders: createHeaders(options),
    };
}
