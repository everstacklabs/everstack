import { GrpcTransportOptions } from "@connectrpc/connect-node";
import { createGrpcWebTransport } from "@connectrpc/connect-web";

/**
 * Create a client transport using grpc web with the given token and configuration options.
 * @param token
 * @param opts
 */
export function createClientTransport(token: string | undefined, opts: GrpcTransportOptions) {
    return createGrpcWebTransport({
        ...opts,
        // Include credentials (cookies) for session-based auth
        fetch: (input, init) => fetch(input, { ...init, credentials: 'include' }),
        // TODO: Add Authorization Bearer interceptor when we have a token
        // interceptors: [...(opts.interceptors || []), NewAuthorizationBearerInterceptor(token)],
        interceptors: [...(opts.interceptors || [])],
    });
}