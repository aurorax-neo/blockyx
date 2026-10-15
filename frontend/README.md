# frontend

blockyx 的 Web UI。`next.config.js` 使用 `output: "export"`。`bun run build` 产出 `out/`，由根目录 `Dockerfile` 拷进 `backend/web/ui-out`，再经 `go:embed` 打进同一个二进制。浏览器打开 `http://<host>:4000`，用 API key 登录后直调同源 `/api`。

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

页面只有登录和仪表盘：统计、拦截开关、名单与缓存维护、查询工具、查询日志。日志和长周期图表走 `/api/querylog/*`；没有可检索日志时，图表退回 `/api/stats` 的 24 小时内存统计。
