import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { useDebounce } from "~/hooks/use-debounce";
import { useLogTopList } from "~/hooks/use-log-top-list";
import { createApiClient } from "~/lib/blocky-api";
import type { TimeRange } from "~/lib/constants";
import { searchLogNames } from "~/lib/querylog-api";

export type FilterValue =
  | { type: "domain"; value: string }
  | { type: "client"; value: string }
  | null;

interface DomainSuggestion {
  domain: string;
  count: number;
}

interface ClientSuggestion {
  client: string;
  count: number;
}

export interface FilterSuggestions {
  domains: DomainSuggestion[];
  clients: ClientSuggestion[];
  isLoading: boolean;
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

export function useFilterSuggestions(
  search: string,
  range: TimeRange = "24h",
): FilterSuggestions {
  const client = useMemo(() => createApiClient(), []);
  const debouncedSearch = useDebounce(search, 300);
  const hasSearch = debouncedSearch.length > 0;

  const to = useMemo(() => new Date().toISOString(), [range]);
  const from = useMemo(
    () => new Date(Date.now() - rangeToMs(range)).toISOString(),
    [range, to],
  );

  const { data: topDomains, isLoading: loadingTopDomains } = useLogTopList(
    { type: "domains", range, limit: 5, offset: 0, filter: "all" },
    !hasSearch,
  );

  const { data: topClients, isLoading: loadingTopClients } = useLogTopList(
    { type: "clients", range, limit: 5, offset: 0, filter: "all" },
    !hasSearch,
  );

  const searched = useQuery({
    queryKey: ["querylog", "search", "domains", debouncedSearch, from, to],
    queryFn: () =>
      searchLogNames(client, { from, to }, "domains", debouncedSearch),
    enabled: hasSearch,
  });
  const searchedClient = useQuery({
    queryKey: ["querylog", "search", "clients", debouncedSearch, from, to],
    queryFn: () =>
      searchLogNames(client, { from, to }, "clients", debouncedSearch),
    enabled: hasSearch,
  });

  const domains = hasSearch
    ? (searched.data?.items.map((item) => ({
        domain: item.name,
        count: item.count,
      })) ?? [])
    : (topDomains?.items.map((item) => ({
        domain: item.name,
        count: item.count,
      })) ?? []);
  const clients = hasSearch
    ? (searchedClient.data?.items.map((item) => ({
        client: item.name,
        count: item.count,
      })) ?? [])
    : (topClients?.items.map((item) => ({
        client: item.name,
        count: item.count,
      })) ?? []);

  const isLoading =
    search !== debouncedSearch ||
    (hasSearch
      ? searched.isLoading || searchedClient.isLoading
      : loadingTopDomains || loadingTopClients);

  return { domains, clients, isLoading };
}

export function formatCount(count: number): string {
  if (count >= 1000) {
    return `${(count / 1000).toFixed(1)}K`;
  }
  return count.toString();
}
