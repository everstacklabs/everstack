// Routes reachable without a session. They render outside the dashboard
// layout and the auth guard lets them through: each one is how a signed-out
// user gets a session.
export const PUBLIC_AUTH_ROUTES = [
    '/login',
    '/register',
    '/invite',
    '/device',
    // Opened from the magic-link email; it signs the user in.
    '/verify-magic-link',
] as const

export function isPublicAuthRoute(pathname: string): boolean {
    return PUBLIC_AUTH_ROUTES.some((route) => pathname.startsWith(route))
}
