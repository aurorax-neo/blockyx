import { NextResponse } from "next/server";
import { SESSION_COOKIE } from "~/server/auth";

/** Clear the API key session cookie. */
export function POST() {
  const response = NextResponse.json({ ok: true });

  response.cookies.delete(SESSION_COOKIE);

  return response;
}
