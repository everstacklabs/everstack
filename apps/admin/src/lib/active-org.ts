// Module-level holder for the user's active organization id. The transport
// interceptor in `apps/admin/src/server/index.ts` reads from here on every
// outgoing RPC; `use-auth.ts` writes when the session loads or the active
// org changes.
//
// Why a mutable module instead of React context: the ConnectRPC transport
// is constructed once at module evaluation (each `apps/admin/src/server/*.ts`
// holds a singleton client), well before any React provider tree exists.
// The interceptor needs an ambient lookup that doesn't depend on the
// component renderer being mounted.
//
// Why required: after the post-2026-05-06 P0 fix, the gateway only
// auto-resolves a tenant for single-membership users. Once a user joins a
// second org (or any FE adds an org switcher), every cookie-authenticated
// RPC will return PermissionDenied unless we send `x-org-id`.

let activeOrgId: string | null = null

export function setActiveOrgId(id: string | null | undefined): void {
    activeOrgId = id && id !== '' ? id : null
}

export function getActiveOrgId(): string | null {
    return activeOrgId
}
