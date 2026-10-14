"use client";

import { LogOut } from "lucide-react";
import { useRouter } from "next/navigation";
import { useCallback, useState } from "react";
import { Button } from "~/components/ui/button";

export function LogoutButton() {
  const router = useRouter();
  const [pending, setPending] = useState(false);

  const onLogout = useCallback(async () => {
    setPending(true);
    try {
      await fetch("/api/auth/logout", { method: "POST" });
    } finally {
      router.push("/login");
      router.refresh();
    }
    // biome-ignore lint/nursery/useReactCompiler: router is stable across renders
  }, [router]);

  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={onLogout}
      disabled={pending}
      aria-label="Sign out"
    >
      <LogOut className="size-4" />
      <span className="hidden sm:inline">Sign out</span>
    </Button>
  );
}
