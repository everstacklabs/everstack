/**
 * Transport layer for Everstack SDK
 *
 * Creates Connect-RPC transports with authentication interceptors
 * for communicating with the Everstack gateway.
 */

import { createConnectTransport as createWebConnectTransport } from "@connectrpc/connect-web";
import { createConnectTransport as createNodeConnectTransport } from "@connectrpc/connect-node";
import type { Interceptor, Transport } from "@connectrpc/connect";

/**
 * Options for creating a Everstack transport
 */
export interface TransportOptions {
    /** Base URL of the Everstack gateway */
    baseUrl: string;
    /** Everstack API key */
    apiKey: string;
    /** Default provider (e.g., "@openai") */
    provider?: string;
    /** Organization ID for multi-tenant setups */
    orgId?: string;
    /** User ID for tracking/attribution */
    userId?: string;
    /** Additional headers to include */
    headers?: Record<string, string>;
}

/**
 * Header names used by the Everstack gateway
 */
export const Headers = {
    API_KEY: "x-evs-api-key",
    PROVIDER: "x-evs-provider",
    ORG_ID: "x-evs-org-id",
    USER_ID: "x-evs-user-id",
    TENANT_ID: "x-evs-tenant-id",
} as const;

/**
 * Creates an interceptor that adds Everstack authentication headers to all requests
 */
export function createAuthInterceptor(options: TransportOptions): Interceptor {
    return (next) => async (req) => {
        // Add API key (required)
        req.header.set(Headers.API_KEY, options.apiKey);

        // Add optional headers
        if (options.provider) {
            req.header.set(Headers.PROVIDER, options.provider);
        }
        if (options.orgId) {
            req.header.set(Headers.ORG_ID, options.orgId);
        }
        if (options.userId) {
            req.header.set(Headers.USER_ID, options.userId);
        }

        // Add any additional custom headers
        if (options.headers) {
            for (const [key, value] of Object.entries(options.headers)) {
                req.header.set(key, value);
            }
        }

        return next(req);
    };
}

/**
 * Creates a Connect transport for browser environments
 * Uses HTTP/1.1 with the Connect protocol
 */
export function createWebTransport(options: TransportOptions): Transport {
    return createWebConnectTransport({
        baseUrl: options.baseUrl,
        interceptors: [createAuthInterceptor(options)],
    });
}

/**
 * Creates a transport for Node.js environments.
 *
 * Uses the Connect protocol over HTTP/2 (not native gRPC). Native gRPC carries
 * the call status in HTTP/2 trailers, which the managed gateway's ingress does
 * not forward — the client then fails with "protocol error: missing status".
 * The Connect protocol carries the status inside the response framing instead,
 * so it survives proxies while keeping HTTP/2 streaming.
 */
export function createNodeTransport(options: TransportOptions): Transport {
    return createNodeConnectTransport({
        baseUrl: options.baseUrl,
        httpVersion: "2",
        interceptors: [createAuthInterceptor(options)],
    });
}

/**
 * Detects the runtime environment and creates the appropriate transport
 */
export function createTransport(options: TransportOptions): Transport {
    // Check if we're in a browser environment
    const isBrowser = typeof window !== "undefined" && typeof window.document !== "undefined";

    if (isBrowser) {
        return createWebTransport(options);
    }

    return createNodeTransport(options);
}
