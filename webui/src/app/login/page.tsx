import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { isLoginRequired, SESSION_COOKIE } from "~/server/auth";
import { getConfiguration } from "~/server/config";
import { LoginForm } from "./login-form";

export const dynamic = "force-dynamic";

export default async function LoginPage() {
  const configuration = await getConfiguration();

  // Already inside (or no login needed): don't show the form.
  if (!(await isLoginRequired(configuration))) {
    redirect("/");
  }

  const store = await cookies();
  if (store.get(SESSION_COOKIE)?.value) {
    redirect("/");
  }

  return (
    <main className="container mx-auto flex min-h-[80vh] max-w-md flex-col items-center justify-center px-4 py-8">
      <h1 className="mb-2 font-bold font-title text-4xl sm:text-5xl">
        {"\u003e "}BlockyUI
      </h1>
      <p className="mb-8 text-muted-foreground text-sm">
        Enter the API key to access the dashboard.
      </p>
      <LoginForm />
      <p className="mt-6 text-center text-muted-foreground text-xs">
        The same key configured as <code>api.apiKey</code> on the DNS server (or
        the <code>BLOCKYX_API_KEY</code> environment variable).
      </p>
    </main>
  );
}
