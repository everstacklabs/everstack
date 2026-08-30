import { createClientTransport } from "@everstack/client/web";
import { getApiBaseUrl } from "@/lib/api-url";
import { getActiveOrgId } from "@/lib/active-org";
import { isAuthError, redirectToCloudLogin } from "@/lib/auth-redirect";

/** Returns a sane default base URL for API calls */
export function getDefaultBaseUrl(): string {
    return getApiBaseUrl();
}

// activeOrgInterceptor stamps `x-org-id` on every outgoing RPC. The
// gateway's cookie-session middleware (cmd/serve/cookie_session_auth.go)
// reads this header to pick a tenant for users with more than one
// org membership. Single-org users don't strictly need it, but sending
// it unconditionally is cheaper than branching and gives the FE
// defense-in-depth: if the membership query ever returns the wrong row
// first, the explicit header still pins the right tenant.
//
// Shape mirrors the existing apiKeyInterceptor in
// apps/admin/src/server/events.ts — keep them parallel so future
// header-injection interceptors slot in the same way.
const activeOrgInterceptor =
    (next: (req: any) => Promise<any>) => async (req: any) => {
        const orgId = getActiveOrgId();
        if (orgId) {
            req.header.set("x-org-id", orgId);
        }
        return next(req);
    };

// authRedirectInterceptor bounces the browser to the cloud /login the
// moment any RPC fails with an auth-shaped Connect error (Unauthenticated
// or PermissionDenied). The user might hold a valid cloud session but no
// org membership for this instance — in that case AuthGuard's session
// poll happily reports "authenticated" and data queries are the only
// surface that reveals the mismatch. Without this interceptor the SPA
// renders half-loaded UI with permission-denied errors stacked behind
// it; with it the navigation supersedes the broken state on the first
// failing RPC.
const authRedirectInterceptor =
    (next: (req: any) => Promise<any>) => async (req: any) => {
        try {
            return await next(req);
        } catch (err) {
            if (isAuthError(err)) redirectToCloudLogin();
            throw err;
        }
    };

/**
 * Create a transport suitable for browser usage (grpc-web) with optional token and overrides.
 * Automatically defaults baseUrl if not provided.
 *
 * The active-org interceptor is prepended so caller-supplied interceptors
 * still run after it; they observe the org header on the outbound request
 * and can override or extend as needed.
 */
export function createServerTransport(token: string | undefined, opts: Record<string, unknown> = {}) {
    const baseUrl = (opts as any).baseUrl ?? getDefaultBaseUrl();
    const callerInterceptors = ((opts as any).interceptors as unknown[]) ?? [];
    // authRedirectInterceptor goes LAST so it wraps every caller-supplied
    // interceptor too: any of them throwing an auth-shaped error triggers
    // the bounce, not just the outermost RPC failure.
    const interceptors = [activeOrgInterceptor, ...callerInterceptors, authRedirectInterceptor];
    return createClientTransport(token, { ...opts, baseUrl, interceptors } as any);
}
