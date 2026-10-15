# Installation

Install the blockyx image from the repository root `README.md`. It listens for DNS on 53 and serves the API and web UI on 4000.

The configuration file is the same shape as [blocky](https://github.com/0xERR0R/blocky). Image names and download links in the examples below belong to that project; use the root `Dockerfile` for blockyx.

You can choose one of the following installation options:

* Run as standalone binary
* Run as docker container

## Prepare your configuration

Blocky supports single or multiple YAML files as configuration. Create new `config.yml` with your configuration
(see [Configuration](configuration.md) for more details and all configuration options).

Simple configuration file, which enables only basic features:

```yaml
upstreams:
  groups:
    default: # (1)!
      - 46.182.19.48
      - 80.241.218.68
      - tcp-tls:fdns1.dismail.de:853
      - https://dns.digitale-gesellschaft.ch/dns-query
blocking:
  denylists:
    ads: # (2)!
      - https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts
  clientGroupsBlock:
    default:
      - ads
web:
  port: 4000
  tls: false
dns:
  port: 53 # (3)!
```

1.  Upstream DNS resolvers blocky forwards allowed queries to. Plain IPs as well as `tcp-tls:` (DoT) and `https://` (DoH) endpoints are supported.
2.  A named denylist group. Each entry is a URL or local file with hosts/domains to block; reference it from `clientGroupsBlock`.
3.  Listeners — `web` serves the Web UI and API, `dns` serves DNS queries (UDP/TCP).

## Run blockyx

=== "Docker"

    blockyx 官方多架构 Docker 镜像（支持 `linux/amd64` 与 `linux/arm64`）发布于 GitHub Container Registry：`ghcr.io/aurorax-neo/blockyx`。

    镜像内已集成完整的 Web 管理界面、API 服务以及 DNS 引擎，不需要配置反向代理或额外的前端容器。

    !!! note "权限与能力机制"

        容器内的 blockyx 以非 root 用户 `blockyx` (UID 1001, GID 1001) 运行。二进制文件内置 `cap_net_bind_service=+p`：
        
        - 运行时拥有 `NET_BIND_SERVICE` 时，会在启动时自动提升生效，支持免 root 监听特权端口（如 53、853、443）。
        - 若在丢弃了所有 capabilities 的受限环境（如 Kubernetes Restricted Pod Security Standard）中运行，可将端口配置为 `>= 1024`，或者显式添加 `--cap-add NET_BIND_SERVICE`。
        - 容器启动入口 `entrypoint.sh` 在检测到以 root 启动时，会自动修正挂载卷 `/etc/blockyx`、`/logs`、`/data` 的属主为 `1001:1001`。

    === "docker run"

        ```sh
        # 准备数据目录并下载配置文件示例
        curl -o config.yml https://raw.githubusercontent.com/aurorax-neo/blockyx/main/config.yml.example

        docker run -d \
          --name blockyx \
          --restart unless-stopped \
          -p 53:53/udp \
          -p 53:53/tcp \
          -p 4000:4000/tcp \
          -v $(pwd)/config.yml:/etc/blockyx/config.yml \
          -v blockyx-data:/data \
          -e BLOCKYX_API_KEY="your-secret-key" \
          ghcr.io/aurorax-neo/blockyx:latest
        ```

    === "docker compose"

        创建 `docker-compose.yml` 文件：

        ```yaml
        services:
          blockyx:
            image: ghcr.io/aurorax-neo/blockyx:latest
            container_name: blockyx
            restart: unless-stopped
            ports:
              - "53:53/udp"
              - "53:53/tcp"
              - "4000:4000/tcp"  # Web UI & REST API
            environment:
              - TZ=Asia/Shanghai
              - BLOCKYX_API_KEY=your-secret-key
            volumes:
              - ./config.yml:/etc/blockyx/config.yml
              - blockyx-data:/data

        volumes:
          blockyx-data:
        ```

        启动容器：

        ```sh
        docker compose up -d
        ```

=== "独立预编译二进制 (Standalone Binary)"

    从 [GitHub Releases](https://github.com/aurorax-neo/blockyx/releases) 下载适合您系统架构的预编译归档（支持 Linux、macOS、Windows），解压后运行：

    ```sh
    ./blockyx --config config.yml
    ```

    !!! warning "Linux 绑定 53/853 特权端口"

        在 Linux 上以非 root 用户监听 1024 以下特权端口时，请授予二进制网络绑定权限：
        ```sh
        sudo setcap 'cap_net_bind_service=+ep' ./blockyx
        ```
        在受限 capability 边界环境（如系统 systemd 单元）中，可使用 `setcap 'cap_net_bind_service=+p' ./blockyx`，blockyx 启动时会自动提权为 effective。

## 容器配置文件路径

在 Docker 镜像中，默认配置文件路径为 `/etc/blockyx/config.yml`。可以通过环境变量 `BLOCKYX_CONFIG` 自定义：

```sh
-e BLOCKYX_CONFIG=/etc/blockyx/my-config.yml
```

（同时也向下兼容旧版 `BLOCKY_CONFIG_FILE` / `CONFIG_FILE` 变量）。

## 高级挂载与数据存储

blockyx 默认将查询日志（`queryLog`）和 24 小时持久化统计（`statistics`）存放于 `/data` 目录。使用 SQLite 时，WAL 机制会旁路生成 `-wal` 和 `-shm` 文件，因此**推荐挂载整个 `/data` 目录或使用具名 Volume**（如 `-v blockyx-data:/data`），避免在 NFS/SMB 挂载上运行 SQLite。

如需使用独立的名单目录，也可以直接将宿主机名单挂载进容器并在配置文件中引用：

```yaml
services:
  blockyx:
    image: ghcr.io/aurorax-neo/blockyx:latest
    container_name: blockyx
    restart: unless-stopped
    ports:
      - "53:53/udp"
      - "53:53/tcp"
      - "4000:4000/tcp"
    volumes:
      - ./config.yml:/etc/blockyx/config.yml
      - blockyx-data:/data
      - ./denylists:/app/denylists:ro
      - ./allowlists:/app/allowlists:ro

volumes:
  blockyx-data:
```
## Multiple configuration files

For complex setups, splitting the configuration between multiple YAML files might be desired. In this case, folder containing YAML files is passed on startup, Blocky will join all the files.

```sh
./blocky --config ./config/
```

!!! warning

    Blocky simply joins the multiple YAML files. If an option (e.g. `upstream`) is present in multiple files, the configuration will not load and start will fail.

## Other installation types

!!! warning

    These projects are not associated with Blocky devs and are listed here for convenience.

### Arch Linux via AUR

See [https://aur.archlinux.org/packages/blocky/](https://aur.archlinux.org/packages/blocky/)

### Alpine Linux

See [https://pkgs.alpinelinux.org/packages?name=blocky&branch=edge&repo=&arch=](https://pkgs.alpinelinux.org/packages?name=blocky&branch=edge&repo=&arch=)

### CentOS/Debian/Fedora install script

See [https://github.com/m0zgen/blocky-installer](https://github.com/m0zgen/blocky-installer)

### FreeBSD

See [https://www.freebsd.org/cgi/ports.cgi?query=blocky&stype=all](https://www.freebsd.org/cgi/ports.cgi?query=blocky&stype=all)

### Gentoo

See the [Gentoo Wiki](https://wiki.gentoo.org/wiki/Project:GURU/Information_for_End_Users) to enable the GURU repository, then run `emerge net-dns/blocky`.

### NixOS

Add `pkgs.blocky` as a module:

```nix
services.blocky = {
  enable = true;

  settings = {
    # anything from config.yml
  };
};
```

### macOS via Homebrew

See [https://formulae.brew.sh/formula/blocky](https://formulae.brew.sh/formula/blocky)

### TrueNAS SCALE via TrueCharts

See [https://truecharts.org/charts/stable/blocky/](https://truecharts.org/charts/stable/blocky/)
(TrueCharts is not an official TrueNAS project)

## Companion projects

!!! warning

    These projects are not associated with Blocky devs and are listed here for convenience.

### Lists updater

[Blocky lists updater](https://github.com/shizunge/blocky-lists-updater) updates list related configuration without restarting blocky DNS.

### Web UIs

- [Blocky Frontend](https://github.com/Mozart409/blocky-frontend) provides a Web UI to control blocky.
See linked project for installation instructions.

- The web UI ships inside blockyx. It follows [blocky-ui](https://github.com/GabeDuarteM/blocky-ui).

- [Blocky Visor](https://github.com/JCHHeilmann/blocky-visor) is a static SPA dashboard with live metrics and DNS query testing. Comes with an optional Go sidecar that adds historical analytics based on parsing Blocky's log files, log viewing, and config editing.
