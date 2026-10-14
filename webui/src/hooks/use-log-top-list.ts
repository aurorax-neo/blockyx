"use client";

import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { createApiClient } from "~/lib/blocky-api";
import type { TimeRange } from "~/lib/constants";
import { fetchLogTop } from "~/lib/querylog-api";

export interface TopListOptions {
  type: "domains" | "clients";
  range: TimeRange;
  limit: number;
  offset?: number;
  filter: "all" | "blocked";
}

function rangeToMs(range: TimeRange): number {
  switch (range) {
    case "1h":
      return 3_600_000;
    case "24h":
      return 86_400_000;
    case "7d":
      return 7 * 86_400_000;
    case "30d":
      return 30 * 86_400_000;
  }
}

export function useLogTopList(options: TopListOptions, enabled = true) {
  const client = useMemo(() => createApiClient(), []);
  const to = useMemo(() => new Date().toISOString(), [options.range]);
  const from = useMemo(
    () => new Date(Date.now() - rangeToMs(options.range)).toISOString(),
    [options.range, to],
  );

  const query = useQuery({
    queryKey: ["querylog", "top", options, from, to],
    queryFn: async () => {
      const result = await fetchLogTop(
        client,
        { from, to },
        options.type,
        options.filter,
        options.limit,
        options.offset ?? 0,
      );

      return {
        items: result.items.map((item) => ({
          name: item.name,
          count: item.total,
          blocked: item.blocked,
          percentage:
            item.total > 0 ? (item.blocked / item.total) * 100 : 0,
        })),
        totalCount: result.totalCount,
      };
    },
    enabled,
  });

  return query;
}
