# blockyx

blockyx 是本地网络使用的 DNS 代理和广告拦截，带 Web 管理界面。一个二进制同时提供 DNS、HTTP API 和界面。

运行时只有 `./blockyx`：

- DNS：`53/udp` 与 `53/tcp`
- HTTP API、DoH、Prometheus、Web UI：默认同一端口 `4000`

Web UI 是 Next.js 静态导出，构建时用 `go:embed` 打进二进制。浏览器直调同源 `/api/*`，不需要 Node，也不需要反代。

功能说明见 `backend/docs/`。部署和下面这些默认行为以本文和 `config.yml.example` 为准。

## 默认行为

1. **`/api/*` 始终要 API key。** 请求带 `X-API-Key` 或 `Authorization: Bearer <key>`。比较是常量时间。`BLOCKYX_API_KEY` 优先于配置文件里的 `api.apiKey`。两者都空时，进程生成随机 key。能更新单个配置文件时，只改 `api.apiKey` 那一行，其余内容和注释保持原样，并把 key 打印到 stderr。配置路径是目录、只读或写不回去时，不改文件，并说明这个 key 只在本次进程有效。
 2. **全系统单配置与 UI 自动联动。** blockyx 彻底消除了独立 UI 配置文件。DNS 服务与 Web UI 完全共用同一份 `config.yml`。UI 上的查询日志、趋势图和 Top 列表直接调用 `/api/querylog/*`，并自动根据 `queryLog` 配置读取存储。支持 UI 检索的类型包括 `sqlite`、`mysql`、`postgresql`、`timescale`、`csv` 和 `csv-client`。`console`、`dnstap`、`none` 运行时，UI 自动退回使用内存 24 小时统计。
 3. **查询日志默认 SQLite。** 未写 `queryLog.type` 时使用 `/data/querylog.db`。显式 `type: none` 关闭日志；显式 `type: console` 写到标准输出。所选写入器创建失败时阻止进程启动，不静默回退。
 4. **CSV 按大小轮转。** `queryLog.rotation` 是字节大小，默认 `100MB`，文件名形如 `2026-10-14_ALL_001.log`。
 5. **日志保留用时长。** 字段是 `queryLog.retention`（例如 `7d`, `30d`）。`0` 表示不清理。
 6. **统计默认打开，可选落盘。** `statistics.enable` 默认 `true`。`statistics.persistence` 默认关；打开后把小时桶写入 SQLite，重启后仪表盘的 24 小时统计不归零。
 7. **Web UI 用同一个 API key 登录。** key 存在浏览器 `localStorage`（键名 `blockyx_api_key`），没有服务端会话。

## 快速开始

```bash
cp config.yml.example config.yml
# 编辑 DNS 上游、拦截名单，并设置 api.apiKey

docker build -t blockyx .
docker run -d --name blockyx --restart unless-stopped \
  -p 53:53/udp -p 53:53/tcp \
  -p 4000:4000 \
  -v ./config.yml:/etc/blockyx/config.yml \
  -v blockyx-data:/data \
  -e BLOCKYX_API_KEY="your-secret-key" \
  blockyx
```

- 打开 `http://<host>:4000`，输入同一个 API key。
- `/data` 存放查询日志库和统计库。SQLite WAL 会旁路产生 `-wal` 和 `-shm`，所以要挂目录，不要只挂单个 `.db` 文件，也不要放在 NFS 或 SMB 上。
- 容器以 UID/GID `1001` 的用户 `blockyx` 运行。entrypoint 在以 root 启动时会 `chown` `/etc/blockyx`、`/logs`、`/data`。绑定挂载进来的配置文件，所有权会变成 `1001`。
- 镜像里的二进制带 `cap_net_bind_service=+p`。进程启动时，如果运行时仍允许这个能力，会自己提升为 effective，从而以非 root 绑定 53。运行时丢掉全部能力时，改用大于等于 1024 的 DNS 端口，或加回 `NET_BIND_SERVICE`。
- 镜像有 `HEALTHCHECK`，执行 `./blockyx --config "$BLOCKYX_CONFIG" healthcheck`。它按镜像使用的配置路径读取 DNS 端口，再查询 `healthcheck.blocky.`，不检查 HTTP。
- push 和 pull request 会跑 `.github/workflows/ci.yml` 里的 Go 测试。打 `v*` tag 后，`.github/workflows/release.yml` 构建并推送 `ghcr.io/<owner>/blockyx`。

配置文件建议可写，并且事先写好 `api.apiKey`。未设置 key 且配置是目录或只读挂载时，生成的 key 无法落盘，重启后会换成新 key。只读挂载必须自己提供 `api.apiKey` 或 `BLOCKYX_API_KEY`。

非容器运行时，默认会去创建 `/data/querylog.db`。父目录建不出来，或所选写入器创建失败时，进程会重试后退出，不会改写到控制台继续运行。请把 `queryLog.target` 指到可写文件，或显式设置 `type: none` / `type: console`。

## 配置

 完整全量配置详解与真实模板见仓库根目录 **`config.yml.example`**（包含全部 24 个模块的详尽中文注释）与 `backend/docs/configuration.md`。
 
 ### 核心模块速查表
 
 | 模块配置项 | 作用说明 | 典型用途与默认行为 |
 | --- | --- | --- |
 | **`upstreams`** | 上游解析器与策略 | 支持 UDP/TCP、DoT、DoH、DoQ，默认 `parallel_best` 并发最快 |
 | **`ports`** | 监听端口与功能隔离 | `dns: 53`，`http: 4000` (管理)，`https: 443` (DoH)，`isolate: true` |
 | **`api`** | API Key 鉴权管理 | 强制校验，空时自动生成并单行回写 |
 | **`caching`** | 响应缓存加速 | 支持 `prefetching` 自动预热、最小/最大 TTL 与否定缓存 |
 | **`blocking`** | 广告与恶意名单拦截 | 支持远程 URL/本地文件黑白名单、定时调度、`zeroIP` / `nxDomain` 阻断 |
 | **`clientLookup`**| 客户端设备识别 | 通过路由器 rDNS PTR 反查或静态 IP 映射识别客户端名称并在 UI 展示 |
 | **`customDNS`** | 自定义静态域名解析 | 局域网主机名、内网服务 A/AAAA 映射或标准 Zone 文件 |
 | **`conditional`**| 条件分流解析 | 将特定后缀（如 `*.lan`, `*.corp`）定向转发给内部 DNS |
 | **`statistics`** | 仪表盘统计与持久化 | `persistence: true` 写入 SQLite，重启后 24h 统计图表不归零 |
 | **`queryLog`** | 查询日志多存储后端 | SQLite、MySQL、PostgreSQL、Timescale、CSV 等，UI 自动复用 |
 | **`dnssec`** | DNSSEC 签名安全校验 | 验证上游响应密码学签名，防御 DNS 缓存投毒与劫持 |
 | **`rebindingProtection`** | 域名重绑定防护 | 阻断公网域名解析返回内网私有 IP，防御 Rebinding 渗透 |
 | **`rateLimit`** | 客户端 QPS 速率限制 | 针对单客户端限制请求频次，防御反射放大攻击与异常脚本 |
 | **`filtering`** | 查询类型过滤 | 例如过滤 `AAAA` 阻断 IPv6 解析 |
 | **`ede`** | 扩展 DNS 错误响应 | RFC 8914 标准错误码反馈 |
 | **`ecs`** | EDNS Client Subnet | 传递客户端子网以获得最近的 CDN 节点加速 |
 | **`redis`** | 分布式集群同步 | 多节点 blockyx 共享缓存与拦截状态 |
 | **`prometheus`**| 监控指标暴露 | 兼容 Prometheus 格式供 Grafana 采集 |
 | **`log`** | 运行日志 | 控制控制台输出级别、格式与隐私脱敏 |
 
 常用核心配置片段：
```yaml
statistics:
  enable: true                 # 默认已是 true；false 时 /api/stats 返回 503
  persistence:
    enable: true
    path: /data/blockyx-stats.db
    retentionDays: 30
    flushInterval: 5m

queryLog:
  type: sqlite                 # 省略时也是 sqlite；none 关闭
  target: /data/querylog.db   # sqlite 的文件路径。不要加 file: 前缀
  retention: 7d                # 0 表示不删除旧记录
  flushInterval: 30s

api:
  apiKey: "change-me"         # 或使用环境变量 BLOCKYX_API_KEY
```

 ### 查询日志存储类型与配置
 
 只要在 `config.yml` 的 `queryLog` 配置好，UI 将自动获取日志并提供过滤检索，**不需要单独为 UI 配置数据源**。
 
 | 类型 | 适用场景 | Web UI 支持 | 关键配置项 |
 | --- | --- | :---: | --- |
 | `sqlite` | 单机/单容器（默认推荐） | **支持** | `target: /data/querylog.db` |
 | `mysql` | 外置 MySQL / MariaDB | **支持** | `target: "user:pass@tcp(host:3306)/db?charset=utf8mb4&parseTime=True"` |
 | `postgresql` | 企业级 PostgreSQL | **支持** | `target: "postgres://user:pass@host:5432/db?sslmode=disable"` |
 | `timescale` | 海量时序数据 (TimescaleDB) | **支持** | 自动创建 Hypertable 与原生 Chunk Retention 策略 |
 | `csv` | 本地文件，按大小轮转 | **支持** | `target: /logs`, `rotation: 100MB` (自动生成 `_001.log`) |
 | `csv-client` | 按客户端分文件的轮转 CSV | **支持** | `target: /logs`, `rotation: 50MB` |
 | `console` | 容器标准输出（stdout） | 仅概览 | 不存盘，UI 仅显示 24h 统计 |
 | `dnstap` | 外部 SIEM / 流式监听 | 仅概览 | `target: unix:/var/run/dnstap.sock` 或 `tcp://...` |
 | `none` | 关闭查询日志 | 仅概览 | 零开销 |
 
 各类型配置示例：
 
 ```yaml
 # 1. SQLite（默认推荐）
 queryLog:
   type: sqlite
   target: /data/querylog.db
   retention: 7d
 
 # 2. MySQL / MariaDB (注意 parseTime=True 必需)
 # queryLog:
 #   type: mysql
 #   target: "blockyx:password@tcp(192.168.1.10:3306)/blockyx_logs?charset=utf8mb4&parseTime=True&loc=Local&timeout=15s"
 #   retention: 30d
 
 # 3. PostgreSQL
 # queryLog:
 #   type: postgresql
 #   target: "postgres://blockyx:password@192.168.1.10:5432/blockyx_logs?sslmode=disable"
 #   retention: 30d
 
 # 4. TimescaleDB (自动转为 Hypertable，并配置自动 Chunk Drop)
 # queryLog:
 #   type: timescale
 #   target: "postgres://blockyx:password@timescale.lan:5432/blockyx_logs?sslmode=disable"
 #   retention: 90d
 
 # 5. CSV (按大小自动轮转并清理过期记录)
 # queryLog:
 #   type: csv
 #   target: /logs
 #   rotation: 100MB
 #   retention: 7d
 ```
 
 ### 加密 DNS 服务 (DoT / DoH / QUIC) 与上游
 
 blockyx 同时支持**对外提供加密 DNS 监听**以及**向上游发起加密 DNS 解析**：
 
 #### 1. 对外提供加密 DNS 服务（服务端 Inbound）
 
 blockyx 支持**端口功能严格隔离模式**（推荐安全部署）：将管理端（Web UI / REST API / 监控）与公共端（DoH 查询）彻底隔离开，避免在对外提供 DoH 解析时泄露管理界面。
 
 | 协议 | 端口角色 | 监听配置项 | 端口特点与安全边界 | 客户端使用方式 |
 | --- | --- | --- | --- | --- |
 | **Web UI & API** | **专用管理端口** | `ports.http: 4000` | 仅提供 **Web UI**、**REST API**、**监控**，不处理 DoH | 局域网管理员访问后台、监控采集 |
 | **DoH** (DNS-over-HTTPS) | **专用公共端口** | `ports.https: 443` | 仅响应 **/dns-query**，访问 UI 或 API 返回 404 | 浏览器、手机客户端，可放心映射公网 |
 | **DoH3 / QUIC** (HTTP/3) | **专用公共端口** | `http3.enable: true` | 在 `ports.https` 相同的 UDP 端口启用 HTTP/3 | 现代支持 QUIC 的 DoH 客户端，抗弱网丢包 |
 | **DoT** (DNS-over-TLS) | **专用公共端口** | `ports.tls: 853` | 独立原生 TCP-TLS 端口，纯 DNS-over-TLS 解析 | Android 原生“私人 DNS”、`kdig -d @host +tls` |
 
 配置示例（推荐的严格隔离模式）：
 
 ```yaml
 ports:
   dns: 53                  # 普通 DNS (UDP/TCP)
   http: 4000               # 专用管理端口 (Web UI + REST API + 监控)，建议仅内网访问
   https: 443               # 专用 DoH 端口 (仅响应 /dns-query，可映射公网)
   dohPath: /dns-query
   isolate: true            # 开启严格功能隔离：443 绝不暴露 Web UI/API；4000 专用于后台
 
   # 可选：开启专用 DoT 端口供 Android“私人DNS”直连
   # tls: 853
 
 # TLS 证书配置 (开启 ports.https 或 ports.tls 时使用)
 # certFile: /etc/blockyx/cert.pem
 # keyFile: /etc/blockyx/key.pem
 
 # 可选：在 ports.https 的 UDP 端口开启基于 QUIC 的 HTTP/3 DoH3 服务
 # http3:
 #   enable: true
 ```
 #### 2. 作为客户端请求上游解析（上游 Outbound）
 
 在 `upstreams.groups` 中支持以下格式：
 
 ```yaml
 upstreams:
   groups:
     default:
       - 1.1.1.1                               # 普通 UDP/TCP
       - tcp-tls:1.1.1.1:853                   # DoT (DNS-over-TLS)
       - https://dns.google/dns-query          # DoH (DNS-over-HTTPS)
       - quic:dns.adguard.com                  # DoQ (DNS-over-QUIC, RFC 9250)
   quic:
     maxIdleTimeout: 30s
     keepAlivePeriod: 15s
 ```


统计持久化只恢复最近 24 小时的小时桶，供 `/api/stats` 和仪表盘卡片使用。7 天、30 天曲线和可筛选的 Top 列表来自查询日志，不是这张统计表。`kill -9` 最多丢掉一个 `flushInterval` 内尚未刷盘的当前小时桶。

## HTTP 接口

需要 API key：

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| GET | `/api/blocking/status` | 拦截开关状态。UI 用它校验 key |
| GET | `/api/blocking/enable` | 打开拦截 |
| GET | `/api/blocking/disable` | 关闭拦截，可选 `duration`、`groups` |
| POST | `/api/lists/refresh` | 刷新名单 |
| POST | `/api/cache/flush` | 清空 DNS 缓存 |
| POST | `/api/query` | 经 blocky 做一次 DNS 查询 |
| GET | `/api/stats` | 滚动 24 小时统计。统计关闭时 503 |
| GET | `/api/querylog/status` | 日志类型，以及 UI 能否查询 |
| GET | `/api/querylog/rows` | 分页日志，新的在前 |
| GET | `/api/querylog/count` | 过滤后的条数 |
| GET | `/api/querylog/over-time` | 时间桶，查询参数 `interval`，例如 `1h` |
| GET | `/api/querylog/top` | Top 域名或客户端 |
| GET | `/api/querylog/search` | 过滤建议 |
| GET | `/api/querylog/query-types` | 查询类型分布 |

```bash
curl -H "X-API-Key: change-me" http://127.0.0.1:4000/api/blocking/status
```

不校验 API key 的路径：`/metrics`（需配置打开 Prometheus）、DNS、DoH `/dns-query`、`/docs/openapi.yaml`、`/docs/config.schema.json`，以及 Web UI 静态文件。`/debug/pprof` 和 `/api/*` 使用同一个 key。不要把 `4000` 暴露到不受信任的网络。

CLI 子命令 `blocking`、`lists`、`cache`、`query`、`stats` 走本机 HTTP API，并带上配置文件里的 `api.apiKey` 或 `BLOCKYX_API_KEY`。`healthcheck` 只查 DNS，`validate` 只检查配置文件。

## 从源码构建

需要 Go 1.26.2 和 bun 1.4.2。只执行 `go build` 时，`backend/web/ui-out` 里没有前端产物，页面是空的。

```bash
cd frontend && bun install --frozen-lockfile && bun run build
rm -rf ../backend/web/ui-out && cp -R out ../backend/web/ui-out
cd ../backend && go build -o ../blockyx .
```

常用检查：

```bash
cd backend && go test ./api/... ./querylog/... ./config/... ./stats/... ./cmd/...
cd frontend && bun run typecheck && bun x vitest run
```

`backend/Makefile` 生成的二进制名是 `blocky`。发布镜像用仓库根目录的 `Dockerfile`。

## 仓库结构

```
blockyx/
├── backend/          # Go：DNS 服务、鉴权、日志 API、统计持久化
│   ├── cmd/          # cobra：默认 serve，另有 query、lists、blocking、cache、stats、healthcheck、validate
│   ├── server/       # DNS 与 HTTP；解析链在 createQueryResolver
│   ├── resolver/     # 拦截、缓存、上游、DNSSEC 等
│   ├── querylog/     # CSV、数据库、dnstap、console 的写入与查询
│   ├── stats/        # 内存统计与 SQLite 小时桶
│   ├── api/          # OpenAPI 生成代码与 API key 中间件
│   ├── web/          # go:embed UI 与 RapiDoc 静态资源
│   └── docs/         # 功能文档（MkDocs）
├── frontend/         # Next.js 静态 UI，output: "export"
├── Dockerfile
├── entrypoint.sh
└── config.yml.example
```

DNS 查询从外到内经过：统计、按源 IP 限流、ECS 客户端身份、客户端名、过滤、FQDN-only、EDE、查询日志、Prometheus、自定义 DNS、hosts、重绑定防护、拦截名单、DNSSEC、缓存、DNS64、ECS、条件上游、特殊用途域名、上游解析树。

本地预览文档站：

```bash
mkdocs serve
```

`mkdocs.yml` 的文档目录是 `backend/docs`。

## 参考

- DNS 服务器：[blocky](https://github.com/0xERR0R/blocky)
- Web 界面：[blocky-ui](https://github.com/GabeDuarteM/blocky-ui)

## 许可证

- `backend/`：Apache-2.0，见 `LICENSE`。
- `frontend/`：见 `frontend/LICENSE`。
