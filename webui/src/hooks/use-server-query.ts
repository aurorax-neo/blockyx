"use client";

import { useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { createApiClient } from "~/lib/blocky-api";
import { parseBlockyQueryResult } from "~/lib/blocky-query";
import { BLOCKY_DNS_RECORD_TYPES } from "~/lib/constants";

export interface QueryInput {
  query: string;
  type: (typeof BLOCKY_DNS_RECORD_TYPES)[number];
}

export interface QueryResult {
  serverId: string;
  success: boolean;
  data?: ReturnType<typeof parseBlockyQueryResult>;
  error?: { message: string };
}

export function useServerQuery() {
  const client = useMemo(() => createApiClient(), []);
  const [results, setResults] = useState<QueryResult[]>([]);
  const [isPending, setIsPending] = useState(false);

  async function execute(input: QueryInput) {
    if (isPending) {
      return;
    }

    setIsPending(true);
    setResults([]);

    try {
      const raw = await client
        .post("query", { json: { query: input.query, type: input.type } })
        .json();
      const data = parseBlockyQueryResult(raw);

      setResults([{ serverId: "default", success: true, data }]);
    } catch (error) {
      toast.error("Query failed", {
        description: error instanceof Error ? error.message : undefined,
      });
      setResults([
        {
          serverId: "default",
          success: false,
          error: {
            message: error instanceof Error ? error.message : "Query failed",
          },
        },
      ]);
    } finally {
      setIsPending(false);
    }
  }

  return {
    execute,
    results,
    names: { default: "Blocky" } as Record<string, string>,
    isPending,
    pendingServerIds: isPending ? ["default"] : [],
  };
}

// Re-export for components that referenced the tRPC-era types.
export type { QueryInput as ServerQueryInput };
