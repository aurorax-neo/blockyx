"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { toast } from "sonner";
import { createApiClient } from "~/lib/blocky-api";
import {
  type commandSchema,
  executeCommand,
} from "~/lib/blocky-commands";
import type { z } from "zod";

type Command = z.infer<typeof commandSchema>;

export function useServerCommand(_scope: "blocking" | "maintenance") {
  const queryClient = useQueryClient();
  const client = useMemo(() => createApiClient(), []);
  const [isPending, setIsPending] = useState(false);

  async function execute(command: Command) {
    if (isPending) {
      return;
    }

    setIsPending(true);

    try {
      await executeCommand(client, command);
      toast.success("Completed");
    } catch (error) {
      toast.error("Unable to complete the action", {
        description: error instanceof Error ? error.message : undefined,
      });
    } finally {
      setIsPending(false);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["blockingStatus"] }),
        queryClient.invalidateQueries({ queryKey: ["statistics"] }),
      ]);
    }
  }

  return {
    execute,
    isPending,
  };
}
