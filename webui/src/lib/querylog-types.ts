/** Query log row as returned by the blocky /api/querylog endpoints. */
export interface LogEntry {
  requestTs: string | null;
  clientIp: string | null;
  clientName: string | null;
  durationMs: number | null;
  reason: string | null;
  questionName: string | null;
  answer: string | null;
  responseCode: string | null;
  responseType: string | null;
  questionType: string | null;
  /** Synthetic source id (single server). */
  sourceId?: string;
  /** Synthetic server id (single server). */
  serverId?: string;
  /** Optional row id (test mocks). */
  id?: number | null;
  /** Effective TLD+1 (test mocks). */
  effectiveTldp?: string | null;
  /** Server hostname (test mocks). */
  hostname?: string | null;
}
