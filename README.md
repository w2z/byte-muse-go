# ByteMuse Go

本项目是ByteMuse 使用AI进行 Go 的重建版本。单个容器同时提供 Web UI、REST API 与后台调度器，支持 SQLite、PostgreSQL 和 MySQL。

- 镜像地址：`ghcr.io/w2z/byte-muse-go`
- api公开链接: `https://s.apifox.cn/0d0f258c-8165-47ec-a98d-fbb718485c25`
- 容器端口：`3750`；数据目录：`/data`；strm目录: `/strm`；默认时区：`Asia/Shanghai`

## Docker 部署

三种数据库只差 `DATABASE_DRIVER` 与 `DATABASE_DSN` 两行，默认 SQLite：不设置这两个变量时数据库文件为 `/data/bytemuse.db`。下面按数据库分别给出完整命令；PostgreSQL 与 MySQL 需要自备可访问的数据库实例，想用 Compose 内置数据库见下一节。

<details open>
<summary><b>SQLite</b></summary>

```bash
docker run -d --name byte-muse-go \
  --restart always \
  -p 3750:3750 \
  -e TZ=Asia/Shanghai \
  -e ADMIN_USERNAME=admin \
  -e ADMIN_PASSWORD='请替换为管理员密码' \
  -e SESSION_SECRET='请替换SESSION_SECRET(32位)' \
  -v /path/to/byte-muse/data:/data \
  -v /path/to/byte-muse/strm:/strm \
  ghcr.io/w2z/byte-muse-go:latest
```

</details>

<details open>
<summary><b>PostgreSQL</b></summary>

```bash
docker run -d --name byte-muse-go \
  --restart always \
  -p 3750:3750 \
  -e TZ=Asia/Shanghai \
  -e DATABASE_DRIVER=postgres \
  -e DATABASE_DSN='postgres://bytemuse:请替换为数据库密码@请替换为数据库主机:5432/bytemuse?sslmode=disable' \
  -e ADMIN_USERNAME=admin \
  -e ADMIN_PASSWORD='请替换为管理员密码' \
  -e SESSION_SECRET='请替换SESSION_SECRET(32位)' \
  -v /path/to/byte-muse/data:/data \
  -v /path/to/byte-muse/strm:/strm \
  ghcr.io/w2z/byte-muse-go:latest
```

</details>

<details open>
<summary><b>MySQL</b></summary>

```bash
docker run -d --name byte-muse-go \
  --restart always \
  -p 3750:3750 \
  -e TZ=Asia/Shanghai \
  -e DATABASE_DRIVER=mysql \
  -e DATABASE_DSN='bytemuse:请替换为数据库密码@tcp(请替换为数据库主机:3306)/bytemuse?charset=utf8mb4&parseTime=true&loc=Local' \
  -e ADMIN_USERNAME=admin \
  -e ADMIN_PASSWORD='请替换为管理员密码' \
  -e SESSION_SECRET='请替换SESSION_SECRET(32位)' \
  -v /path/to/byte-muse/data:/data \
  -v /path/to/byte-muse/strm:/strm \
  ghcr.io/w2z/byte-muse-go:latest
```

</details>

`/path/to/byte-muse/data` 与 `/path/to/byte-muse/strm` 需要允许容器内 UID 65532 写入；只挂载 `/data` 与 `/strm`，不要覆盖镜像里的 `/app`。以上命令都只启动应用容器，需要抓取增强服务时使用下面的 Compose 配置。

## Docker Compose

`deploy/` 下保留 `compose.sqlite.yaml`、`compose.postgres.yaml`、`compose.mysql.yaml` 三个模板，下面按数据库给出完整配置，与这三个文件内容一致。模板都直接写出配置值，不使用环境变量插值：服务名与容器名保持同一个名字——应用 `byte-muse-go`、抓取增强 `cloudflarebypass_byte_muse_go`、内置数据库 `postgres_byte_muse_go` / `mysql_byte_muse_go`；各服务接入同一 `bridge` 网络，`DATABASE_DSN` 与 `BYPASS_URL` 直接用该名字访问。内置数据库把标准端口发布到宿主机（PostgreSQL `5432`、MySQL `3306`）供数据库客户端连接，宿主端口冲突时只改冒号左侧的值。代理地址与宿主机路径是占位符，`ADMIN_USERNAME`、`ADMIN_PASSWORD`、`SESSION_SECRET` 以及数据库密码也是占位值，部署前在文件中替换：`SESSION_SECRET` 少于 32 字节时服务会直接拒绝启动。真实拓扑与凭据只留在部署机上。

<details open>
<summary><b>SQLite</b></summary>

```yaml
services:
  byte-muse-go:
    image: ghcr.io/w2z/byte-muse-go:latest
    container_name: byte-muse-go
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
      SESSION_SECRET: 请替换SESSION_SECRET(32位)
    volumes:
      # 只挂载 /data 与 /strm：镜像的 /app 存放服务二进制与前端产物，挂载覆盖后容器无法启动。
      # 宿主机目录需允许容器内 UID 65532 写入：chown -R 65532:65532 /path/to/byte-muse/data /path/to/byte-muse/strm
      - /path/to/byte-muse/data:/data
      - /path/to/byte-muse/strm:/strm
  # 抓取增强服务：设置页把 BYPASS_ENGINE 选为 cloudflare_bypass_for_scraping、BYPASS_URL 填 http://cloudflarebypass_byte_muse_go:8000
  cloudflarebypass_byte_muse_go:
    image: ghcr.io/sarperavci/cloudflarebypassforscraping:latest
    container_name: cloudflarebypass_byte_muse_go
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

</details>

<details open>
<summary><b>PostgreSQL</b></summary>

```yaml
services:
  byte-muse-go:
    image: ghcr.io/w2z/byte-muse-go:latest
    container_name: byte-muse-go
    restart: always
    networks:
      - bridge
    ports:
      - "3750:3750"
    # 数据库未就绪时先不启动应用：迁移在启动阶段执行，连不上会反复重启。
    depends_on:
      postgres_byte_muse_go:
        condition: service_healthy
    environment:
      TZ: Asia/Shanghai
      DATABASE_DRIVER: postgres
      # 连接下面的 postgres_byte_muse_go 服务，服务名就是 DSN 主机名；改用外部数据库时替换主机、端口与密码。
      DATABASE_DSN: postgres://bytemuse:请替换为数据库密码@postgres_byte_muse_go:5432/bytemuse?sslmode=disable
      # 部署前必须替换下面三项：管理员账号密码，以及 32 字节以上随机会话密钥。
      # 占位值长度不足 32 字节，未替换时服务会直接拒绝启动。
      ADMIN_USERNAME: admin
      ADMIN_PASSWORD: 请替换为管理员密码
      SESSION_SECRET: 请替换SESSION_SECRET(32位)
    volumes:
      # 只挂载 /data 与 /strm：镜像的 /app 存放服务二进制与前端产物，挂载覆盖后容器无法启动。
      # 宿主机目录需允许容器内 UID 65532 写入：chown -R 65532:65532 /path/to/byte-muse/data /path/to/byte-muse/strm
      - /path/to/byte-muse/data:/data
      - /path/to/byte-muse/strm:/strm
  # 内置 PostgreSQL：与应用同一 bridge 网络，服务名 postgres_byte_muse_go 即 DSN 主机名。
  # 5432 端口发布到宿主机供数据库客户端连接；宿主端口被占用时只改冒号左侧的值。
  postgres_byte_muse_go:
    image: postgres:17-alpine
    container_name: postgres_byte_muse_go
    restart: always
    networks:
      - bridge
    ports:
      - "5432:5432"
    environment:
      POSTGRES_DB: bytemuse
      POSTGRES_USER: bytemuse
      POSTGRES_PASSWORD: 请替换为数据库密码
      TZ: Asia/Shanghai
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U bytemuse -d bytemuse"]
      interval: 10s
      timeout: 5s
      retries: 10
    volumes:
      # PostgreSQL 数据目录；容器内以 UID 70 运行，宿主机目录需可写。
      - /path/to/byte-muse/pgdata:/var/lib/postgresql/data
  # 抓取增强服务：设置页把 BYPASS_ENGINE 选为 cloudflare_bypass_for_scraping、BYPASS_URL 填 http://cloudflarebypass_byte_muse_go:8000
  cloudflarebypass_byte_muse_go:
    image: ghcr.io/sarperavci/cloudflarebypassforscraping:latest
    container_name: cloudflarebypass_byte_muse_go
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

</details>

<details open>
<summary><b>MySQL</b></summary>

```yaml
services:
  byte-muse-go:
    image: ghcr.io/w2z/byte-muse-go:latest
    container_name: byte-muse-go
    restart: always
    networks:
      - bridge
    ports:
      - "3750:3750"
    # 数据库未就绪时先不启动应用：迁移在启动阶段执行，连不上会反复重启。
    depends_on:
      mysql_byte_muse_go:
        condition: service_healthy
    environment:
      TZ: Asia/Shanghai
      DATABASE_DRIVER: mysql
      # 连接下面的 mysql_byte_muse_go 服务，服务名就是 DSN 主机名；改用外部数据库时替换主机、端口与密码。
      DATABASE_DSN: bytemuse:请替换为数据库密码@tcp(mysql_byte_muse_go:3306)/bytemuse?charset=utf8mb4&parseTime=true&loc=Local
      # 部署前必须替换下面三项：管理员账号密码，以及 32 字节以上随机会话密钥。
      # 占位值长度不足 32 字节，未替换时服务会直接拒绝启动。
      ADMIN_USERNAME: admin
      ADMIN_PASSWORD: 请替换为管理员密码
      SESSION_SECRET: 请替换SESSION_SECRET(32位)
    volumes:
      # 只挂载 /data 与 /strm：镜像的 /app 存放服务二进制与前端产物，挂载覆盖后容器无法启动。
      # 宿主机目录需允许容器内 UID 65532 写入：chown -R 65532:65532 /path/to/byte-muse/data /path/to/byte-muse/strm
      - /path/to/byte-muse/data:/data
      - /path/to/byte-muse/strm:/strm
  # 内置 MySQL：与应用同一 bridge 网络，服务名 mysql_byte_muse_go 即 DSN 主机名。
  # 3306 端口发布到宿主机供数据库客户端连接；宿主端口被占用时只改冒号左侧的值。
  mysql_byte_muse_go:
    image: mysql:8.4
    container_name: mysql_byte_muse_go
    restart: always
    networks:
      - bridge
    ports:
      - "3306:3306"
    environment:
      MYSQL_DATABASE: bytemuse
      MYSQL_USER: bytemuse
      MYSQL_PASSWORD: 请替换为数据库密码
      MYSQL_ROOT_PASSWORD: 请替换为数据库root密码
      TZ: Asia/Shanghai
    healthcheck:
      test: ["CMD-SHELL", "mysqladmin ping -h 127.0.0.1 -uroot -p$$MYSQL_ROOT_PASSWORD --silent"]
      interval: 10s
      timeout: 5s
      retries: 10
    volumes:
      # MySQL 数据目录；容器内以 UID 999 运行，宿主机目录需可写。
      - /path/to/byte-muse/mysqldata:/var/lib/mysql
  # 抓取增强服务：设置页把 BYPASS_ENGINE 选为 cloudflare_bypass_for_scraping、BYPASS_URL 填 http://cloudflarebypass_byte_muse_go:8000
  cloudflarebypass_byte_muse_go:
    image: ghcr.io/sarperavci/cloudflarebypassforscraping:latest
    container_name: cloudflarebypass_byte_muse_go
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

</details>