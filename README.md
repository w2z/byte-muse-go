# ByteMuse Go

本项目是ByteMuse 使用AI进行 Go 的重建版本。单个容器同时提供 Web UI、REST API 与后台调度器，支持 SQLite、PostgreSQL 和 MySQL。

演员目录与热门演员：后台任务「同步热门演员」按 ACTOR_SCHEDULE_TIME 更新 JavDB 演员月榜并执行已订阅演员追新；固定任务「同步演员目录」每天 04:00 从 gfriends 官方 Filetree.json 幂等导入演员姓名、别名和头像 URL。演员页的「全部演员」包含系统已有及目录导入演员，「热门」仅显示最近一次成功发布的热门榜。也可在维护窗口执行 bytemuse sync-actors gfriends 或 bytemuse sync-actors hot，命令不会创建订阅或下载任务。

JavDB App 接入：详情采集优先读取 App JSON 的影片及演员资料，补空头像、追加别名并保存来源演员 ID 到采集快照，不覆盖订阅日期。`POST /api/v1/collection/runs` 支持 `{"source":"javdb","kind":"actor","query":"演员来源ID","page":1}`，查询指定作品页；它不同于 AVBase 的演员名查询。热门演员仍使用网页月榜，gfriends 目录不变。磁力资源搜索按精确番号匹配后归入 BT，与 Nyaa 共用 `BT_DEFAULT_DOWNLOADER` 及下载状态机；资料采集本身不触发下载。

协议参考 [miyabi](https://github.com/ppxb/miyabi/tree/88c5f95f7a0c278b806563874c109908f7be1fe8/internal/javdb) 的上游路径、请求参数与签名协议，适配器在本项目独立实现。当前使用 4 条固定初始线路和进程内故障切换；动态备用域名解密、最快线路探测、跨重启线路保存、发现页及其磁力操作 UI 尚未接入。

- 镜像地址：`ghcr.io/w2z/byte-muse-go`
- api公开链接: `https://s.apifox.cn/0d0f258c-8165-47ec-a98d-fbb718485c25`
- 容器端口：`3750`；数据目录：`/data`（影片封面缓存在 `/data/cover`）；strm目录: `/strm`；默认时区：`Asia/Shanghai`

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
  -e DATABASE_DSN='postgres://bytemuse:请替换为数据库密码@请替换为宿主机地址:5431/bytemuse?sslmode=disable' \
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
  -e DATABASE_DSN='bytemuse:请替换为数据库密码@tcp(请替换为宿主机地址:3307)/bytemuse?charset=utf8mb4&parseTime=true&loc=Local' \
  -e ADMIN_USERNAME=admin \
  -e ADMIN_PASSWORD='请替换为管理员密码' \
  -e SESSION_SECRET='请替换SESSION_SECRET(32位)' \
  -v /path/to/byte-muse/data:/data \
  -v /path/to/byte-muse/strm:/strm \
  ghcr.io/w2z/byte-muse-go:latest
```

</details>

`/path/to/byte-muse/data` 与 `/path/to/byte-muse/strm` 需要允许容器内 UID 65532 写入；只挂载 `/data` 与 `/strm`，不要覆盖镜像里的 `/app`。以上命令都只启动应用容器，需要抓取增强服务时使用下面的 Compose 配置。

影片封面默认持久化到 `/data/cover`，文件名是番号（例如 `sons-1223.png`），随 `/data` 一起保留，不需要额外挂载；缓存目录不可写或源站不可用时页面自动回退到源站地址。缓存目录可用 `COVER_ROOT` 覆盖，默认值 `/data/cover`。
设置页的「外网访问地址」已配置时，页面封面与企业微信封面推送都改用 `{外网访问地址}/api/v1/covers/{番号}` 取图，第三方与页面拿到同一张缓存图；留空时页面用跟随自身来源的相对地址，企业微信直接拉取图床原图。当该前缀为 https://i0.wp.com/、https://i1.wp.com/、https://i3.wp.com/ 或 https://i4.wp.com/ 时，页面与企业微信封面直接使用代理域名加原图主机、路径和查询参数，例如 https://i0.wp.com/c0.jdbstatic.com/covers/p9/P98NVa.jpg，不拼接 /api/v1/covers。
strm 文件固定写入容器内 `/strm`；设置页不再配置本地根目录。宿主机如需持久化，请将宿主目录映射到容器的 `/strm`；历史 `STRM_ROOT` 设置不会再生效，已有文件不会自动迁移。

在「设置 → 网盘 → STRM 生成」开启 **115 事件监听**，填写同一已绑定账号的生活事件 Cookie（UID、CID、SEID）、115 网盘映射和 STRM 文件播放地址。Cookie 加密保存；未填写 Cookie 时开关关闭且禁用，清空 Cookie 会自动关闭，保存时后端同时持久化关闭状态（部分更新也生效）。生活事件接口不接受 OpenAPI 令牌，需要在 115 开启生活事件记录。服务每 30 秒轮询，失败后 60 秒重试，设置保存后下一轮生效。首次开启以当前事件为基线，已有文件请先手动生成；重启后从成功游标续接。上传、接收、复制、移动、重命名和新建目录事件合并触发全部 115 映射扫描，复用格式、大小及排除规则，生成或更新 STRM；不扫描 CD2，不删除本地旧文件。映射失败保留游标重试，重复执行不改写相同内容；错误与同步结果见「日志 → 媒体库」。

事件采用参考项目的生活事件轮询思路，但不复制其删除同步。Web 事件接口只提供最近 10000 条，积压超出窗口时执行完整映射扫描补偿。115 未提供的事件（例如回收站恢复或部分第三方上传）无法靠监听发现，需手动生成补齐；大目录的事件批次扫描耗时取决于目录规模与 115 限流。

## Docker Compose

`deploy/` 下保留 `compose.sqlite.yaml`、`compose.postgres.yaml`、`compose.mysql.yaml` 三个模板，下面按数据库给出完整配置，与这三个文件内容一致。模板直接写出配置值，不使用环境变量插值。`DATABASE_DSN` 使用容器可访问的宿主机地址及映射端口；`BYPASS_URL` 使用 `http://cloudflarebypass_byte_muse_go:8000`。部署前替换宿主机地址、代理地址、挂载路径及账号密码占位值；`SESSION_SECRET` 少于 32 字节时服务会拒绝启动。真实拓扑与凭据只留在部署机上。

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
      # 影片封面缓存在 /data/cover，随 /data 一起持久化，不需要额外挂载。
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
      DATABASE_DSN: postgres://bytemuse:请替换为数据库密码@请替换为宿主机地址:5431/bytemuse?sslmode=disable
      # 部署前必须替换下面三项：管理员账号密码，以及 32 字节以上随机会话密钥。
      # 占位值长度不足 32 字节，未替换时服务会直接拒绝启动。
      ADMIN_USERNAME: admin
      ADMIN_PASSWORD: 请替换为管理员密码
      SESSION_SECRET: 请替换SESSION_SECRET(32位)
    volumes:
      # 只挂载 /data 与 /strm：镜像的 /app 存放服务二进制与前端产物，挂载覆盖后容器无法启动。
      # 宿主机目录需允许容器内 UID 65532 写入：chown -R 65532:65532 /path/to/byte-muse/data /path/to/byte-muse/strm
      # 影片封面缓存在 /data/cover，随 /data 一起持久化，不需要额外挂载。
      - /path/to/byte-muse/data:/data
      - /path/to/byte-muse/strm:/strm
  # 内置 PostgreSQL
  postgres_byte_muse_go:
    image: postgres:17-alpine
    container_name: postgres_byte_muse_go
    restart: always
    networks:
      - bridge
    ports:
      - "5431:5432"
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
      DATABASE_DSN: bytemuse:请替换为数据库密码@tcp(请替换为宿主机地址:3307)/bytemuse?charset=utf8mb4&parseTime=true&loc=Local
      # 部署前必须替换下面三项：管理员账号密码，以及 32 字节以上随机会话密钥。
      # 占位值长度不足 32 字节，未替换时服务会直接拒绝启动。
      ADMIN_USERNAME: admin
      ADMIN_PASSWORD: 请替换为管理员密码
      SESSION_SECRET: 请替换SESSION_SECRET(32位)
    volumes:
      # 只挂载 /data 与 /strm：镜像的 /app 存放服务二进制与前端产物，挂载覆盖后容器无法启动。
      # 宿主机目录需允许容器内 UID 65532 写入：chown -R 65532:65532 /path/to/byte-muse/data /path/to/byte-muse/strm
      # 影片封面缓存在 /data/cover，随 /data 一起持久化，不需要额外挂载。
      - /path/to/byte-muse/data:/data
      - /path/to/byte-muse/strm:/strm
  # 内置 MySQL
  mysql_byte_muse_go:
    image: mysql:8.4
    container_name: mysql_byte_muse_go
    restart: always
    networks:
      - bridge
    ports:
      - "3307:3306"
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
