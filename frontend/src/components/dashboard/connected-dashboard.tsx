"use client";

import { useQuery } from "@tanstack/react-query";
import { useCallback, useMemo } from "react";
import { Dashboard } from "~/components/dashboard/dashboard";
import {
  ServerContext,
  type ServerSelection,
  type StatisticsResult,
} from "~/components/dashboard/server-context";
import {
  createApiClient,
  DEFAULT_SERVER,
  type StatusResult,
} from "~/lib/blocky-api";
import { readBlockingStatus } from "~/lib/blocky-commands";
import {
  createStatisticsSnapshot,
  readBlockyStatistics,
} from "~/lib/blocky-statistics";

const singleSelection: ServerSelection = {
  selected: () => ["default"],
  toggle: () => noopToggle,
};

function noopToggle(): void {
  // Single-server mode: selection never changes.
}

function noopDiagnostics(): void {
  // Single-server mode: no per-server diagnostics.
}

export function ConnectedDashboard({ apiKey }: { apiKey: string }) {
  const client = useMemo(() => createApiClient(apiKey), [apiKey]);

  const status = useQuery({
    queryKey: ["blockingStatus"],
    queryFn: async (): Promise<StatusResult[]> => {
      try {
        const data = await readBlockingStatus(client);

        return [{ serverId: "default", success: true, data }];
      } catch {
        return [{ serverId: "default", success: false }];
      }
    },
    refetchInterval: 30_000,
  });

  const statistics = useQuery({
    queryKey: ["statistics"],
    queryFn: async (): Promise<StatisticsResult[]> => {
      try {
        const stats = await readBlockyStatistics(client);
        const snapshot = createStatisticsSnapshot(stats);

        return [
          {
            serverId: "default",
            success: true,
            data: {
              ...snapshot,
              summary: stats.summary,
              answered: Object.values(stats.byResponseType).reduce(
                (total, count) => total + count,
                0,
              ),
            },
          },
        ];
      } catch {
        return [{ serverId: "default", success: false }];
      }
    },
    refetchInterval: 30_000,
  });

  const logsProbe = useQuery({
    queryKey: ["logsAvailable"],
    queryFn: async () => {
      try {
        await client.get("querylog/count").json();

        return true;
      } catch {
        return false;
      }
    },
  });

  const servers = useMemo(
    () => [{ ...DEFAULT_SERVER, hasLogs: logsProbe.data ?? false }],
    [logsProbe.data],
  );

  const reportDiagnostics = useCallback(noopDiagnostics, []);

  const failed = statistics.data?.[0]?.success === false;
  const hasDiagnostics = failed || Boolean(statistics.error);

  return (
    <ServerContext
      value={{
        servers,
        selection: singleSelection,
        statuses: status.data,
        loading: status.isLoading,
        statusUpdatedAt: status.dataUpdatedAt,
        statistics: statistics.data,
        statisticsLoading: statistics.isLoading,
        reportDiagnostics,
      }}
    >
      {hasDiagnostics ? (
        <div className="mb-6 space-y-3">
          <div
            role="status"
            className="space-y-1 rounded-lg border bg-card px-4 py-3 text-sm"
          >
            {failed || statistics.error ? (
              <p className="text-amber-400">
                Unable to load server statistics.
              </p>
            ) : null}
            <p className="text-muted-foreground text-xs">
              Showing available data. Stored logs do not require a live server
              connection.
            </p>
          </div>
        </div>
      ) : null}
      <Dashboard
        showLogs={servers.some((server) => server.hasLogs)}
        showServerColumn={false}
      />
    </ServerContext>
  );
}
