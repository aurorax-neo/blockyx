"use client";

import { useCallback, useState } from "react";
import { Button } from "~/components/ui/button";
import { Card, CardContent } from "~/components/ui/card";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import { setApiKey, validateApiKey } from "~/lib/blocky-api";

export function LoginForm({ onSuccess }: { onSuccess: () => void }) {
  const [key, setKey] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);

  const onSubmit = useCallback(
    async (event: React.FormEvent) => {
      event.preventDefault();
      setError(null);
      setPending(true);

      try {
        const ok = await validateApiKey(key);

        if (!ok) {
          setError("Invalid API key.");
          return;
        }

        setApiKey(key);
        onSuccess();
      } catch {
        setError("Unable to reach the server.");
      } finally {
        setPending(false);
      }
    },
    [key, onSuccess],
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
