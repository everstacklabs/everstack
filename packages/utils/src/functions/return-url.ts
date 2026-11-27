/**
 * The single place the Cloud console and the instance admin decide where a
 * user may be sent after an auth step (sign-in, SSO step-up, invite, device or
 * instance login).
 *
 * Every accepted target is rebuilt from a parsed URL. The input string is
 * never handed back, so prefix tricks, backslashes, userinfo, odd casing and
 * stray whitespace cannot survive into window.location.
 */
export interface ReturnURLPolicy {
  /** The Cloud console origin, normally window.location.origin. */
  currentOrigin: string;
  /**
   * Base domains whose direct subdomains are tenant instances, for example
   * "eu-gra-1.everstack.ai". Matched on the parsed hostname with a dot
   * boundary and exactly one extra label.
   */
  tenantBaseDomains?: readonly string[];
  /** Accept loopback instance origins. Only for a console served from loopback. */
  allowLoopbackInstances?: boolean;
}

export type ReturnTarget =
  /** A same-origin route. `fromAbsoluteURL` is true when the input named our origin explicitly. */
  | { kind: "path"; path: string; fromAbsoluteURL: boolean }
  /** An allowlisted tenant instance. `origin` is the only part callers should build on. */
  | { kind: "instance"; origin: string; url: string }
  | { kind: "fallback"; path: string };

const UNSAFE_CHARACTERS = /[\u0000-\u001F\u007F\\]/;
const ABSOLUTE_HTTP_URL = /^https?:\/\//i;
const DNS_LABEL = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/;
const LOOPBACK_HOSTS = new Set(["localhost", "127.0.0.1", "[::1]"]);

export function resolveReturnTarget(
  raw: unknown,
  policy: ReturnURLPolicy,
  fallback = "/",
): ReturnTarget {
  const fail: ReturnTarget = { kind: "fallback", path: fallback };
  if (typeof raw !== "string" || raw === "") return fail;

  const value = decodeWholeValueOnce(raw);
  if (value === null) return fail;
  if (value !== value.trim() || UNSAFE_CHARACTERS.test(value)) return fail;

  let currentOrigin: string;
  try {
    currentOrigin = new URL(policy.currentOrigin).origin;
  } catch {
    return fail;
  }

  if (value.startsWith("/")) {
    if (value.startsWith("//") || hasEncodedAuthorityPrefix(value)) return fail;
    let parsed: URL;
    try {
      parsed = new URL(value, currentOrigin);
    } catch {
      return fail;
    }
    if (parsed.origin !== currentOrigin) return fail;
    // Dot segments ("/.//host", "/a/..//host", "/%2e//host") only collapse
    // into an authority after parsing, so check the normalized path too.
    if (hasEncodedAuthorityPrefix(parsed.pathname)) return fail;
    return { kind: "path", path: pathOf(parsed), fromAbsoluteURL: false };
  }

  if (!ABSOLUTE_HTTP_URL.test(value)) return fail;

  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return fail;
  }
  if (parsed.username || parsed.password) return fail;

  if (parsed.origin === currentOrigin) {
    if (hasEncodedAuthorityPrefix(parsed.pathname)) return fail;
    return { kind: "path", path: pathOf(parsed), fromAbsoluteURL: true };
  }

  if (
    isTenantInstance(parsed, policy.tenantBaseDomains ?? []) ||
    (policy.allowLoopbackInstances === true && isLoopbackInstance(parsed))
  ) {
    return {
      kind: "instance",
      origin: parsed.origin,
      url: `${parsed.origin}${pathOf(parsed)}`,
    };
  }

  return fail;
}

/**
 * Same-origin route for a return URL, or the fallback. Use this where only an
 * in-console destination makes sense (SSO step-up, invite, device flows).
 */
export function safeReturnPath(
  raw: unknown,
  currentOrigin: string,
  fallback = "/",
): string {
  const target = resolveReturnTarget(raw, { currentOrigin }, fallback);
  return target.kind === "path" ? target.path : fallback;
}

/** True for hostnames that only ever name the local machine. */
export function isLoopbackHostname(hostname: string): boolean {
  return LOOPBACK_HOSTS.has(hostname);
}

export function parseTenantBaseDomains(value: string): string[] {
  return value
    .split(",")
    .map((domain) => domain.trim().toLowerCase().replace(/^\.+|\.+$/g, ""))
    .filter(
      (domain) =>
        domain.includes(".") && domain.split(".").every((label) => DNS_LABEL.test(label)),
    );
}

/**
 * A self-hosted gateway base URL from the license upgrade flow. These are
 * customer hosts, so no host allowlist applies, but only a plain http(s)
 * origin and path survive: no other scheme, no credentials, no query.
 */
export function safeGatewayBaseURL(raw: unknown): string | undefined {
  if (typeof raw !== "string") return undefined;
  if (raw !== raw.trim() || UNSAFE_CHARACTERS.test(raw)) return undefined;
  if (!ABSOLUTE_HTTP_URL.test(raw)) return undefined;
  let parsed: URL;
  try {
    parsed = new URL(raw);
  } catch {
    return undefined;
  }
  if (parsed.username || parsed.password) return undefined;
  return `${parsed.origin}${parsed.pathname.replace(/\/+$/, "")}`;
}

// A return URL is sometimes encoded as a whole before it reaches us (for
// example an OAuth continuation carried through another query string). Decode
// exactly once, and only when the raw value is not already a navigation target.
function decodeWholeValueOnce(raw: string): string | null {
  if (raw.startsWith("/") || ABSOLUTE_HTTP_URL.test(raw)) return raw;
  try {
    return decodeURIComponent(raw);
  } catch {
    return null;
  }
}

// True when a path would be read as an authority: "//host" or "/\host",
// literally or once percent-decoded ("/%2F/host", "/%5Chost"; some routers
// decode the path before navigating). Callers run it on the raw input and
// again on the parsed, dot-segment-normalized pathname.
function hasEncodedAuthorityPrefix(value: string): boolean {
  const path = value.split(/[?#]/, 1)[0] ?? "";
  let decoded: string;
  try {
    decoded = decodeURIComponent(path);
  } catch {
    return true;
  }
  return decoded.startsWith("//") || decoded.startsWith("/\\");
}

function isTenantInstance(parsed: URL, baseDomains: readonly string[]): boolean {
  if (parsed.protocol !== "https:" || parsed.port !== "") return false;
  const hostname = parsed.hostname;
  return baseDomains.some((base) => {
    if (!hostname.endsWith(`.${base}`)) return false;
    const label = hostname.slice(0, -(base.length + 1));
    return DNS_LABEL.test(label);
  });
}

function isLoopbackInstance(parsed: URL): boolean {
  return (
    (parsed.protocol === "http:" || parsed.protocol === "https:") &&
    LOOPBACK_HOSTS.has(parsed.hostname)
  );
}

function pathOf(parsed: URL): string {
  return `${parsed.pathname}${parsed.search}${parsed.hash}`;
}
