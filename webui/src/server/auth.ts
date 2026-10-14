/**
 * blockyx UI authentication, backed by the unified blocky API key.
 *
 * The browser never talks to blocky directly: the Next.js server proxies every
 * blocky call. A successful login stores the API key in an HTTP-only session
 * cookie; server-side blocky clients then send it as `X-API-Key`, falling back
 * to the key configured via `BLOCKY_REQUEST_HEADERS` when no session exists.
 */
import type { Configuration } from "~/server/config/schema";

export const SESSION_COOKIE = "blockyx_api_key";
export const SESSION_MAX_AGE = 60 * 60 * 24 * 30; // 30 days

type ServerEntry = Configuration["servers"][string];

function defaultServer(configuration: Configuration): ServerEntry | undefined {
  return (
    configuration.servers.default ?? Object.values(configuration.servers)[0]
  );
}

/** Extract the API key from a headers record (`X-API-Key` or `Authorization: Bearer`). */
export function apiKeyFromHeaders(
  headers: Record<string, string> | undefined,
): string | undefined {
  if (!headers) {
    return undefined;
  }

  for (const [name, value] of Object.entries(headers)) {
    const lower = name.toLowerCase();
    if (lower === "x-api-key" && value) {
      return value;
    }
    if (
      lower === "authorization" &&
      value.toLowerCase().startsWith("bearer ")
    ) {
      return value.slice("bearer ".length);
    }
  }

  return undefined;
}

/** Parse the session cookie value out of a raw `Cookie` header. */
export function sessionKeyFromCookieHeader(
  cookieHeader: string | null | undefined,
): string | undefined {
  if (!cookieHeader) {
    return undefined;
  }

  for (const part of cookieHeader.split(";")) {
    const idx = part.indexOf("=");
    if (idx < 0) {
      continue;
    }
    if (part.slice(0, idx).trim() === SESSION_COOKIE) {
      const value = part.slice(idx + 1).trim();
      try {
        return decodeURIComponent(value) || undefined;
      } catch {
        return value || undefined;
      }
    }
  }

  return undefined;
}

/** The key configured on the server via `BLOCKY_REQUEST_HEADERS` (may be absent). */
export function getConfiguredApiKey(
  configuration: Configuration,
): string | undefined {
  return apiKeyFromHeaders(defaultServer(configuration)?.headers);
}

/** Resolve the key for outbound blocky calls: session cookie wins over env config. */
export function resolveApiKey(
  configuration: Configuration,
  cookieHeader: string | null | undefined,
): string | undefined {
  return (
    sessionKeyFromCookieHeader(cookieHeader) ??
    getConfiguredApiKey(configuration)
  );
}

/**
 * Check a candidate key against blocky itself: the key is valid when blocky
 * answers `/api/blocking/status` with 2xx.
 */
export async function validateApiKey(
  configuration: Configuration,
  key: string,
): Promise<boolean> {
  const server = defaultServer(configuration);
  if (!(server && key)) {
    return false;
  }

  try {
    const response = await fetch(`${server.url}/api/blocking/status`, {
      headers: { "X-API-Key": key },
      signal: AbortSignal.timeout(5000),
    });

    return response.ok;
  } catch {
    return false;
  }
}

let probeCache: { required: boolean; at: number } | undefined;
const PROBE_TTL_MS = 60_000;

/**
 * Whether the login gate is active: true when blocky rejects keyless API
 * calls with 401. Demo mode never requires login. The probe result is cached
 * briefly; an unreachable blocky fails open so outages stay diagnosable.
 */
export async function isLoginRequired(
  configuration: Configuration,
): Promise<boolean> {
  if (configuration.demoMode) {
    return false;
  }

  const now = Date.now();
  if (probeCache && now - probeCache.at < PROBE_TTL_MS) {
    return probeCache.required;
  }

  let required = false;
  const server = defaultServer(configuration);

  if (server) {
    try {
      const response = await fetch(`${server.url}/api/blocking/status`, {
        signal: AbortSignal.timeout(5000),
      });

      required = response.status === 401;
    } catch {
      required = false;
    }
  }

  probeCache = { required, at: now };

  return required;
}

/** Test-only hook to reset the probe cache. */
export function resetLoginProbeCache(): void {
  probeCache = undefined;
}
