"use client";

import { useRouter } from "next/navigation";
import { useCallback, useState } from "react";
import { Button } from "~/components/ui/button";
import { Card, CardContent } from "~/components/ui/card";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";

export function LoginForm() {
  const router = useRouter();
  const [key, setKey] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);

  const onSubmit = useCallback(
    async (event: React.FormEvent) => {
      event.preventDefault();
      setError(null);
      setPending(true);

      try {
        const response = await fetch("/api/auth/login", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ key }),
        });

        if (!response.ok) {
          const body = (await response.json().catch(() => null)) as {
            error?: string;
          } | null;
          setError(body?.error ?? "Login failed.");
          return;
        }

        router.push("/");
        router.refresh();
      } catch {
        setError("Unable to reach the server.");
      } finally {
        setPending(false);
      }
    },
    [key, router],
  );

  const onKeyChange = useCallback(
    (event: React.ChangeEvent<HTMLInputElement>) => {
      setKey(event.target.value);
    },
    [],
  );

  return (
    <Card className="w-full">
      <CardContent>
        <form onSubmit={onSubmit} className="flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            <Label htmlFor="api-key">API key</Label>
            <Input
              id="api-key"
              type="password"
              autoComplete="current-password"
              placeholder="Enter API key"
              value={key}
              onChange={onKeyChange}
              disabled={pending}
            />
          </div>
          {error ? (
            <p role="alert" className="text-destructive text-sm">
              {error}
            </p>
          ) : null}
          <Button type="submit" disabled={pending || !key}>
            {pending ? "Signing in…" : "Sign in"}
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}
