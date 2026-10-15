# Interfaces

## REST API


??? abstract "OpenAPI specification"

    ```yaml
    --8<-- "api/openapi.yaml"
    ```

If http listener is enabled, blocky provides REST API. You can download the [OpenAPI YAML](api/openapi.yaml) interface specification. 

You can also browse the interactive API documentation (RapiDoc) documentation [online](rapidoc.html).

### Common endpoints

| Method | Path                  | Purpose                                              |
| ------ | --------------------- | ---------------------------------------------------- |
| GET    | `/api/blocking/enable`  | Enable blocking globally.                          |
| GET    | `/api/blocking/disable` | Disable blocking globally (optional `duration`, `groups` query params). |
| GET    | `/api/blocking/status`  | Return current blocking status as JSON.            |
| POST   | `/api/lists/refresh`    | Refresh all allow/denylists.                       |
| POST   | `/api/cache/flush`      | Clear the entire DNS response cache.               |
| POST   | `/api/query`            | Run a DNS query through Blocky and return the result as JSON. |
| GET    | `/api/stats`            | In-memory DNS statistics over a rolling 24h window as JSON. Requires [statistics](configuration.md#statistics) to be enabled; returns `503` otherwise. |

!!! note "API key authentication"

    `/api/*` and `/debug` always require a key when one is configured, including the query-log routes below.
    Send `X-API-Key` or `Authorization: Bearer <key>`. A request without the key gets HTTP 401.
    If both `api.apiKey` and `BLOCKYX_API_KEY` are empty, the process generates a key, tries to save it
    into the single config file without rewriting the rest of the file, and prints it to stderr.
    `/metrics`, `/docs/*`, DoH and the web UI files are not authenticated.

!!! example "Flush the DNS cache"

    ```sh
    curl -X POST http://<blocky-host>:<http-port>/api/cache/flush
    ```

    Returns HTTP `200` on success. Useful after editing `customDNS`
    or `hostsFile` entries that may already be cached.

!!! note "Statistics semantics"

    For `/api/stats`, the `summary` fields are server-computed categories, so callers never
    interpret a raw response type:

    | Field      | Response types                                        |
    | ---------- | ----------------------------------------------------- |
    | `blocked`  | `BLOCKED` + `REBIND`                                  |
    | `filtered` | `FILTERED` + `NOTFQDN`                                |
    | `forwarded`| `RESOLVED` + `CONDITIONAL`                            |
    | `cached`   | `CACHED`                                              |
    | `local`    | `CUSTOMDNS` + `HOSTSFILE` + `SPECIAL` + `SYNTHESIZED` |
    | `errors`   | `BOGUS`, plus queries a resolver failed outright      |

    `blocked` counts only queries blocked to protect the client: denylist hits and
    [DNS rebinding](configuration.md#dns-rebinding-protection) hits. Two other outcomes are
    deliberately kept out of it, so they cannot inflate `blocked` or the `topBlockedDomains`
    list:

    - Query-type filtering (e.g. `AAAA` via `filtering.queryTypes`) and non-FQDN rejections are
      client-requested, not protective, and are counted as `filtered`.
    - A [DNSSEC](configuration.md#dnssec) validation failure (`BOGUS`) is a SERVFAIL — blocky
      could not obtain a trustworthy answer — so it is a resolution error, not a block, and is
      counted as `errors` together with queries a resolver failed outright.

    The `blocked` / `filtered` split also applies to the `perHour` series, which carries both.
    Together with `dropped` (rate-limited), the categories above partition every query, so they
    always add up to `queries`.

    The `lists` and `cache` objects are point-in-time gauges
    (current values, not affected by the 24h window), while `start`/`end` bound the windowed fields
    only. All timestamps (`start`, `end`, `perHour[].hour`) are always returned in UTC (RFC 3339,
    `Z` suffix), regardless of the server's local time zone. Statistics are independent of Prometheus
    and work with plain JSON.

## Query log API

These routes sit under `/api/querylog` and use the same API key. They read a
`sqlite`, `mysql`, `postgresql`, `timescale`, `csv` or `csv-client` log.
`console`, `dnstap` and `none` answer `GET /status` with `"supported": false`
and return HTTP 503 for the other routes.

| Method | Path                         | Purpose                                              |
| ------ | ---------------------------- | ---------------------------------------------------- |
| GET    | `/api/querylog/status`       | Configured log type and whether the UI can search it. |
| GET    | `/api/querylog/rows`         | Log rows, newest first. `limit` and `offset` paginate. |
| GET    | `/api/querylog/count`        | Row count for the active filters.                    |
| GET    | `/api/querylog/over-time`    | Buckets. `interval` is a Go duration, for example `1h`. |
| GET    | `/api/querylog/top`          | Top domains or clients.                              |
| GET    | `/api/querylog/search`       | Filter suggestions.                                  |
| GET    | `/api/querylog/query-types`  | Counts by DNS query type.                            |

Filters shared by these routes include `from`, `to`, `search`, `domain`, `client`, `qtype` and `rtype`.

## CLI

Blocky provides a CLI. `blocking`, `lists`, `cache`, `query` and `stats` call the REST API on this host.

- `./blockyx blocking enable`
- `./blockyx blocking disable`
- `./blockyx blocking disable --duration [duration]`
- `./blockyx blocking disable --groups ads,othergroup`
- `./blockyx blocking status`
- `./blockyx query <domain>`
- `./blockyx query <domain> --type <queryType>`
- `./blockyx lists refresh`
- `./blockyx stats`
- `./blockyx validate [--config /path/to/config.yaml]`
- `./blockyx healthcheck` performs a DNS query for `healthcheck.blocky.` and does not call HTTP

!!! note "CLI control commands send the API key"

    `blocking`, `lists`, `cache`, `query` and `stats` call the local HTTP API and send
    `X-API-Key` from `api.apiKey` or `BLOCKYX_API_KEY` (`BLOCKYX_API_KEY` wins).
    `validate` only reads the config file. `healthcheck` only speaks DNS.
