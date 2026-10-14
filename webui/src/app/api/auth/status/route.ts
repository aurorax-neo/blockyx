import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { isLoginRequired, SESSION_COOKIE } from "~/server/auth";
import { getConfiguration } from "~/server/config";

/** Whether the UI currently requires login, and whether this browser is in. */
export async function GET() {
  const configuration = await getConfiguration();
  const loginRequired = await isLoginRequired(configuration);

  if (!loginRequired) {
    return NextResponse.json({ loginRequired, authenticated: true });
  }

  const store = await cookies();
  const authenticated = Boolean(store.get(SESSION_COOKIE)?.value);

  return NextResponse.json({ loginRequired, authenticated });
}
