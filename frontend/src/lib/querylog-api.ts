import type { KyInstance } from "ky";

export interface QueryLogEntry {
  requestTs: string;
  clientIp: string;
  clientName: string;
  durationMs: number;
  reason: string;
  questionName: string;
  answer: string;
  responseCode: string;
  responseType: string;
  questionType: string;
}

export interface QueryLogFilters {
  from?: string;
  to?: string;
  search?: string;
  domain?: string;
  client?: string;
  qtype?: string;
  rtype?: string;
}

function params(
  filters: QueryLogFilters,
  extra: Record<string, string | number> = {},
) {
  const search = new URLSearchParams();

  for (const [key, value] of Object.entries({ ...filters, ...extra })) {
    if (value !== undefined && value !== "") {
      search.set(key, String(value));
    }
  }

  return search.toString();
}

export async function fetchLogRows(
  client: KyInstance,
  filters: QueryLogFilters,
  limit: number,
  offset: number,
): Promise<{ items: QueryLogEntry[]; hasMore: boolean }> {
  return client
    .get(`querylog/rows?${params(filters, { limit, offset })}`, {
      cache: "no-store",
    })
    .json();
}

export async function fetchLogCount(
  client: KyInstance,
  filters: QueryLogFilters,
): Promise<{ total: number }> {
  return client.get(`querylog/count?${params(filters)}`, { cache: "no-store" }).json();
}

export interface OverTimeBucket {
  time: string;
  total: number;
  blocked: number;
  cached: number;
}

export async function fetchLogOverTime(
  client: KyInstance,
  filters: QueryLogFilters,
  interval: string,
): Promise<{ items: OverTimeBucket[] }> {
  return client
    .get(`querylog/over-time?${params(filters, { interval })}`, {
      cache: "no-store",
    })
    .json();
}

export interface TopEntry {
  name: string;
  total: number;
  blocked: number;
}

export async function fetchLogTop(
  client: KyInstance,
  filters: QueryLogFilters,
  type: "domains" | "clients",
  filter: "all" | "blocked",
  limit: number,
  offset = 0,
): Promise<{ items: TopEntry[]; totalCount: number }> {
  return client
    .get(`querylog/top?${params(filters, { type, filter, limit, offset })}`, {
      cache: "no-store",
    })
    .json();
}

export async function searchLogNames(
  client: KyInstance,
  filters: QueryLogFilters,
  type: "domains" | "clients",
  q: string,
): Promise<{ items: { name: string; count: number }[] }> {
  return client
    .get(`querylog/search?${params(filters, { type, q })}`, {
      cache: "no-store",
    })
    .json();
}
