"use client";

import { useCallback, useEffect, useState } from "react";
import { LoginForm } from "~/app/login/login-form";
import { ConnectedDashboard } from "~/components/dashboard/connected-dashboard";
import { LogoutButton } from "~/components/dashboard/logout-button";
import { clearApiKey, getApiKey } from "~/lib/blocky-api";

export default function HomePage() {
  // undefined = not checked yet, null = no key stored
  const [apiKey, setApiKey] = useState<string | null | undefined>(undefined);

  useEffect(() => {
    setApiKey(getApiKey());
  }, []);

  const onLogout = useCallback(() => {
    clearApiKey();
    setApiKey(null);
  }, []);

  const onLoginSuccess = useCallback(() => {
    setApiKey(getApiKey());
  }, []);

  if (apiKey === undefined) {
    return null;
  }

  if (apiKey === null) {
    return (
      <main className="container mx-auto flex min-h-svh max-w-md flex-col items-center justify-center px-4 py-8">
        <h1 className="mb-8 font-bold font-title text-5xl">{"> "}BlockyUI</h1>
        <LoginForm onSuccess={onLoginSuccess} />
      </main>
    );
  }

  return (
    <main className="container mx-auto max-w-5xl px-4 py-8 sm:py-10">
      <header className="mb-8 flex items-start justify-between sm:mb-10">
        <h1 className="font-bold font-title text-5xl sm:text-6xl">
          {"> "}BlockyUI
        </h1>
        <LogoutButton onLogout={onLogout} />
      </header>

      <ConnectedDashboard apiKey={apiKey} />
    </main>
  );
}
