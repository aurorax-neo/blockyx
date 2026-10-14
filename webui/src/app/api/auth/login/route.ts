import { NextResponse } from "next/server";
import { SESSION_COOKIE, SESSION_MAX_AGE, validateApiKey } from "~/server/auth";
import { getConfiguration } from "~/server/config";

/**
 * Log in with the unified blocky API key. The key is validated against blocky
 * itself; on success it is stored in an HTTP-only session cookie that the
 * server attaches to every proxied blocky call.
 */
export async function POST(request: Request) {
  let key: unknown;

  try {
    ({ key } = (await request.json()) as { key?: unknown });
  } catch {
    key = undefined;
  }

  if (typeof key !== "string" || !key) {
    return NextResponse.json(
      { error: "API key is required." },
      { status: 400 },
    );
  }

  const configuration = await getConfiguration();

  if (!(await validateApiKey(configuration, key))) {
    return NextResponse.json({ error: "Invalid API key." }, { status: 401 });
  }

  const response = NextResponse.json({ ok: true });

  response.cookies.set(SESSION_COOKIE, encodeURIComponent(key), {
    httpOnly: true,
    sameSite: "lax",
    path: "/",
    maxAge: SESSION_MAX_AGE,
  });

  return response;
}
