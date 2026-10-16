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
    default:
      return 86_400_000;
  }
}

export function useLogTopList(options: TopListOptions, enabled = true) {
  const client = useMemo(() => createApiClient(), []);
  const alignedNow = Math.floor(Date.now() / 300_000) * 300_000;
  const to = useMemo(
    () => new Date(alignedNow).toISOString(),
    [options.range, alignedNow],
  );
  const from = useMemo(() => {
    const span = rangeToMs(options.range);
    const start =
      options.range === "1h"
        ? alignedNow - span
        : Math.floor((alignedNow - span) / 3_600_000) * 3_600_000;

    return new Date(start).toISOString();
  }, [options.range, alignedNow]);

  const query = useQuery({
    queryKey: ["querylog", "top", options, from, to],
    placeholderData: (previousData) => previousData,
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
          percentage: item.total > 0 ? (item.blocked / item.total) * 100 : 0,
        })),
        totalCount: result.totalCount,
      };
    },
    enabled,
  });

  return query;
}
