"use client";

import { createContext, useContext } from "react";
import type { ServerSummary, StatusResult } from "~/lib/blocky-api";
import type {
  BlockyStatistics,
  createStatisticsSnapshot,
} from "~/lib/blocky-statistics";

export type StatisticsSnapshot = ReturnType<typeof createStatisticsSnapshot> & {
  summary: BlockyStatistics["summary"];
  answered: number;
};

export interface StatisticsResult {
  serverId: string;
  success: boolean;
  data?: StatisticsSnapshot;
  error?: { kind: string; message: string };
}

/** Single-server selection shim (the merged binary manages itself). */
export interface ServerSelection {
  selected: (scope: string) => string[];
  toggle: (scope: string) => (id: string) => void;
}

export interface DashboardServers {
  servers: ServerSummary[];
  selection: ServerSelection;
  statuses: StatusResult[] | undefined;
  loading: boolean;
  statusUpdatedAt: number;
  statistics: StatisticsResult[] | undefined;
  statisticsLoading: boolean;
  refetchStatus?: () => Promise<unknown>;
  isFetchingStatus?: boolean;
  refetchStatistics?: () => Promise<unknown>;
  isFetchingStatistics?: boolean;
  reportDiagnostics: (
    key: string,
    diagnostics: { sourceId: string; message: string }[],
  ) => void;
}

export const ServerContext = createContext<DashboardServers | null>(null);

export function useDashboardServers() {
  const context = useContext(ServerContext);
  if (!context) {
    throw new Error("Dashboard components require a server context.");
  }
  return context;
}
