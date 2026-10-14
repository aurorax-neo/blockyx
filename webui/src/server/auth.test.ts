import { afterEach, describe, expect, it, vi } from "vitest";
import {
  apiKeyFromHeaders,
  getConfiguredApiKey,
  isLoginRequired,
  resetLoginProbeCache,
  resolveApiKey,
  SESSION_COOKIE,
  sessionKeyFromCookieHeader,
  validateApiKey,
} from "./auth";
import type { Configuration } from "./config/schema";

function testConfig(
  headers: Record<string, string> = {},
  demoMode = false,
): Configuration {
  return {
    demoMode,
    servers: {
      default: {
        url: "http://127.0.0.1:4000",
        headers,
      },
    },
  } as unknown as Configuration;
}

const baseConfig = testConfig();

afterEach(() => {
  resetLoginProbeCache();
  vi.unstubAllGlobals();
});

describe("apiKeyFromHeaders", () => {
  it("reads X-API-Key case-insensitively", () => {
    expect(apiKeyFromHeaders({ "X-API-Key": "k1" })).toBe("k1");
    expect(apiKeyFromHeaders({ "x-api-key": "k2" })).toBe("k2");
  });

  it("reads Authorization: Bearer", () => {
    expect(apiKeyFromHeaders({ Authorization: "Bearer tok" })).toBe("tok");
    expect(apiKeyFromHeaders({ authorization: "bearer tok" })).toBe("tok");
  });

  it("returns undefined when absent", () => {
    expect(apiKeyFromHeaders({})).toBeUndefined();
    expect(apiKeyFromHeaders(undefined)).toBeUndefined();
    expect(apiKeyFromHeaders({ "X-Other": "x" })).toBeUndefined();
  });
});

describe("sessionKeyFromCookieHeader", () => {
  it("parses the session cookie among others", () => {
    expect(
      sessionKeyFromCookieHeader(`a=1; ${SESSION_COOKIE}=secret123; b=2`),
    ).toBe("secret123");
  });

  it("decodes percent-encoded values", () => {
    expect(sessionKeyFromCookieHeader(`${SESSION_COOKIE}=a%20b`)).toBe("a b");
  });

  it("returns undefined when missing", () => {
    expect(sessionKeyFromCookieHeader("a=1; b=2")).toBeUndefined();
    expect(sessionKeyFromCookieHeader(null)).toBeUndefined();
    expect(sessionKeyFromCookieHeader(undefined)).toBeUndefined();
  });
});

describe("resolveApiKey", () => {
  const withEnv = testConfig({ "X-API-Key": "env-key" });

  it("session cookie wins over configured key", () => {
    expect(resolveApiKey(withEnv, `${SESSION_COOKIE}=session-key`)).toBe(
      "session-key",
    );
  });

  it("falls back to configured key", () => {
    expect(resolveApiKey(withEnv, null)).toBe("env-key");
  });

  it("undefined when neither is set", () => {
    expect(resolveApiKey(baseConfig, null)).toBeUndefined();
  });

  it("getConfiguredApiKey reads Bearer too", () => {
    const cfg = testConfig({ Authorization: "Bearer b1" });

    expect(getConfiguredApiKey(cfg)).toBe("b1");
  });
});

describe("validateApiKey", () => {
  it("true on 2xx, false on 401", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.resolve(new Response("{}", { status: 200 }))),
    );
    expect(await validateApiKey(baseConfig, "good")).toBe(true);

    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.resolve(new Response("{}", { status: 401 }))),
    );
    expect(await validateApiKey(baseConfig, "bad")).toBe(false);
  });

  it("false when blocky is unreachable", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.reject(new Error("down"))),
    );
    expect(await validateApiKey(baseConfig, "k")).toBe(false);
  });

  it("sends the key as X-API-Key", async () => {
    let seen: string | null = null;
    vi.stubGlobal(
      "fetch",
      vi.fn((_url: string, init: RequestInit) => {
        seen = new Headers(init.headers).get("X-API-Key");
        return Promise.resolve(new Response("{}", { status: 200 }));
      }),
    );
    await validateApiKey(baseConfig, "my-key");
    expect(seen).toBe("my-key");
  });
});

describe("isLoginRequired", () => {
  it("true when blocky answers 401 without a key", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.resolve(new Response("{}", { status: 401 }))),
    );
    expect(await isLoginRequired(baseConfig)).toBe(true);
  });

  it("false when blocky answers 200 without a key", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.resolve(new Response("{}", { status: 200 }))),
    );
    expect(await isLoginRequired(baseConfig)).toBe(false);
  });

  it("false in demo mode without probing", async () => {
    const spy = vi.fn(() => Promise.resolve(new Response("{}", { status: 401 })));
    vi.stubGlobal("fetch", spy);
    expect(await isLoginRequired(testConfig({}, true))).toBe(
      false,
    );
    expect(spy).not.toHaveBeenCalled();
  });

  it("fails open when blocky is unreachable", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.reject(new Error("down"))),
    );
    expect(await isLoginRequired(baseConfig)).toBe(false);
  });
});
