import ky, { type KyInstance } from "ky";

const API_KEY_STORAGE = "blockyx_api_key";

/** API key persisted in the browser (the single binary has no server session). */
export function getApiKey(): string | null {
  if (typeof window === "undefined") {
    return null;
  }

  return window.localStorage.getItem(API_KEY_STORAGE);
}

export function setApiKey(key: string): void {
  window.localStorage.setItem(API_KEY_STORAGE, key);
}

export function clearApiKey(): void {
  window.localStorage.removeItem(API_KEY_STORAGE);
}

/**
 * Client for the blocky HTTP API. The UI is served by the blocky binary
 * itself, so the API is always same-origin at /api.
 */
export function createApiClient(apiKey?: string | null): KyInstance {
  const key = apiKey ?? getApiKey();

  return ky.create({
    prefix: "/api",
    headers: key ? { "X-API-Key": key } : {},
    throwHttpErrors: true,
  });
}

/** Validate a key against the blocky API. Resolves true when accepted. */
export async function validateApiKey(key: string): Promise<boolean> {
  try {
    const client = createApiClient(key);
    await client.get("blocking/status").json();

    return true;
  } catch {
    return false;
  }
}

// ---------------------------------------------------------------------------
// Shared shapes (single server: "default")
// ---------------------------------------------------------------------------

export interface ServerSummary {
  id: string;
  name: string;
  hasLogs: boolean;
  hasMappedLogs: boolean;
}

export interface BlockingStatus {
  enabled: boolean;
  disabledGroups?: string[];
  autoEnableInSec?: number;
}

export interface StatusResult {
  serverId: string;
  success: boolean;
  data?: BlockingStatus;
}

export const DEFAULT_SERVER: ServerSummary = {
  id: "default",
  name: "Blocky",
  hasLogs: true,
  hasMappedLogs: false,
};
