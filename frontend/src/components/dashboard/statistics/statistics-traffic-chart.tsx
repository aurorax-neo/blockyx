"use client";

import { RefreshCw } from "lucide-react";
import { useDashboardServers } from "~/components/dashboard/server-context";
import { TrafficChart } from "~/components/dashboard/statistics/queries-over-time-chart";
import { Button } from "~/components/ui/button";
import { aggregateStatisticsTraffic } from "~/lib/aggregate-statistics";
import { cn } from "~/lib/utils";

export function StatisticsTrafficChart() {
  const dashboard = useDashboardServers();
  const data = aggregateStatisticsTraffic(dashboard.statistics ?? []);
  if (!dashboard.statisticsLoading && data === null) {
    return null;
  }

  return (
    <TrafficChart
      range="24h"
      data={data ?? undefined}
      isLoading={dashboard.statisticsLoading}
      series={["total", "blocked"]}
      controls={
        <Button
          variant="outline"
          size="responsive-icon"
          onClick={() => dashboard.refetchStatistics?.()}
          disabled={dashboard.isFetchingStatistics}
          aria-label="Refresh chart data"
          title="Refresh chart"
        >
          <RefreshCw
            className={cn(
              "h-4 w-4",
              dashboard.isFetchingStatistics && "animate-spin",
            )}
          />
        </Button>
      }
    />
  );
}
