# ByteMuse Go

本项目是ByteMuse 使用AI进行 Go 的重建版本。单个容器同时提供 Web UI、REST API 与后台调度器，支持 SQLite、PostgreSQL 和 MySQL。

下载来源追溯：新任务在提交下载器前将实际下载链接保存到 `download_tasks.download_url`，BT 保存磁力链接，PT 保存本次获取种子文件使用的 URL；`resource_uri` 继续保存原始资源引用。迁移 41 只新增可空字段，历史记录保持 NULL。PT 链接可能含临时令牌且会过期，仅用于内部追溯，不新增到列表 API 或日志；重新获取种子仍使用资源引用和站点认证。

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

容器直接以 root 运行，`/path/to/byte-muse/data` 与 `/path/to/byte-muse/strm` 无需 chown，只要不是只读挂载即可；只挂载 `/data` 与 `/strm`，不要覆盖镜像里的 `/app`。以上命令都只启动应用容器，需要抓取增强服务时使用下面的 Compose 配置。

影片封面默认持久化到 `/data/cover`，文件名是番号（例如 `sons-1223.png`），随 `/data` 一起保留，不需要额外挂载；缓存目录不可写或源站不可用时页面自动回退到源站地址。缓存目录可用 `COVER_ROOT` 覆盖，默认值 `/data/cover`。
设置页的「外网访问地址」已配置时，页面封面与企业微信封面推送都改用 `{外网访问地址}/api/v1/covers/{番号}` 取图，第三方与页面拿到同一张缓存图；留空时页面用跟随自身来源的相对地址，企业微信直接拉取图床原图。当该前缀为 https://i0.wp.com/、https://i1.wp.com/、https://i3.wp.com/ 或 https://i4.wp.com/ 时，页面与企业微信封面直接使用代理域名加原图主机、路径和查询参数，例如 https://i0.wp.com/c0.jdbstatic.com/covers/p9/P98NVa.jpg，不拼接 /api/v1/covers。
strm 文件固定写入容器内 `/strm`；设置页不再配置本地根目录。宿主机如需持久化，请将宿主目录映射到容器的 `/strm`；历史 `STRM_ROOT` 设置不会再生效，已有文件不会自动迁移。

设置页的扫描入库与 STRM 生成使用独立后台任务，各自只允许一个未结束任务。执行期间禁用对应启动按钮，提供暂停、继续和取消；暂停在下一个安全处理点生效，取消保留已入库或已写出的结果。STRM 生成的已处理数、总数和百分比仅统计视频对应的 STRM，字幕、NFO、图片等附件下载单独计数，任务仍会等待附件下载结束。任务状态、动态总数和已处理数保存在数据库，页面关闭不取消执行，重新打开继续显示进度。服务重启后，运行中的扫描入库和 STRM 生成自动沿原任务断点继续；刷新 STRM 媒体信息的排队或运行任务也自动继续。手动暂停保持暂停，可点击继续；手动取消、已完成和失败任务不会自动启动。没有断点的历史扫描或生成任务仍须手动新建；活动进度最多每 250 毫秒落库一次，阶段变化与控制状态立即保存。迁移 36 创建 `scan_tasks` 表，迁移 40 新增 `task_checkpoints` 断点表，不回填旧任务、不修改已有媒体数据。

扫描入库、生成 STRM、刷新 STRM 媒体信息的最新任务失败、取消、中断或部分失败时，操作按钮后显示红色「继续失败的任务」。续跑沿用原任务及已保存的目录、生成模式，复用成功读取的目录页并跳过已确认完成的文件或媒体项；全量生成续跑不会重复清空已完成清理的映射。扫描入库只在批次写入成功后确认断点，未提交尾批会继续处理。服务重启后断点仍保留；升级前没有断点的旧任务无法精确续跑，不显示此按钮。需要改目录、模式或重新读取整个目录时使用原启动按钮创建新任务。断点确认前发生崩溃的操作可能安全重试，媒体探测不承诺第三方恰好执行一次。
在「STRM文件播放地址」上方的「下载媒体」开启下载，并用标签输入框编辑后缀（默认 `srt、ssa、ass、nfo、jpg、png`）。默认关闭；开启后每次生成复用网盘遍历，同时下载最多 5 个文件，保留网盘请求间隔与并发限制，生成任务的重试由独立阶段管理。下载保留原文件名及相对目录，遵循映射的排除规则，不受最小视频体积限制；与视频格式重合时优先下载原文件。增量生成和事件同步跳过本地已有媒体，全量生成重新下载并原子替换；失败保留旧文件、清理未完成文件，结果分别显示下载成功、跳过与失败数量。所有下载结束后才触发已配置的 Emby 刷新。迁移 37 只登记两个默认设置，不覆盖既有配置或媒体。

STRM 生成任务内部使用独立的目录扫描、生成队列和下载队列，待处理项暂存在系统临时目录，扫描不等待下游完成。目录页读取失败后等待 60 秒，只重试原目录页；生成连续失败 10 次、下载明确限流连续失败 20 次后停止该阶段的新工作，等待已发出的操作结束，再冷却 60 秒。冷却后只探测一个失败项：成功恢复正常处理，限流再失败则继续等待。普通附件错误（链接过期、权限拒绝、网络及本地文件错误等）每项最多执行 3 轮，失败项排到队尾，不触发整个下载阶段冷却；每轮遇到直链过期最多额外重新取链一次，3 轮仍失败就保留失败明细和数量，继续处理其他文件。下载时原样使用 115 返回的直链，不根据 t 参数或本地时间预判过期；仅在下载端的 401/403 响应明确报告链接过期时重新取链，普通权限拒绝和限流不当作链接过期。全部附件处理结束后汇总成功、跳过与失败数，可通过「继续失败的任务」重新处理失败项，已成功断点不会重复执行。重试计数属于本次运行，重启或手动续跑重新计数。页面冷却状态显示「等待重试」，具体原因由任务详情及脱敏日志说明，不把普通失败误报为 115 限流。扫描与生成阶段保持原有冷却策略，手动暂停、取消始终有效。总进度仅累计成功和跳过的 STRM，重试不重复计数；明细恢复成功后清除文件错误并扣除目录失败数。任务取消保留成功断点，服务重启由目录页和成功断点重建队列，不依赖临时文件恢复。

115 的 STRM 播放链接使用 `/files/play/115/<pick_code>`，播放时直接换取直链，无需查询文件信息；CloudDrive2 继续使用文件路径。升级前使用 `file_id` 的 115 STRM 文件需在设置页重新生成，生成操作会更新对应文件内容；扫描结果缺少 `pick_code` 时报告该文件生成失败。

在「设置 → 网盘 → STRM 生成」开启 **115 事件监听**，填写同一已绑定账号的生活事件 Cookie（UID、CID、SEID）、115 网盘映射和 STRM 文件播放地址。Cookie 加密保存；未填写 Cookie 时开关关闭且禁用，清空 Cookie 会自动关闭，保存时后端同时持久化关闭状态（部分更新也生效）。生活事件接口不接受 OpenAPI 令牌，需要在 115 开启生活事件记录。服务每 30 秒轮询，失败后 60 秒重试，设置保存后下一轮生效。首次开启以当前事件为基线，不回放历史；重启后从成功游标续接。上传、接收、复制、移动、重命名和新建目录事件按文件 ID 合并，核验当前路径后只生成命中的文件或扫描变化目录的子树，复用格式、大小及排除规则；不扫描 CD2。失败保留游标重试，重复执行不改写相同内容；错误与生成、下载、删除数量见「日志 → 115 事件」。移动和重命名生成新路径，暂不自动清理旧路径。

删除事件（类型 22）先确认目标详情不存在，并完整列举父目录再次核验，再按文件 ID 清理本程序登记的 STRM 或下载附件；删除目录时只处理已登记后代，仍存在于网盘的后代和其他文件保留，最后只移除空目录。限流、超时、权限错误、分页异常不会被当作删除；缺少父目录或核验失败时保留本地文件并重试。路径必须在当前映射内且不含符号链接，内容 SHA256 必须与登记值一致；外部修改保留并记日志。不会删除网盘文件，也不会按视频同名连带删除仍存在的封面或字幕。

迁移 42 新增受管文件归属表，手动生成与事件生成在实际写入时登记，跳过的历史文件不自动接管。升级前已存在的 STRM 和附件没有归属证据，不自动清理；账号、映射或过滤配置变化后的旧记录也不自动清理。迁移不回填历史文件，可在备份后通过明确的全量生成重新建立当前文件归属。Web 事件接口只提供最近 10000 条，积压超窗或生成事件缺少定位字段时执行全部 115 映射的生成补偿，不能补偿已丢失的删除事件。115 未提供的事件（例如回收站恢复或部分第三方上传）需手动生成补齐。

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
      # 容器直接以 root 运行，宿主机目录无需 chown，只要不是只读挂载即可。
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

  # 可选 FlareSolverr：取消下方服务配置的注释后启用。
  # 设置页：BYPASS_ENGINE=flaresolverr，BYPASS_URL=http://flareSolverr_byte_muse_go:8191。
  # 需要代理时，在 ByteMuse 设置 PROXY 并开启 BYPASS_USE_PROXY，由请求传入代理。
  # flareSolverr_byte_muse_go:
  #   image: ghcr.io/flaresolverr/flaresolverr:latest
  #   container_name: flareSolverr_byte_muse_go
  #   restart: always
  #   networks:
  #     - bridge
  #   ports:
  #     - "8191:8191"
  #   environment:
  #     TZ: Asia/Shanghai
  #     LOG_LEVEL: info
  #     LOG_HTML: "false"
  #     # 可选默认代理：按需取消以下参数的注释；请求或会话传入的代理优先。
  #     # 设置后即使 ByteMuse 关闭 BYPASS_USE_PROXY，FlareSolverr 仍会使用默认代理。
  #     # PROXY_URL: http://<代理主机>:<代理端口>
  #     # 代理需要认证时再填写下面两项。
  #     # PROXY_USERNAME: <代理用户名>
  #     # PROXY_PASSWORD: <代理密码>

  # 可选 Scrapling：这是独立 MCP 服务，当前 ByteMuse 未接入，不能直接作为 BYPASS_URL。
  # 取消下方服务配置的注释并替换认证令牌后启用；MCP 路径为 /mcp。
  # MCP 客户端使用 Authorization: Bearer <认证令牌>；抓取代理通过工具的 proxy 参数传入。
  # 浏览器抓取不依赖 HTTP_PROXY / HTTPS_PROXY；在 open_session 或单次抓取工具中传 proxy。
  # 无认证参数示例：{"proxy": "http://<代理主机>:<代理端口>"}
  # 带认证参数示例：{"proxy": "http://<代理用户名>:<代理密码>@<代理主机>:<代理端口>"}
  # 用户名、密码中的特殊字符需 URL 编码；会话请求复用 open_session 设置的代理。
  # scrapling_byte_muse_go:
  #   image: ghcr.io/d4vinci/scrapling:latest
  #   container_name: scrapling_byte_muse_go
  #   restart: always
  #   networks:
  #     - bridge
  #   ports:
  #     - "8001:8000"
  #   command: ["mcp", "--http", "--host", "0.0.0.0", "--port", "8000"]
  #   environment:
  #     TZ: Asia/Shanghai
  #     SCRAPLING_MCP_AUTH_TOKEN: 请替换为随机认证令牌

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
      # 容器直接以 root 运行，宿主机目录无需 chown，只要不是只读挂载即可。
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

  # 可选 FlareSolverr：取消下方服务配置的注释后启用。
  # 设置页：BYPASS_ENGINE=flaresolverr，BYPASS_URL=http://flareSolverr_byte_muse_go:8191。
  # 需要代理时，在 ByteMuse 设置 PROXY 并开启 BYPASS_USE_PROXY，由请求传入代理。
  # flareSolverr_byte_muse_go:
  #   image: ghcr.io/flaresolverr/flaresolverr:latest
  #   container_name: flareSolverr_byte_muse_go
  #   restart: always
  #   networks:
  #     - bridge
  #   ports:
  #     - "8191:8191"
  #   environment:
  #     TZ: Asia/Shanghai
  #     LOG_LEVEL: info
  #     LOG_HTML: "false"
  #     # 可选默认代理：按需取消以下参数的注释；请求或会话传入的代理优先。
  #     # 设置后即使 ByteMuse 关闭 BYPASS_USE_PROXY，FlareSolverr 仍会使用默认代理。
  #     # PROXY_URL: http://<代理主机>:<代理端口>
  #     # 代理需要认证时再填写下面两项。
  #     # PROXY_USERNAME: <代理用户名>
  #     # PROXY_PASSWORD: <代理密码>

  # 可选 Scrapling：这是独立 MCP 服务，当前 ByteMuse 未接入，不能直接作为 BYPASS_URL。
  # 取消下方服务配置的注释并替换认证令牌后启用；MCP 路径为 /mcp。
  # MCP 客户端使用 Authorization: Bearer <认证令牌>；抓取代理通过工具的 proxy 参数传入。
  # 浏览器抓取不依赖 HTTP_PROXY / HTTPS_PROXY；在 open_session 或单次抓取工具中传 proxy。
  # 无认证参数示例：{"proxy": "http://<代理主机>:<代理端口>"}
  # 带认证参数示例：{"proxy": "http://<代理用户名>:<代理密码>@<代理主机>:<代理端口>"}
  # 用户名、密码中的特殊字符需 URL 编码；会话请求复用 open_session 设置的代理。
  # scrapling_byte_muse_go:
  #   image: ghcr.io/d4vinci/scrapling:latest
  #   container_name: scrapling_byte_muse_go
  #   restart: always
  #   networks:
  #     - bridge
  #   ports:
  #     - "8001:8000"
  #   command: ["mcp", "--http", "--host", "0.0.0.0", "--port", "8000"]
  #   environment:
  #     TZ: Asia/Shanghai
  #     SCRAPLING_MCP_AUTH_TOKEN: 请替换为随机认证令牌

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
      # 容器直接以 root 运行，宿主机目录无需 chown，只要不是只读挂载即可。
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

  # 可选 FlareSolverr：取消下方服务配置的注释后启用。
  # 设置页：BYPASS_ENGINE=flaresolverr，BYPASS_URL=http://flareSolverr_byte_muse_go:8191。
  # 需要代理时，在 ByteMuse 设置 PROXY 并开启 BYPASS_USE_PROXY，由请求传入代理。
  # flareSolverr_byte_muse_go:
  #   image: ghcr.io/flaresolverr/flaresolverr:latest
  #   container_name: flareSolverr_byte_muse_go
  #   restart: always
  #   networks:
  #     - bridge
  #   ports:
  #     - "8191:8191"
  #   environment:
  #     TZ: Asia/Shanghai
  #     LOG_LEVEL: info
  #     LOG_HTML: "false"
  #     # 可选默认代理：按需取消以下参数的注释；请求或会话传入的代理优先。
  #     # 设置后即使 ByteMuse 关闭 BYPASS_USE_PROXY，FlareSolverr 仍会使用默认代理。
  #     # PROXY_URL: http://<代理主机>:<代理端口>
  #     # 代理需要认证时再填写下面两项。
  #     # PROXY_USERNAME: <代理用户名>
  #     # PROXY_PASSWORD: <代理密码>

  # 可选 Scrapling：这是独立 MCP 服务，当前 ByteMuse 未接入，不能直接作为 BYPASS_URL。
  # 取消下方服务配置的注释并替换认证令牌后启用；MCP 路径为 /mcp。
  # MCP 客户端使用 Authorization: Bearer <认证令牌>；抓取代理通过工具的 proxy 参数传入。
  # 浏览器抓取不依赖 HTTP_PROXY / HTTPS_PROXY；在 open_session 或单次抓取工具中传 proxy。
  # 无认证参数示例：{"proxy": "http://<代理主机>:<代理端口>"}
  # 带认证参数示例：{"proxy": "http://<代理用户名>:<代理密码>@<代理主机>:<代理端口>"}
  # 用户名、密码中的特殊字符需 URL 编码；会话请求复用 open_session 设置的代理。
  # scrapling_byte_muse_go:
  #   image: ghcr.io/d4vinci/scrapling:latest
  #   container_name: scrapling_byte_muse_go
  #   restart: always
  #   networks:
  #     - bridge
  #   ports:
  #     - "8001:8000"
  #   command: ["mcp", "--http", "--host", "0.0.0.0", "--port", "8000"]
  #   environment:
  #     TZ: Asia/Shanghai
  #     SCRAPLING_MCP_AUTH_TOKEN: 请替换为随机认证令牌

networks:
  bridge:
    driver: bridge
```

```bash
docker compose -f deploy/compose.mysql.yaml up -d
```

</details>

## MCP 服务

在独立的「设置 → 站点设置 → MCP 服务」分组开启并保存，默认关闭，保存后立即生效，无需重启。首次开启自动生成访问令牌，加密保存；点击令牌框的显示按钮后可复制。MCP 不依赖对话 Agent 开关或 OpenAI 模型配置，保存与重置也只作用于 MCP 分组。

客户端选择 **Streamable HTTP**，服务地址为 `https://你的域名/api/v1/mcp`（也可复制设置页显示的地址），配置请求头 `Authorization: Bearer <MCP 访问令牌>`。设置页地址使用当前浏览器访问站点的协议、域名或 IP 与端口，不使用封面地址前缀 `EXTERNAL_DOMAIN`；外部客户端需要连接时，请通过它可访问的站点地址打开设置页后复制。客户端须支持自定义 Bearer 请求头；当前不提供 OAuth 授权流程或旧版 HTTP+SSE `/sse` 端点。请求使用 `Content-Type: application/json`，兼容客户端的 `Accept: application/json, text/event-stream`。

MCP 复用渠道 Agent 的工具目录，支持影片、演员、标签、榜单、媒体库、下载任务和日志查询，以及影片/演员/标签订阅、取消订阅和触发任务。依赖当前消息渠道的 `send_message` 不对外开放。影片订阅沿用现有规则，会登记一次资源搜索与下载受理；受理不表示下载完成，重复请求由现有业务服务去重。工具参数以 `tools/list` 返回的 JSON Schema 为准。

影片列表使用 `query_media`（权限页显示“查询影片列表”）：不需要关键词，支持 `page`（默认 1，最多 1000000）、`page_size`（默认 20，范围 1–20）、`search`、`subscription`、`download`、`library`、`video_type` 和 `vr`，与网页影片列表共用查询服务及排序、筛选规则；不主动采集资料、订阅或下载，按需标题翻译沿用网页规则。返回 `page`、`page_size`、`total`、`returned`、`has_more` 和精简 `items`，每项包含影片 ID、番号、标题、影片类型、封面、发行日期和订阅/下载/媒体库状态；缺失字段保留 null。新增工具默认未授权，须勾选保存后刷新客户端目录。

「工具权限」按实际工具逐项勾选，默认全部不选，已有实例未配置权限时同样不授予任何工具。勾选并保存后，只有白名单中的工具才会出现在 MCP 工具列表里；客户端即使缓存了工具名，调用未勾选或已撤销的工具仍会被拒绝。权限对后续请求立即生效，已受理任务继续执行；新增权限可能需要客户端刷新工具列表。网页和消息渠道 Agent 的功能不受 MCP 权限限制。每项说明会标明工具的业务行为，部分工具会联网采集、保存资料或登记下载，授权前按说明选择。

权限保存为既有 `app_settings` 中的 `MCP_ALLOWED_TOOLS` JSON 字符串数组；空字符串或 `[]` 清空，省略保留，重复项去重排序，不支持通配符。未知或已移除的工具名不会产生访问权限。系统设置响应通过 `mcp_tools` 返回实际可授权目录，前端不维护独立工具名称清单。不新增表，不回填历史设置。

关闭后后续 MCP 请求返回 404，保留令牌。令牌框右侧的「更换令牌」生成 32 字节安全随机草稿（64 位十六进制），点击「保存设置」后才生效，保存前可重置撤销；与输入框相连的「复制」按钮复制当前输入。自动生成后仍可直接编辑自定义值，保存后旧令牌立即失效；开启状态下清空令牌并保存也会重新生成。自定义令牌支持 32 到 128 位字母、数字、下划线或短横线。已经受理的业务任务沿原生命周期执行。管理登录 Cookie 不作为 MCP 凭据，MCP 令牌也不能访问后台设置 API。该令牌允许执行目录中列出的业务操作，仅交给可信客户端。

服务地址下方提供客户端配置：Claude Code 的 `.mcp.json`、Cursor 的 `.cursor/mcp.json` 使用 JSON，Codex 的 `~/.codex/config.toml` 使用原生 TOML。配置预览用 `<MCP_TOKEN>` 遮蔽令牌；「复制配置」会填入已保存的令牌，新令牌草稿需先保存。未配置令牌时复制的是占位模板，需要手动替换占位符。令牌及配置复制均兼容局域网 HTTP 浏览器。

服务采用无会话模式：POST 执行 MCP JSON-RPC，通知返回 202，GET 和 DELETE 返回 405；无需保存 `Mcp-Session-Id`。单次请求体上限 1 MiB，单次工具执行上限 60 秒。此功能复用 `app_settings`，不新增表、不回填历史数据。

## 网盘上传

115 返回限流或 HTTP 405 后，上传任务保存冷却原因和下次尝试时间，释放 worker 并由队列到期自动重试，不再在底层长时间占住并发槽而显示 0%。冷却时间在重启后保留，不扣减普通文件失败的三次重试额度；普通传输错误仍按任务重试规则处理。文件占用观察和限流等待不计入异常任务数。

同一进程内上传冷却连续失败按 60、120、240、480、960、1800 秒退避；同一批并发限流只计一轮，服务端明确的 Retry-After 优先。冷却到期后开始且成功的请求重置轮次，冷却前已在途的成功响应不重置；重启保留已持久化的截止时间，但不恢复内存退避轮次。正常上传并发不变。并发上传完成时优先将可信回执合并到准备快照；需要回查的路径仍逐文件核对 ID、大小和 SHA1，并使用上传后时间屏障。未知写入和删除仍使用严格缓存失效。

115 原名覆盖支持目标目录中多个同名旧文件：上传前在任务 JSON 快照中保存 `overwrite_snapshot.files`（旧文件 ID、SHA1、字节数），新文件身份确认后逐个核验并移入回收站，最后才允许移动模式删除本地源文件。快照一经保存，失败重试与重启不能追加后来出现的同名文件；旧文件身份改变、缺少摘要或出现同名目录时保留文件并报告具体原因。已被删除的快照文件不重复删除。此字段缺失表示旧版本任务，待执行覆盖任务在下一次处理时现场建立快照；旧版清理阶段只继承原有单文件删除范围，不批量回填历史记录，也不修改表结构。

115 上传链路的目录查询、目录创建、上传初始化和结果回查由「上传任务数量」控制并发，不再受客户端默认的单请求串行与 400ms 固定间隔限制。一个 worker 顺序执行其请求；其他 115 功能仍使用原有限速。115 返回限流时仍保留共享冷却和取消机制；增加并发不保证增加吞吐，频繁限流时应调低上传任务数量。

115 网盘设置支持选择开放应用：OpenList（`100197303`，默认）或 CloudDrive2（`100195313`）。保存 `PAN115_OPEN_APP` 后，下一次登录使用对应流程：OpenList 为 PKCE 二维码，CloudDrive2 按官方授权码方式打开授权页并通过回调取得令牌。修改选项不改变已有令牌；切换已有绑定需解除绑定并重新登录，Cookie 扫码不受影响。应用选择不保证免除 115 服务端限流。旧设置默认 OpenList，不回填历史数据。

上传调度启动时从数据库恢复一次队列，后续只在持久化成功后更新内存队列与列表；完成事件直接补位，不再为每个文件反复读取、解析和排序全部上传台账。不同文件的准备工作可并行，仅同名子目录创建与保留两者命名预留互斥。满并发期间提前检查有限数量的待上传源文件，保留占用与稳定性保护，不把等待确认当作实际传输；五个并发槽位不保证五个文件始终同时传输字节。

上传全局暂停/继续、目录和文件暂停/开始只写入一条范围控制记录，不逐条重写目录内文件任务。控制记录复用现有 `cloud_upload_records` 的 `state=control` 与 JSON 快照，按递增序号确定全局、目录、文件操作的先后优先级，重启恢复相同规则；不存在新字段的历史任务按原状态处理，不启动时回填。任务实际执行时才保存生效的控制序号及运行进度。目录开始可覆盖此前全局暂停，后来单独暂停的文件仍暂停；新全局暂停可覆盖此前单文件运行意图。已完成任务不重传，已耗尽重试或身份冲突任务不能通过开始清除错误；供应商冷却不被开始绕过。停止、删除和明确重试仍执行原有安全流程。控制请求持久化成功后刷新内存展示，显示刷新仍随任务数量增长，但不再产生数万次独立数据库写入。

115 上传复用业务层已保存的源文件版本 SHA1，不再进行第二次整文件校验；协议要求的前 128 KiB 和指定片段摘要仍独立计算。重试仅复用未变化源文件的摘要，上传成功优先使用可信回执，异常恢复保留远端身份回查，移动删除前的本地内容核对保持不变。

数据库迁移 47 新增影片 `UPPER(code)` 查询索引和下载记录 `(media_id, updated_at DESC, id ASC)` 索引，不回填、不改写历史业务记录。启动升级时建索引可能短暂阻塞相关表写入，应在维护窗口执行；回退旧程序可保留索引。标签列表改为单条 SQL 返回分页与总数，PostgreSQL 按单部影片去重标签后聚合，避免全量去重排序落盘，不修改标签计数和筛选语义。

清空列表保留去重台账，自动监控不会把已清空的相同版本重新入队。用户再次点击目录选择旁的上传图标时，仍存在于本地的已清空终态任务重新入队，按当前上传模式和同名策略执行，重新记录添加时间；未完成的恢复记录和当前可见任务不重置。两个列表没有任务时只显示标题，不显示 `(0/0)`；状态筛选不改变标题的全局任务数量。

115 上传成功确认使用结构化回执：秒传读取上传初始化中的文件 ID 和 PickCode；缺少 ID 且 PickCode 格式已知时本地解码，否则回查。普通 OSS 上传读取服务端回调，要求业务成功且文件名、大小和 SHA1 与源文件一致，不能仅凭 HTTP 200 判断成功。回执缺少身份或祖先路径时回退目录核验；超时、回调失败和内容不一致保留任务，下次先核验已上传结果。目录祖先链复用准备阶段的账号隔离缓存，新建目录从父链派生；完整回执在当前操作中交给本地后处理。覆盖旧文件及 move 重启恢复仍保留新鲜的远端核验，旧上传台账不回填，数据库结构不变。

上传模式默认「复制」，保留本地文件；「移动」仅在上传回执或必要回查确认远端文件 ID、大小和 SHA1 后，重新核对本地路径、文件身份、大小、修改时间及 SHA1，再删除该源文件，并清理文件所在目录内的空子目录，逐级清理空父目录，遇到仍包含文件的目录即停止，始终保留映射根目录。跳过同名文件不执行移动。模式在入队时固定，已入队和历史任务不受后来切换影响；清理失败保留远端回执供重试，不重复上传，不扫描删除无关的历史空目录。文件校验百分比仅显示在状态说明，上传进度列在校验期间显示「—」。Windows 通过禁止写入和删除共享的句柄删除已核对的文件；Unix 使用 advisory lock、可见写进程检查和 rooted 文件访问，外部写入方仍应遵守文件锁，不能把占用检测当作跨主机排他保证。

手动扫描采用边扫描、边入队、边上传：每个文件成功持久化后立即加入文件列表并更新目录统计，页面下一次轮询即可看到；已有队列按入队时间和并发上限开始上传，不等待整批目录扫描完成。扫描中的总任务数会持续增加，扫描期间禁用重复扫描；停止或删除全部任务会取消尚未完成的扫描，配置映射变更会取消旧扫描。扫描不修改本地文件；移动任务造成的源文件消失不会中断剩余目录扫描。

手动上传入队时持久化任务运行意图，关闭目录监控也不影响这些任务在服务重启后继续执行。明确暂停、停止、删除的任务不自动恢复。旧版本手动队列缺少运行意图时，可再次手动扫描补齐仍可见且未结束的普通任务，保留其原入队时间和上传回执，不解除暂停。

列表「清空已完成」按当前可见任务选择成功记录，在事务内按主键分批更新隐藏标记，不逐条重写完整回执，也不为清理全量读取队列；提交后立即移除内存列表项并更新计数，失败则保持列表不变。目录列表仅清理全部文件成功的目录，文件列表按文件清理；不删除本地、网盘文件或持久化上传回执。

工作线程开始处理远端目录准备时即计入「进行中」，不再伪装为等待；本地校验结束后切换到上传阶段，115 初始化及回查耗时不计为校验耗时。调度每轮限制文件占用检查数量，避免大队列逐项检查阻塞补充并发槽位；无法完全确认占用时仍保留稳定性观察与上传前后校验。115 目录准备按账号和目录 ID 共享 40 秒完整列表缓存，相同目录的并发读取合并；缓存最多保留 512 个目录、共 100000 个条目，分页失败不缓存部分结果。本程序新建目录成功后记录空目录，并立即更新父目录状态，省去新建子级与文件上传前的重复存在性查询。同名目标互斥，不同文件仍按用户并发数运行，不增加固定上传等待。上传成功优先使用秒传结果或 OSS 业务回调确认的身份；需回查时只接受上传结束后发起的目录读取，不能使用上传前缓存。覆盖和旧文件清理保留身份校验。明确限流拒绝不清空有效准备缓存；完整可信回执按文件 ID 合并到已有完整目录缓存且不延长其有效期；结果不确定、回执不完整或删除使目录内容失效，目录不存在时清除相关账号的路径缓存。重启不恢复内存目录快照，只使用持久化任务中的目录 ID 重新验证。缓存不替代移动模式删除本地文件前的保护。

原名直传重试只在上传协调器内执行一次必要的父目录回查，不先重复检查父目录。覆盖快照为空仍持久化，但没有旧文件时跳过清理阶段的额外查询；上传已成功而回查失败时，重试先核对已上传内容，旧文件清理失败只继续清理。同目录准备命中、回执完整、无重试及片段挑战的秒传新文件只需一次上传初始化，不固定追加上传后目录回查；目录准备、分页、旧文件删除及普通 OSS 传输另计。进程输出记录脱敏的「115 上传接口」和「115 上传目录缓存」诊断，包含任务标识、阶段、普通失败重试计数、接口路径、请求开始时间、耗时、错误码、是否实际尝试发送及缓存命中情况；列表请求另记录目录 ID 的摘要、offset 和 limit，便于区分分页和重复查询；不记录原始目录 ID、完整查询参数、令牌、文件名或响应正文，不逐请求写入日志数据库。可通过容器日志区分真实接口请求与本地冷却拦截。

目录身份核对使用固定的新鲜度下界：准备和恢复可复用本轮任务开始后发起的有效查询；需要额外回查时，核对要求查询晚于该文件上传返回成功的时间，不因进度通知或进入下一分支而移动下界。同一次查询若晚于多个文件各自的上传完成时间，可分别核对这些文件，不为每个文件再次刷新。相同内容确认与覆盖快照冻结共用一次结果；清理阶段省去重复的预查询，但每次删除旧文件前仍强制重新确认新旧身份、删除后仍确认新文件存在及旧文件消失。校验耗时达到目录缓存期限时重新刷新，已有远端文件的移动模式在本轮本地校验后取得一次新鲜核验结果，不因进入下一分支再次刷新。未满足写入时间、过期或失效的快照不可复用；不等待整批上传完成，也不承诺所有并发时序下整个目录只查一次。

上传任务数量默认 5，允许 1–32 个文件并发。115 控制接口与普通 API 共享 400ms 最小间隔及串行配额，OSS 数据传输仍按文件并发执行。限流返回持久化队列等待，冷却后先串行探测；恢复期按至少 1 秒间隔发起上传控制请求，持续成功满 1 分钟后才清零退避等级。此策略不能保证第三方账号额度或风控一定恢复。保存后立即唤醒调度器，增大时补充任务，减小时保留当前传输直到运行数低于上限，再补新任务，无需重启。任务按入队正序派发，但并发完成顺序不保证；同源版本或冲突目标互斥，目录创建与保留两者命名预留串行。迁移 46 只初始化 CLOUD_UPLOAD_MODE=copy 和 CLOUD_UPLOAD_CONCURRENCY=5，不覆盖已有设置，不改写历史上传台账。

文件列表与实际调度共用排序规则：按固定入队时间升序，相同时间按本地完整路径升序，再按任务标识稳定排序。扫描逐项记录入队时间；开始、暂停、重试和重启不会重置该时间。旧的未完成任务缺少入队时间时，在继续处理前固定其原更新时间作为兼容依据，不根据本次执行时间移动行位置。已完成历史数据不回填；被占用、暂停或失败用尽重试次数的任务可以跳过，不代表倒序执行。

新任务直接使用最终文件名上传，不创建 `.bytemuse-go-tmp` 文件、不执行重命名。115 覆盖前持久化旧文件 ID，上传后验证新文件的 ID、大小和 SHA1，再删除仍与快照一致的旧文件；失败保留旧文件，重试先回查已上传结果。覆盖过程中可能短暂存在两个同名文件。旧临时文件不自动清理，存在备份的历史任务保留恢复记录。CloudDrive2 新建与保留两者也使用最终名称；其路径型接口无法保证保留旧文件的安全覆盖，遇到不同内容的同名文件时停止并提示选择保留两者，不先删除旧文件。

在「设置 → 网盘 → 网盘上传」添加本地目录与 115 / CloudDrive2 目录映射，选择同名文件「跳过 / 覆盖 / 保留两者」后保存。默认关闭监控、默认跳过同名文件。每条目录映射末尾、删除按钮左侧的上传图标按已保存映射立即扫描并上传，不自动保存草稿，也不改变监控开关；监控关闭时仍完成本次扫描发现且尚未入账的文件。开启监控后还会持续处理新增或变化的常规文件，保留子目录结构；不会上传符号链接。容器部署需将上传源挂载到容器，目录选择器展示服务主机可访问的目录。

后台使用文件系统事件与每 30 秒目录对账，按入队时间正序上传，不再按入队时间统一等待 5 秒。Windows 取得排他写入保护后立即执行；Linux/其他 Unix 未发现写入者也不能证明空闲，使用至少间隔 2 秒的连续大小、修改时间采样辅助判断，任一变化或发现占用都会重置观察。观察期跳过该文件，先执行后续任务，不计入失败重试；观察结果只存当前进程，重启后重新采样。文件占用时显示「该文件正在被占用」，不确定时显示观察原因，普通排队显示「等待中」。校验前、校验后及上传后再次核对源文件版本；稳定采样不是绝对安全保证，Linux 容器外不可见进程和不遵守锁的新写入仍无法完全排除，生产端建议完成复制后原子改名。普通失败立即重试，累计 3 次失败后停止自动尝试；首次失败即显示原因，可单项重新上传或点击「全部重试」。账号或权限错误仍按 15 分钟间隔检查。每个后续任务结束及空闲轮询时重新检查占用；复制产生的新版本会重新入队。115 使用已绑定账号的 OpenAPI 与 OSS 分片上传；CD2 先接收本地文件，再等待其云端传输完成。115 明确返回目录已删除时按保存的绝对路径重建，不把限流视为删除。保留两者自动追加编号，并持久化目标名，防止重试反复创建副本。

底部「目录列表」只显示已有上传任务的目录，包括已添加排队、进行中、失败或暂停，以及成功（含跳过）；仅保存映射且尚未发现文件时不显示。有任务时显示文件夹大小、上传进度、速度及成功/总个数，成功数量包含跳过。「文件列表」按服务端分页显示本地路径、网盘路径、文件大小、状态、进度和速度，并按加入队列的时间正序排列。网盘路径由映射目录、相对路径和最终保留文件名组成。两个列表均可按全部、进行中、等待中、已暂停、已停止、失败和成功筛选；跳过归入成功，源文件变化归入失败，不再单独筛选；目录在包含该状态的文件时显示，统计仍覆盖该目录全部文件。文件筛选先过滤再分页。行内只显示当前状态允许的暂停、停止、开始或删除，例如进行中不显示开始。目录操作作用于该目录下的全部文件任务，不影响其他目录；这些操作不改变全局监控开关。暂停包含等待传输和已上传待替换的文件。监控关闭时，开始只运行所选文件或所选目录下的文件。等待中的文件按首次入队时间从早到晚开始；正在替换或清理的任务仍优先收尾。数据每两秒刷新，CD2 的传送与云端上传分阶段显示。文件校验按已读取并完成校验计算的字节显示进度、已校验大小和速度，阶段结束前不超过 99.9%。115 初始化复用上传前 SHA1，仅另读 128 KiB 预校验及服务端要求的签名区间。移动模式删除源文件前检查身份、大小和修改时间：Windows 在本次计算 SHA1 至删除期间持续持有禁止写入及替换的句柄时复用该次 SHA1；NAS/Linux 的锁仅具建议性，以及重试或重启恢复时，仍进行本地内容复核，显示为「已上传，待清理」，不再次显示上传前「校验中」。复核不调用 115 API；发现源文件变化时保留本地与远端文件，立即停止自动重试，人工核对后可手动重试。文件和目录列表显示添加时间与完成时间；目录完成时间仅在其文件全部成功或跳过后显示最晚完成时间，旧的已完成记录缺少完成时间时显示最后更新时间。顶部「清空已完成」和「清空错误」只处理当前列表：目录列表仅移除整行都已完成或都已失败的目录，文件列表按单行移除，保留去重记录且不删除网盘或本地文件。普通失败和远端身份暂不能确认时最多自动尝试3次，未用尽显示「处理中 (已失败次数/3)」，用尽后变为失败并显示重新上传；重新上传会重新获得3次自动尝试。账号或权限阻塞不计入该上限。目录选择里正在上传的目录不能删除。关闭监控保留当前进程的列表快照；重新开启或重启会扫描重建列表，并从持久化记录恢复已处理状态。删除本地文件不会删除网盘文件。

启用 115 事件同步后，本程序上传核验成功会直接复用文件 ID、PickCode 和祖先路径生成 STRM；已开启下载的字幕、NFO、图片从原上传文件复制，核对大小、修改时间和 SHA1 后原子落盘，不再取 115 下载直链。过滤、命名、冲突与文件归属沿用 STRM 规则。新增迁移 48 的 `upload_postprocess` 独立保存不可变上传回执及后处理状态，不让事件或 STRM 流程回写 `cloud_upload_records`。后处理失败保留待处理回执和目录缓存。已有回执的 copy/move 任务先只重试本地处理，不重复上传或进入远端替换清理；普通本地错误最多自动尝试三次，附件内容冲突首次失败即停止自动重试，处理冲突后可手动重试。move 本地后处理成功后，仅重新核验已安装的远端文件身份，再删除未改变的源文件。move 模式等本地后处理成功后才删除经核验的源文件；源与附件输出相同则报错保留。

本地上传工作进行期间暂缓事件轮询；回执完成后，只对账号、文件 ID、父目录、事件时间及生成规则均匹配的上传事件跳过详情查询。移动、重命名、删除、外部上传、旧任务及无法关联的事件继续远端核验；目录事件和补偿扫描仍可能调用列表接口。手动全量扫描仍按远端目录工作。本地回执优化减少上传后的重复请求；上传控制接口同时遵守上述共享节流与冷却恢复策略，仍不能保证不会触发 115 风控。

迁移 39 仅新增 cloud_upload_records 和三项默认配置；迁移 48 只新增后处理表和索引，两者均不回填或修改媒体历史数据。上传台账记录文件版本、冲突策略及提交阶段，不保存网盘凭据。后处理回执独立于旧版 JSON 写入，回退旧版不会因重新保存上传快照而擦除回执，但旧版不会消费新后处理任务。回退功能时关闭 CLOUD_UPLOAD_ENABLE，保留台账；迁移前沿用部署数据库备份流程。

## 网页内升级

点击顶栏版本号打开更新弹窗。本地版本立即显示，后台检查不阻塞页面；手动点击「立即检查更新」会绕过服务端缓存。有更新时可点击「立即升级」，关闭弹窗不会取消已受理的任务。

升级复用服务端有效期内的版本检查结果，避免刚检查成功又因重复请求失败而中止；成功缓存1小时、失败缓存1分钟，过期后重新查询，目标仍须匹配且高于当前版本。有更新时也保留「重新检查更新」入口，新检查结果替代旧升级错误；检查失败显示具体原因，不再统一显示版本变化。SHA256、运行包版本及启动协议校验保持不变。

升级步骤依次为「下载文件、解压文件、正在升级、正在重启」。进度条独立显示当前步骤的 0–100%，切换步骤归零：下载按响应字节数、解压按压缩包读取字节数、安装按已完成操作推进；阶段校验完成才进入下一步。获取摘要、文件大小未知或等待重启就绪时显示「处理中」，不估算百分比；下载回退重试会重新计数。步骤和进度只在升级期间显示。

有更新时，弹窗在状态区域按编号展示当前版本之后至最新版本的提交说明，跨多个版本时按版本升序汇总；无更新仍显示「当前已是最新版本」。说明由发布脚本从完整 Git 历史中的构建提交标题生成，写入 version.json，与版本检查共用请求及回退；纯文档和版本回写提交不计入说明。旧版本文件缺少说明时保留新版本提示，不影响升级。

版本检查依次尝试 raw.githubusercontent.com 直连、jsDelivr GitHub CDN 直连、使用设置页 PROXY 代理访问 raw；每个来源最多等待 10 秒，成功即停止。运行包下载依次尝试 GitHub Release 直连、已配置代理，不使用不支持 Release 附件的 jsDelivr。每轮完整下载最多 4 分钟，失败后清理部分文件并重新下载、校验 SHA256。没有配置代理时跳过代理阶段；保存代理设置后下一次回退立即生效。直连阶段不会隐式使用环境代理。CDN 可能存在缓存延迟。

新镜像默认使用 `bytemuse supervise` 管理单个 `serve` 子进程。首次启用此能力需要更新一次镜像；之后网页内升级下载 GitHub Release 的对应架构运行包，核验 SHA256、版本和启动协议，前后端一起切换到 `/data/upgrades` 下的独立版本目录，只重启内部服务，保留同一个容器。无需 Docker socket、宿主机升级服务或 root 权限。必须持久化挂载 `/data`，并保持该目录可由应用用户写入。

升级包在镜像构建成功后发布；源码版本已更新而升级包尚未就绪时，下载会失败并保留旧服务，可稍后重试。服务恢复并通过就绪检查后页面自动刷新。容器重启继续运行已升级版本；更换为更高版本镜像时优先运行新镜像内版本。启动器协议不兼容时需更新镜像。

新版本沿用既有数据库迁移入口，升级前使用部署环境现有备份流程。新服务启动失败不会自动降级或回滚数据库，避免旧程序访问已升级的数据结构；此时检查容器日志并修复或恢复完整备份。镜像标签代表基础镜像，网页版本号代表当前实际运行程序，两者在网页升级后可能不同。
