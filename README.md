# ByteMuse Go

本项目是ByteMuse 使用AI进行 Go 的重建版本。单个容器同时提供 Web UI、REST API 与后台调度器，支持 SQLite、PostgreSQL 和 MySQL。

- 镜像地址：`ghcr.io/w2z/byte-muse-go`
- api公开链接: `https://s.apifox.cn/0d0f258c-8165-47ec-a98d-fbb718485c25`
- 容器端口：`3750`；数据目录：`/data`；strm目录: `/strm`；默认时区：`Asia/Shanghai`


## Docker 部署

三种数据库只差 `DATABASE_DRIVER` 与 `DATABASE_DSN` 两行，默认 SQLite：不设置这两个变量时数据库文件为 `/data/bytemuse.db`。PostgreSQL 与 MySQL 把注释中对应两行（含行尾 `\`）插入到 `-e TZ=Asia/Shanghai \` 之后即可。

```bash
# PostgreSQL：
#   -e DATABASE_DRIVER=postgres \
#   -e DATABASE_DSN='postgres://bytemuse:请替换为数据库密码@请替换为数据库主机:5432/bytemuse?sslmode=disable' \
# MySQL：
#   -e DATABASE_DRIVER=mysql \
#   -e DATABASE_DSN='bytemuse:请替换为数据库密码@tcp(请替换为数据库主机:3306)/bytemuse?charset=utf8mb4&parseTime=true&loc=Local' \

docker run -d --name byte-muse \
  --restart always \
  -p 3750:3750 \
  -e TZ=Asia/Shanghai \
  -e ADMIN_USERNAME=admin \
  -e ADMIN_PASSWORD='请替换为管理员密码' \
  -e SESSION_SECRET='请替换为32字节以上随机串' \
  -v /path/to/byte-muse/data:/data \
  -v /path/to/byte-muse/strm:/strm \
  ghcr.io/w2z/byte-muse-go:latest
```

`/path/to/byte-muse/data` 与 `/path/to/byte-muse/strm` 需要允许容器内 UID 65532 写入；只挂载 `/data` 与 `/strm`，不要覆盖镜像里的 `/app`。以上命令只启动应用容器，需要抓取增强服务时使用下面的 Compose 配置。

## Docker Compose

`deploy/` 下保留 `compose.sqlite.yaml`、`compose.postgres.yaml`、`compose.mysql.yaml` 三个模板：SQLite 用文件库，PostgreSQL 与 MySQL 模板额外内置同方言的数据库服务。下面是 SQLite 的完整配置，PostgreSQL 与 MySQL 按注释替换两行、并启用对应的数据库服务块即可。模板都直接写出配置值，不使用环境变量插值：应用容器固定命名 `byte-muse-go`（Compose 服务名仍是 `byte-muse`），并附带 `cloudflarebypass` 抓取增强服务，各服务接入同一 `bridge` 网络。代理地址与宿主机路径是占位符，`ADMIN_USERNAME`、`ADMIN_PASSWORD`、`SESSION_SECRET` 以及数据库密码也是占位值，部署前在文件中替换：`SESSION_SECRET` 少于 32 字节时服务会直接拒绝启动。真实拓扑与凭据只留在部署机上。

```yaml
services:
  byte-muse:
    image: ghcr.io/w2z/byte-muse-go:latest
    container_name: byte-muse-go
    restart: always
    networks:
      - bridge
    ports:
      - "3750:3750"
    # PostgreSQL / MySQL 部署时启用：等数据库健康后再启动应用，避免迁移连不上反复重启。
    # depends_on:
    #   postgres:
    #     condition: service_healthy
    environment:
      TZ: Asia/Shanghai
      # 数据库三选一：默认 SQLite 用下面两行，PostgreSQL 与 MySQL 用注释中的两行替换。
      DATABASE_DRIVER: sqlite
      DATABASE_DSN: file:/data/bytemuse.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)
      # PostgreSQL：DSN 主机名就是下面 postgres 服务名
      # DATABASE_DRIVER: postgres
      # DATABASE_DSN: postgres://bytemuse:请替换为数据库密码@postgres:5432/bytemuse?sslmode=disable
      # MySQL：DSN 主机名就是下面 mysql 服务名
      # DATABASE_DRIVER: mysql
      # DATABASE_DSN: bytemuse:请替换为数据库密码@tcp(mysql:3306)/bytemuse?charset=utf8mb4&parseTime=true&loc=Local
      # 部署前必须替换下面三项：管理员账号密码，以及 32 字节以上随机会话密钥。
      # 占位值长度不足 32 字节，未替换时服务会直接拒绝启动。
      ADMIN_USERNAME: admin
      ADMIN_PASSWORD: 请替换为管理员密码
      SESSION_SECRET: 请替换SESSION_SECRET
    volumes:
      # 只挂载 /data 与 /strm：镜像的 /app 存放服务二进制与前端产物，挂载覆盖后容器无法启动。
      # 宿主机目录需允许容器内 UID 65532 写入：chown -R 65532:65532 /path/to/byte-muse/data /path/to/byte-muse/strm
      - /path/to/byte-muse/data:/data
      - /path/to/byte-muse/strm:/strm
  # PostgreSQL 部署时启用（deploy/compose.postgres.yaml 中已启用）：
  # postgres:
  #   image: postgres:17-alpine
  #   container_name: byte-muse-postgres
  #   restart: always
  #   networks:
  #     - bridge
  #   environment:
  #     POSTGRES_DB: bytemuse
  #     POSTGRES_USER: bytemuse
  #     POSTGRES_PASSWORD: 请替换为数据库密码
  #     TZ: Asia/Shanghai
  #   healthcheck:
  #     test: ["CMD-SHELL", "pg_isready -U bytemuse -d bytemuse"]
  #     interval: 10s
  #     timeout: 5s
  #     retries: 10
  #   volumes:
  #     # PostgreSQL 数据目录；容器内以 UID 70 运行，宿主机目录需可写。
  #     - /path/to/byte-muse/pgdata:/var/lib/postgresql/data
  # MySQL 部署时启用（deploy/compose.mysql.yaml 中已启用）：
  # mysql:
  #   image: mysql:8.4
  #   container_name: byte-muse-mysql
  #   restart: always
  #   networks:
  #     - bridge
  #   environment:
  #     MYSQL_DATABASE: bytemuse
  #     MYSQL_USER: bytemuse
  #     MYSQL_PASSWORD: 请替换为数据库密码
  #     MYSQL_ROOT_PASSWORD: 请替换为数据库root密码
  #     TZ: Asia/Shanghai
  #   healthcheck:
  #     test: ["CMD-SHELL", "mysqladmin ping -h 127.0.0.1 -uroot -p$$MYSQL_ROOT_PASSWORD --silent"]
  #     interval: 10s
  #     timeout: 5s
  #     retries: 10
  #   volumes:
  #     # MySQL 数据目录；容器内以 UID 999 运行，宿主机目录需可写。
  #     - /path/to/byte-muse/mysqldata:/var/lib/mysql
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
# PostgreSQL 换成 deploy/compose.postgres.yaml，MySQL 换成 deploy/compose.mysql.yaml
docker compose -f deploy/compose.sqlite.yaml up -d
```
