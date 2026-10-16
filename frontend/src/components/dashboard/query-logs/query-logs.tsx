"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { History, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { MobileQueryLog } from "~/components/dashboard/query-logs/mobile-query-log";
import {
  identifyQueryLogRows,
  type QueryLogRow,
} from "~/components/dashboard/query-logs/query-log-identity";
import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import { Label } from "~/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "~/components/ui/select";
import { Switch } from "~/components/ui/switch";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "~/components/ui/tooltip";
import { usePrefetchAdjacentPages } from "~/hooks/use-prefetch-adjacent-pages";
import { createApiClient } from "~/lib/blocky-api";
import {
  BLOCKY_DNS_RECORD_TYPES,
  BLOCKY_RESPONSE_TYPES,
  isDnsRecordType,
  isResponseType,
} from "~/lib/constants";
import {
  type QueryLogFilters as ApiFilters,
  fetchLogCount,
  fetchLogRows,
} from "~/lib/querylog-api";
import { columns } from "./columns";
import { DataTable } from "./data-table";
import {
  type QueryLogFilter,
  QueryLogFilterCombobox,
} from "./query-log-filter-combobox";

const serverColumns: typeof columns = [
  { accessorKey: "hostname", header: "Server" },
  ...columns,
];

export function QueryLogs({
  showServerColumn = false,
}: {
  showServerColumn?: boolean;
}) {
  const apiClient = useMemo(() => createApiClient(), []);
  const [pageIndex, setPageIndex] = useState(0);
  const [filter, setFilter] = useState<QueryLogFilter>(null);
  const [responseTypeFilter, setResponseTypeFilter] = useState("ALL");
  const [questionTypeFilter, setQuestionTypeFilter] = useState("ALL");
  const [pageSize, setPageSize] = useState(10);
  const [autoRefresh, setAutoRefresh] = useState(true);

  const handleFilterChange = useCallback(
    (value: QueryLogFilter) => {
      setFilter(value);
      setPageIndex(0);
    },
    [setPageIndex],
  );

  const handleResponseTypeChange = useCallback(
    (value: string) => {
      setResponseTypeFilter(value);
      setPageIndex(0);
    },
    [setPageIndex],
  );

  const handleQuestionTypeChange = useCallback(
    (value: string) => {
      setQuestionTypeFilter(value);
      setPageIndex(0);
    },
    [setPageIndex],
  );

  const search = filter?.type === "domain-search" ? filter.value : undefined;
  const domain = filter?.type === "domain" ? filter.value : undefined;
  const client = filter?.type === "client" ? filter.value : undefined;
  const responseType = isResponseType(responseTypeFilter)
    ? responseTypeFilter
    : undefined;
  const questionType = isDnsRecordType(questionTypeFilter)
    ? questionTypeFilter
    : undefined;
  const apiFilters: ApiFilters = {
    search,
    domain,
    client,
    qtype: questionType,
    rtype: responseType,
  };
  const rows = useQuery({
    queryKey: ["querylog", "rows", apiFilters, pageSize, pageIndex],
    queryFn: () =>
      fetchLogRows(apiClient, apiFilters, pageSize + 1, pageIndex * pageSize),
    refetchOnWindowFocus: autoRefresh,
    refetchInterval: autoRefresh ? 30_000 : false,
  });
  const count = useQuery({
    queryKey: ["querylog", "count", apiFilters],
    queryFn: () => fetchLogCount(apiClient, apiFilters),
    refetchOnWindowFocus: autoRefresh,
    refetchInterval: autoRefresh ? 30_000 : false,
  });
  const queryLogsData = rows.data && {
    items: identifyQueryLogRows(
      rows.data.items
        .slice(0, pageSize)
        .map((item) => ({ ...item, sourceId: "default" })),
    ).map((item) => ({ ...item, hostname: "Blocky" })),
    totalCount: count.data?.total,
  };
  const {
    isFetching: isFetchingLogs,
    isLoading: isLoadingLogs,
    isPlaceholderData: isPlaceholderLogs,
    error,
  } = rows;
  const refetch = useCallback(
    () => Promise.all([rows.refetch(), count.refetch()]),
    [count, rows],
  );
  const hasNextPage = (rows.data?.items.length ?? 0) > pageSize;

  const handleAutoRefreshChange = useCallback(
    (enabled: boolean) => {
      setAutoRefresh(enabled);
      if (enabled) {
        refetch();
      }
    },
    [refetch],
  );

  useEffect(() => {
    if (error) {
      toast.error("Failed to fetch query logs", {
        description: error.message,
        id: "query-logs-error",
      });
    } else {
      toast.dismiss("query-logs-error");
    }
  }, [error]);

  const pageCount =
    queryLogsData?.totalCount === undefined
      ? undefined
      : Math.ceil(queryLogsData.totalCount / pageSize);
  const showLogsLoading = isLoadingLogs || isPlaceholderLogs;
  const queryClient = useQueryClient();

  usePrefetchAdjacentPages({
    enabled: !isFetchingLogs && queryLogsData !== undefined,
    currentPage: pageIndex,
    totalPages: pageCount ?? 0,
    prefetchPage: (targetPage) => {
      queryClient.prefetchQuery({
        queryKey: ["querylog", "rows", apiFilters, pageSize, targetPage],
        queryFn: () =>
          fetchLogRows(
            apiClient,
            apiFilters,
            pageSize + 1,
            targetPage * pageSize,
          ),
      });
    },
  });

  const getRowId = useCallback((entry: QueryLogRow) => entry.rowId, []);
  const renderMobileRow = useCallback(
    (entry: QueryLogRow) => (
      <MobileQueryLog entry={entry} showServer={showServerColumn} />
    ),
    [showServerColumn],
  );
  const isRefreshing = isFetchingLogs || count.isFetching;
  const refreshLogs = useCallback(async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["querylog", "rows"] }),
      queryClient.invalidateQueries({ queryKey: ["querylog", "count"] }),
      rows.refetch(),
      count.refetch(),
    ]);
  }, [queryClient, rows, count]);
  const table = (
    <DataTable
      columns={showServerColumn ? serverColumns : columns}
      data={queryLogsData?.items ?? []}
      pageCount={pageCount}
      hasNextPage={hasNextPage}
      pageIndex={pageIndex}
      onPageChange={setPageIndex}
      pageSize={pageSize}
      onPageSizeChange={setPageSize}
      isLoading={showLogsLoading}
      getRowId={getRowId}
      renderMobileRow={renderMobileRow}
    />
  );

  return (
    <Card role="region" aria-label="Query Logs">
      <CardHeader>
        <div className="flex w-full flex-row flex-wrap items-center justify-between gap-y-3">
          <div className="flex min-w-40 flex-1 flex-col gap-1">
            <CardTitle className="flex items-center gap-2">
              <History className="h-5 w-5" />
              Query Logs
            </CardTitle>
            <CardDescription>
              View the DNS query logs processed by the server
            </CardDescription>
          </div>
          <div className="flex h-full items-center justify-center gap-4 pl-4">
            <div className="flex items-center gap-2">
              <Label
                htmlFor="auto-refresh"
                className="text-muted-foreground text-sm"
              >
                Auto
              </Label>
              <Tooltip disableHoverableContent>
                <TooltipTrigger asChild>
                  <span className="inline-flex">
                    <Switch
                      id="auto-refresh"
                      checked={autoRefresh}
                      onCheckedChange={handleAutoRefreshChange}
                    />
                  </span>
                </TooltipTrigger>
                <TooltipContent sideOffset={4}>
                  Auto-refresh (every 30s)
                </TooltipContent>
              </Tooltip>
            </div>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="responsive-icon"
                  onClick={refreshLogs}
                  disabled={isRefreshing}
                  aria-label="Refresh"
                >
                  <RefreshCw className={isRefreshing ? "animate-spin" : ""} />
                </Button>
              </TooltipTrigger>
              <TooltipContent sideOffset={4}>Refresh</TooltipContent>
            </Tooltip>
          </div>
        </div>
        <div className="mt-4 flex flex-col gap-2 sm:flex-row sm:items-center">
          <QueryLogFilterCombobox
            value={filter}
            onChange={handleFilterChange}
          />
          <div className="grid grid-cols-2 gap-2 sm:flex sm:items-center">
            <Select
              value={responseTypeFilter}
              onValueChange={handleResponseTypeChange}
            >
              <SelectTrigger
                size="responsive"
                aria-label="Filter by reason"
                className="w-full sm:w-36"
              >
                <SelectValue placeholder="Response" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="ALL">All Reasons</SelectItem>
                {BLOCKY_RESPONSE_TYPES.map((type) => (
                  <SelectItem key={type} value={type}>
                    {type}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select
              value={questionTypeFilter}
              onValueChange={handleQuestionTypeChange}
            >
              <SelectTrigger
                size="responsive"
                aria-label="Filter by record type"
                className="w-full sm:w-28"
              >
                <SelectValue placeholder="Type" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="ALL">All Types</SelectItem>
                {BLOCKY_DNS_RECORD_TYPES.map((type) => (
                  <SelectItem key={type} value={type}>
                    {type}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>
      </CardHeader>
      <CardContent>{table}</CardContent>
    </Card>
  );
}
