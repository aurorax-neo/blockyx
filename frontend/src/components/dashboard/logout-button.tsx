"use client";

import { LogOut } from "lucide-react";
import { Button } from "~/components/ui/button";

export function LogoutButton({ onLogout }: { onLogout: () => void }) {
  return (
    <Button variant="ghost" size="icon" onClick={onLogout} title="Sign out">
      <LogOut className="size-4" />
    </Button>
  );
}
