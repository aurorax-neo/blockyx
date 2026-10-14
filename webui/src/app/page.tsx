import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { ConnectedDashboard } from "~/components/dashboard/connected-dashboard";
import { LogoutButton } from "~/components/dashboard/logout-button";
import { DEFAULT_DEMO_CONFIGURATION, demoServers } from "~/demo/config";
import { isLoginRequired, SESSION_COOKIE } from "~/server/auth";
import { getConfiguration } from "~/server/config";
import { serverSummaries } from "~/server/config/servers";

export const dynamic = "force-dynamic";

export default async function HomePage() {
  const configuration = await getConfiguration();

  if (!configuration.demoMode && (await isLoginRequired(configuration))) {
    const store = await cookies();
    if (!store.get(SESSION_COOKIE)?.value) {
      redirect("/login");
    }
  }

  const servers = configuration.demoMode
    ? demoServers(DEFAULT_DEMO_CONFIGURATION.serverCount)
    : serverSummaries(configuration);

  return (
    <main className="container mx-auto max-w-5xl px-4 py-8 sm:py-10">
      <header className="mb-8 flex items-start justify-between sm:mb-10">
        <h1 className="font-bold font-title text-5xl sm:text-6xl">
          {"> "}BlockyUI
        </h1>
        {configuration.demoMode ? null : <LogoutButton />}
      </header>

      <ConnectedDashboard servers={servers} demoMode={configuration.demoMode} />
    </main>
  );
}
