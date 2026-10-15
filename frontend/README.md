# frontend

 blockyx 的 Web UI。`next.config.js` 使用 `output: "export"`。`bun run build` 产出 `out/`，由根目录 `Dockerfile` 拷进 `backend/web/ui-out`，再经 `go:embed` 打进同一个二进制。浏览器打开 `http://<host>:4000`，用 API key 登录后直调同源 `/api`。
 
 UI 与 DNS 服务完全共用主配置文件（根目录 `config.yml`），无需为前端配置独立的日志源或连接信息。
 部署见仓库根目录 `README.md`。界面参考 [blocky-ui](https://github.com/GabeDuarteM/blocky-ui)。

## 本地开发

需要 Node 24.15+ 和 bun 1.4.2。

```bash
bun install --frozen-lockfile
bun run dev
```

开发服务器只提供界面。接口在运行中的 blockyx 上，默认不是这个 Next 进程。静态产物要进二进制时：

```bash
bun run build
rm -rf ../backend/web/ui-out && cp -R out ../backend/web/ui-out
```

检查：

```bash
bun run typecheck
bun x vitest run
```

 页面只有登录和仪表盘：统计、拦截开关、名单与缓存维护、查询工具、查询日志。
 
 所有的日志与长周期图表直接请求同源的 `/api/querylog/*`；后端直接根据根目录 `config.yml` 里的 `queryLog` 配置（支持 SQLite、MySQL、PostgreSQL、Timescale、CSV 等）读取对应存储，UI 全自动适配。没有可检索日志（如使用 console、dnstap、none）时，图表退回 `/api/stats` 的 24 小时内存统计。前端无需也不维护任何独立的配置文件。
