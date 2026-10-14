# blockyx

blockyx = **blocky**（Go 写的 DNS 代理/广告拦截）+ **blocky-ui**（Next.js 管理界面），
合并为**一个仓库、一次构建、一个镜像**。

上游项目：

- DNS 服务端：[0xERR0R/blocky](https://github.com/0xERR0R/blocky)
- Web UI：[GabeDuarteM/blocky-ui](https://github.com/GabeDuarteM/blocky-ui)

## blockyx 相对上游的改动

1. **API key 认证**（Go，`api/api_auth.go`）
   - 配置项 `api.apiKey`；环境变量 `BLOCKYX_API_KEY` 优先级更高。
   - 启用后，所有 `/api/*` 请求必须带 `X-API-Key: <key>` 或 `Authorization: Bearer <key>`，
     否则返回 `401`。key 为空时不校验（保持上游默认行为）。
   - 常量时间比较，防止时序攻击。
2. **查询日志细粒度**（Go，`querylog/file_writer.go`）
   - `queryLog.rotation: daily | hourly | minutely`（默认 `daily`，与上游行为一致）。
   - `hourly` → `2006-01-02_15_ALL.log`，`minutely` → `2006-01-02_15-04_ALL.log`。
   - 文件名仍以 `2006-01-02` 开头，retention 清理逻辑不受影响；
     blocky-ui 的 CSV 读取器无需改动即可识别。
3. **UI 修复**（`webui/src/components/dashboard/statistics/stat-card.tsx`）
   - "Query Outcomes" 卡片：数字变大时红色百分比徽章不再溢出卡片，
     空间不足时自动换行（`flex-wrap`）。

## 快速开始

```bash
# 1. 准备配置
cp config.yml.example config.yml
#    编辑 config.yml：上游 DNS、拦截名单等；建议设置 api.apiKey

# 2. 构建
docker build -t blockyx .

# 3. 运行（DNS 53，Web UI 3000，HTTP API 4000）
docker run -d --name blockyx --restart unless-stopped \
  -p 53:53/udp -p 53:53/tcp \
  -p 3000:3000 -p 4000:4000 \
  -v ./config.yml:/etc/blockyx/config.yml:ro \
  -v blockyx-logs:/logs \
  -e BLOCKYX_API_KEY="your-secret-key" \
  blockyx
```

- Web UI：`http://<host>:3000`
- HTTP API：`http://<host>:4000`（需 apikey，见下）
- 未设置 `BLOCKYX_API_KEY` 且配置文件里也没有 `api.apiKey` 时，
  容器启动会生成一个随机 key 并打印到日志（`docker logs blockyx` 查看）。

发布：打 tag `v*` 推送后，GitHub Actions 自动构建并推送镜像到
`ghcr.io/<owner>/blockyx`（见 `.github/workflows/release.yml`）。

## 配置说明

完整示例见 `config.yml.example`。blockyx 特有的三项：

```yaml
# Web UI 的仪表盘卡片依赖 /api/stats，必须打开
statistics:
  enable: true

queryLog:
  type: csv
  target: /logs
  rotation: daily        # daily | hourly | minutely
  logRetentionDays: 7

api:
  apiKey: ""            # 留空 = 不校验；或用 BLOCKYX_API_KEY 环境变量
```

其余配置与上游 blocky 完全一致（见上游文档）。

### API key 用法

```bash
# X-API-Key header（推荐）
curl -H "X-API-Key: your-secret-key" http://<host>:4000/api/blocking/status

# 或 Authorization: Bearer
curl -H "Authorization: Bearer your-secret-key" http://<host>:4000/api/blocking/status
```

注意：

- 只保护 `/api/*`（含 `/api/stats`、`/api/blocking/*` 等）。
  `/metrics`（prometheus，按配置）和 DNS 端口不受影响。
- Web UI 容器内部通过 `127.0.0.1:4000` 直连，key 由 entrypoint 自动注入，
  无需手动配置。

### 日志细粒度

| rotation   | 文件名示例                |
| ---------- | ------------------------- |
| `daily`    | `2026-10-14_ALL.log`      |
| `hourly`   | `2026-10-14_15_ALL.log`   |
| `minutely` | `2026-10-14_15-04_ALL.log`|

- `csv-client` 类型同样适用（`_client1.log` 后缀保持不变）。
- Web UI 的日志页、统计图表对三种粒度都兼容（按天首部匹配文件）。
- `logRetentionDays` 语义不变（按天首部清理）。

## 从源码构建

```bash
# Go 二进制
go build -o blockyx .

# Web UI（需要 bun）
cd webui && bun install --frozen-lockfile && bun run build
```

单测：

```bash
go test ./api/... ./querylog/... ./config/...
cd webui && bun run typecheck && bun run test src/lib/aggregate-statistics.test.ts
```

## 仓库结构

```
blockyx/
├── main.go  api/  config/  querylog/  resolver/  server/ ...  # Go：blocky（+ blockyx 改动）
├── webui/                                                     # Next.js：blocky-ui（+ 徽章溢出修复）
├── Dockerfile            # 单镜像：Go 二进制 + Web UI，一次打包
├── entrypoint.sh         # 启动 blocky + web UI，注入 API key
├── config.yml.example
└── .github/workflows/release.yml   # tag v* → 构建推送 ghcr.io/<owner>/blockyx
```

## 许可证

- Go 部分：沿用上游 blocky 的 Apache-2.0（见 `LICENSE`）。
- `webui/`：沿用上游 blocky-ui 的许可证（见 `webui/LICENSE`）。
