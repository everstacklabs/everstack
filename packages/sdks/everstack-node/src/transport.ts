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
    /**
     * Deadline applied to every call, in milliseconds. Connect sends this to
     * the server as the request timeout as well as enforcing it client side.
     */
    timeoutMs?: number;
    /**
     * Override the fetch implementation used by the fetch-based transports.
     * Ignored by the Node transport, which speaks HTTP/2 directly.
     */
    fetch?: typeof globalThis.fetch;
    /**
     * Force a particular runtime instead of detecting one. Useful in tests and
     * in hosts that emulate another runtime closely enough to fool detection.
     */
    runtime?: RuntimeKind;
}

/**
 * Runtimes this SDK distinguishes between, because they disagree about which
 * transport primitives actually work.
 */
export type RuntimeKind = "browser" | "node" | "bun" | "deno" | "edge";

/**
 * Detects the current runtime.
 *
 * Node is identified positively rather than by elimination: it is the only
 * runtime where the HTTP/2 transport is known to work, so anything we cannot
 * confidently identify as Node is better served by fetch. Bun reports a
 * `process.versions.node` of its own, so it has to be excluded explicitly or
 * it would be mistaken for Node and routed onto a transport that stalls.
 */
export function detectRuntime(): RuntimeKind {
    if (typeof window !== "undefined" && typeof (window as { document?: unknown }).document !== "undefined") {
        return "browser";
    }
    const versions = typeof process !== "undefined" ? process.versions : undefined;
    if (versions?.bun !== undefined) {
        return "bun";
    }
    if (typeof (globalThis as { Deno?: unknown }).Deno !== "undefined") {
        return "deno";
    }
    if (versions?.node !== undefined) {
        return "node";
    }
    return "edge";
}

/**
 * Wraps a fetch implementation so it can be used from Connect on runtimes that
 * reject `redirect: "error"`.
 *
 * Connect's fetch transports set that value unconditionally. The fetch standard
 * allows it, but Cloudflare Workers throws on it at runtime, which takes down
 * every call made from a Worker. `"manual"` is the closest supported behaviour:
 * a redirect is surfaced as an opaque response rather than followed, so a
 * misrouted call still fails instead of silently succeeding somewhere else.
 */
export function createCompatibleFetch(
    baseFetch: typeof globalThis.fetch = globalThis.fetch,
): typeof globalThis.fetch {
    return (input, init) => {
        if (init?.redirect === "error") {
            const { redirect: _unsupported, ...rest } = init;
            return baseFetch(input, { ...rest, redirect: "manual" });
        }
        return baseFetch(input, init);
    };
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
        defaultTimeoutMs: options.timeoutMs,
        ...(options.fetch ? { fetch: options.fetch } : {}),
    });
}

/**
 * Creates a transport for runtimes that have fetch but no usable HTTP/2 client:
 * Cloudflare Workers and other edge hosts, Bun, and Deno.
 *
 * Cloudflare stubs out `node:http2` without implementing it and Bun's HTTP/2
 * client stalls, so the Node transport cannot be used in any of them. The
 * Connect protocol runs perfectly well over fetch, which all of them support.
 */
export function createFetchTransport(options: TransportOptions): Transport {
    return createWebConnectTransport({
        baseUrl: options.baseUrl,
        interceptors: [createAuthInterceptor(options)],
        defaultTimeoutMs: options.timeoutMs,
        fetch: createCompatibleFetch(options.fetch),
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
        defaultTimeoutMs: options.timeoutMs,
    });
}

/**
 * Detects the runtime environment and creates the appropriate transport.
 *
 * Only Node gets the HTTP/2 transport. Every other runtime gets fetch, which is
 * the safer default: a runtime we failed to recognise is far more likely to
 * support fetch than to support `node:http2`.
 */
export function createTransport(options: TransportOptions): Transport {
    switch (options.runtime ?? detectRuntime()) {
        case "browser":
            return createWebTransport(options);
        case "node":
            return createNodeTransport(options);
        default:
            return createFetchTransport(options);
    }
}
