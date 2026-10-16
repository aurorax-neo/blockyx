"use client";

import { Globe, type LucideIcon, Users } from "lucide-react";
import { useCallback, useState } from "react";
import { useLogTopList } from "~/hooks/use-log-top-list";

import type { TimeRange } from "~/lib/constants";
import { PaginatedTopList } from "./paginated-top-list";
import type { TopListFilter } from "./top-list";

type TopListType = "domains" | "clients";

const TOP_LIST_CONFIG: Record<
  TopListType,
  { title: string; description: string; icon: LucideIcon }
> = {
  domains: {
    title: "Top Domains",
    description: "Most frequently queried domains",
    icon: Globe,
  },
  clients: {
    title: "Top Clients",
    description: "Devices with the most DNS queries",
    icon: Users,
  },
};

interface TopListTableProps {
  type: TopListType;
  range: TimeRange;
  limit: number;
  page: number;
  onPageChange: (page: number) => void;
}

export function TopListTable({
  type,
  range,
  limit,
  page,
  onPageChange,
}: TopListTableProps) {
  const [filter, setFilter] = useState<TopListFilter>("all");
  const query = useLogTopList({
    type,
    range,
    limit,
    offset: page * limit,
    filter,
  });
  const config = TOP_LIST_CONFIG[type];

  const handleFilterChange = useCallback(
    (nextFilter: TopListFilter) => {
      setFilter(nextFilter);
      onPageChange(0);
    },
    [onPageChange],
  );

  return (
    <PaginatedTopList
      title={config.title}
      description={config.description}
      icon={config.icon}
      items={query.data?.items ?? []}
      totalCount={query.data?.totalCount ?? 0}
      isLoading={query.isLoading && !query.data}
      isRefreshing={query.isFetching && Boolean(query.data)}
      filter={filter}
      onFilterChange={handleFilterChange}
      page={page}
      limit={limit}
      onPageChange={onPageChange}
    />
  );
}
