# ByteMuse Go

ByteMuse 的 Go 重建版本。单个容器同时提供 Web UI、REST API 与后台调度器，支持 SQLite、PostgreSQL 和 MySQL。

- 镜像地址：`ghcr.io/w2z/byte-muse-go`
- 容器端口：`3750`；数据目录：`/data`；默认时区：`Asia/Shanghai`
- 版本、提交与镜像摘要记录在 `version.json`

## 镜像标签

| 标签 | 说明 |
| --- | --- |
| `latest` | 最新一次成功构建 |
| `0.1.<提交计数>` | 版本号，同一提交恒定 |
| `sha-<短提交>` | 按提交定位 |

## Docker

### 构建镜像

```bash
docker build -f deploy/Dockerfile -t bytemuse-go:local .
```

### SQLite 部署

```bash
docker run -d --name byte-muse \
  --restart always \
  -p 3750:3750 \
  -e TZ=Asia/Shanghai \
  -e ADMIN_USERNAME=admin \
  -e ADMIN_PASSWORD='请替换为管理员密码' \
  -e SESSION_SECRET='请替换为32字节以上随机串' \
  -v /path/to/byte-muse/data:/data \
  ghcr.io/w2z/byte-muse-go:latest
```

### PostgreSQL 部署

```bash
docker run -d --name byte-muse \
  --restart always \
  -p 3750:3750 \
  -e TZ=Asia/Shanghai \
  -e DATABASE_DRIVER=postgres \
  -e DATABASE_DSN='postgres://bytemuse:请替换为数据库密码@请替换为数据库主机:5432/bytemuse?sslmode=disable' \
  -e ADMIN_USERNAME=admin \
  -e ADMIN_PASSWORD='请替换为管理员密码' \
  -e SESSION_SECRET='请替换为32字节以上随机串' \
  -v /path/to/byte-muse/data:/data \
  ghcr.io/w2z/byte-muse-go:latest
```

### MySQL 部署

```bash
docker run -d --name byte-muse \
  --restart always \
  -p 3750:3750 \
  -e TZ=Asia/Shanghai \
  -e DATABASE_DRIVER=mysql \
  -e DATABASE_DSN='bytemuse:请替换为数据库密码@tcp(请替换为数据库主机:3306)/bytemuse?charset=utf8mb4&parseTime=true&loc=Local' \
  -e ADMIN_USERNAME=admin \
  -e ADMIN_PASSWORD='请替换为管理员密码' \
  -e SESSION_SECRET='请替换为32字节以上随机串' \
  -v /path/to/byte-muse/data:/data \
  ghcr.io/w2z/byte-muse-go:latest
```

`/path/to/byte-muse/data` 需要允许容器内 UID 65532 写入；只挂载 `/data`，不要覆盖镜像里的 `/app`。以上命令只启动应用容器，需要抓取增强服务时使用下面的 Compose 配置。

## Docker Compose

`deploy/` 下的三个 Compose 文件都直接写出配置值，不使用环境变量插值：应用服务固定命名 `byte-muse`，并附带 `cloudflarebypass` 抓取增强服务，两者接入同一 `bridge` 网络。代理地址与宿主机路径是占位符，`ADMIN_USERNAME`、`ADMIN_PASSWORD`、`SESSION_SECRET` 以及 PostgreSQL/MySQL 的 `DATABASE_DSN` 也是占位值，部署前在文件中替换：`SESSION_SECRET` 少于 32 字节、DSN 主机无法解析时，服务会直接拒绝启动。真实拓扑与凭据只留在部署机上。

### SQLite 部署

`deploy/compose.sqlite.yaml`：

```yaml
services:
  byte-muse:
    image: ghcr.io/w2z/byte-muse-go:latest
    container_name: byte-muse
    restart: always
    networks:
      - bridge
    ports:
      - "3750:3750"
    environment:
      TZ: Asia/Shanghai
      DATABASE_DRIVER: sqlite
      DATABASE_DSN: file:/data/bytemuse.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)
      # 部署前必须替换下面三项：管理员账号密码，以及 32 字节以上随机会话密钥。
      # 占位值长度不足 32 字节，未替换时服务会直接拒绝启动。
      ADMIN_USERNAME: admin
      ADMIN_PASSWORD: 请替换为管理员密码
      SESSION_SECRET: 请替换SESSION_SECRET
    volumes:
      # 只挂载 /data：镜像的 /app 存放服务二进制与前端产物，挂载覆盖后容器无法启动。
      # 宿主机目录需允许容器内 UID 65532 写入：chown -R 65532:65532 /path/to/byte-muse/data
      - /path/to/byte-muse/data:/data
  # 抓取增强服务：设置页把 BYPASS_ENGINE 选为 cloudflare_bypass_for_scraping、BYPASS_URL 填 http://cloudflarebypass:8000
  cloudflarebypass:
    image: ghcr.io/sarperavci/cloudflarebypassforscraping:latest
    container_name: CloudFlareBypass
    restart: always
    networks:
      - bridge
    ports:
      - "30089:8000"
    environment:
      - HTTP_PROXY=http://<代理主机>:<代理端口>
      - HTTPS_PROXY=http://<代理主机>:<代理端口>
      - DOCKERMODE=true

networks:
  bridge:
    driver: bridge
```

```bash
docker compose -f deploy/compose.sqlite.yaml up -d
```

### PostgreSQL 部署

`deploy/compose.postgres.yaml`：

```yaml
services:
  byte-muse:
    image: ghcr.io/w2z/byte-muse-go:latest
    container_name: byte-muse
    restart: always
    networks:
      - bridge
    ports:
      - "3750:3750"
    environment:
      TZ: Asia/Shanghai
      DATABASE_DRIVER: postgres
      # 部署前必须替换：数据库主机与密码，以及下面的管理员账号密码和会话密钥。
      # 占位主机无法解析、会话密钥不足 32 字节，未替换时服务会直接拒绝启动。
      DATABASE_DSN: postgres://bytemuse:请替换为数据库密码@请替换为数据库主机:5432/bytemuse?sslmode=disable
      ADMIN_USERNAME: admin
      ADMIN_PASSWORD: 请替换为管理员密码
      SESSION_SECRET: 请替换SESSION_SECRET
    volumes:
      # 只挂载 /data：镜像的 /app 存放服务二进制与前端产物，挂载覆盖后容器无法启动。
      # 宿主机目录需允许容器内 UID 65532 写入：chown -R 65532:65532 /path/to/byte-muse/data
      - /path/to/byte-muse/data:/data
  # 抓取增强服务：设置页把 BYPASS_ENGINE 选为 cloudflare_bypass_for_scraping、BYPASS_URL 填 http://cloudflarebypass:8000
  cloudflarebypass:
    image: ghcr.io/sarperavci/cloudflarebypassforscraping:latest
    container_name: CloudFlareBypass
    restart: always
    networks:
      - bridge
    ports:
      - "30089:8000"
    environment:
      - HTTP_PROXY=http://<代理主机>:<代理端口>
      - HTTPS_PROXY=http://<代理主机>:<代理端口>
      - DOCKERMODE=true

networks:
  bridge:
    driver: bridge
```

```bash
docker compose -f deploy/compose.postgres.yaml up -d
```

### MySQL 部署

`deploy/compose.mysql.yaml`：

```yaml
services:
  byte-muse:
    image: ghcr.io/w2z/byte-muse-go:latest
    container_name: byte-muse
    restart: always
    networks:
      - bridge
    ports:
      - "3750:3750"
    environment:
      TZ: Asia/Shanghai
      DATABASE_DRIVER: mysql
      # 部署前必须替换：数据库主机与密码，以及下面的管理员账号密码和会话密钥。
      # 占位主机无法解析、会话密钥不足 32 字节，未替换时服务会直接拒绝启动。
      DATABASE_DSN: bytemuse:请替换为数据库密码@tcp(请替换为数据库主机:3306)/bytemuse?charset=utf8mb4&parseTime=true&loc=Local
      ADMIN_USERNAME: admin
      ADMIN_PASSWORD: 请替换为管理员密码
      SESSION_SECRET: 请替换SESSION_SECRET
    volumes:
      # 只挂载 /data：镜像的 /app 存放服务二进制与前端产物，挂载覆盖后容器无法启动。
      # 宿主机目录需允许容器内 UID 65532 写入：chown -R 65532:65532 /path/to/byte-muse/data
      - /path/to/byte-muse/data:/data
  # 抓取增强服务：设置页把 BYPASS_ENGINE 选为 cloudflare_bypass_for_scraping、BYPASS_URL 填 http://cloudflarebypass:8000
  cloudflarebypass:
    image: ghcr.io/sarperavci/cloudflarebypassforscraping:latest
    container_name: CloudFlareBypass
    restart: always
    networks:
      - bridge
    ports:
      - "30089:8000"
    environment:
      - HTTP_PROXY=http://<代理主机>:<代理端口>
      - HTTPS_PROXY=http://<代理主机>:<代理端口>
      - DOCKERMODE=true

networks:
  bridge:
    driver: bridge
```

```bash
docker compose -f deploy/compose.mysql.yaml up -d
```
