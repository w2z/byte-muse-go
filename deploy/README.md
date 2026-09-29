# 单容器部署

三个 Compose 文件都以 `byte-muse` 服务启动 ByteMuse 应用容器，并附带 `cloudflarebypass` 抓取增强服务；`serve` 在同一进程中提供 Web/API 并运行后台调度器。

- `compose.sqlite.yaml`：数据库文件保存在宿主机数据目录（模板占位 `/path/to/byte-muse/data`），适合 NAS 单机部署。
- `compose.postgres.yaml`：内置 `postgres` 服务（`postgres:17-alpine`），应用通过 `DATABASE_DSN` 连接它；改用外部 PostgreSQL 时替换 DSN 的主机、端口与密码。
- `compose.mysql.yaml`：内置 `mysql` 服务（`mysql:8.4`），应用通过 `DATABASE_DSN` 连接它；改用外部 MySQL 8.0+ 时替换 DSN 的主机、端口与密码。

三个 Compose 文件都直接写出配置值，不使用环境变量插值。`ADMIN_USERNAME`、`ADMIN_PASSWORD`、`SESSION_SECRET` 以及 PostgreSQL/MySQL 的 `DATABASE_DSN` 都是文件里的占位值，部署前必须替换；`SESSION_SECRET` 少于 32 字节、DSN 占位主机无法解析时，服务会直接拒绝启动。真实凭据只留在部署机上，不提交回仓库。

## 运行时约定

- 容器与网络：应用容器固定命名 `byte-muse`，增强服务固定命名 `CloudFlareBypass`，两者都接入 Compose 定义的 `bridge` 网络，设置页可以直接用 `http://cloudflarebypass:8000` 访问增强服务。
- 抓取增强：设置页把 `BYPASS_ENGINE` 选为 `cloudflare_bypass_for_scraping`、`BYPASS_URL` 填 `http://cloudflarebypass:8000`；该服务只做页面增强，不保存源站凭据。
- 会话 Cookie：只有 `APP_ENV=production` 才要求 HTTPS 传输。Compose 未设置该变量，纯 HTTP 访问时可以正常登录；部署在 HTTPS 反向代理之后时再自行加上。
- 定时任务：调度器在 `serve` 进程内运行，Cron 表达式按容器本地时间解释，设置页默认排期为 20:00、21:00、21:30、22:00。镜像固定 `TZ=Asia/Shanghai` 并自带时区数据；时区与使用方不一致时排期会整体偏移，日志清理（`0 0 * * *`）同样受影响。
- 镜像来源：Compose 直接写明 `image: ghcr.io/w2z/byte-muse-go:latest`，`docker compose up -d` 拉取已发布镜像；本地构建用 `docker build -f deploy/Dockerfile -t bytemuse-go:local .`，不由 Compose 构建。
- 运行版本：镜像通过构建参数 `BYTEMUSE_VERSION` 注入版本，未注入时为 `dev`。
- 数据目录：只挂载 `/data` 与 `/strm`；镜像里的 `/app` 存放服务二进制与前端产物，挂载覆盖后容器无法启动。容器以非 root 用户 `nonroot`（UID/GID 65532）运行，绑定宿主机目录前需保证该 UID 可写，例如 `chown -R 65532:65532 /path/to/byte-muse/data /path/to/byte-muse/strm`。
- 内置数据库：`postgres`（容器内 UID 70）与 `mysql`（容器内 UID 999）分别需要 `pgdata`（`/var/lib/postgresql/data`）和 `mysqldata`（`/var/lib/mysql`）可写；应用用 `depends_on: condition: service_healthy` 等数据库健康后再启动，避免迁移连不上反复重启。数据库不对宿主机发布端口，只在 Compose 网络内供应用访问。
- 仓库内不留内网信息：三个 Compose 文件里的代理地址与宿主机路径都是占位符，真实拓扑放在部署机的本地未跟踪文件 `deploy/compose.<方言>.local.yaml`（已在 `.gitignore` 中忽略），避免公开仓库泄露内网地址。
- 健康检查：镜像内置 `HEALTHCHECK` 调用 `/app/bytemuse doctor`，Compose 不重复定义；数据库不可用时容器状态为 `unhealthy`。

镜像由 `deploy/Dockerfile` 多阶段构建，最终运行层只包含 `/app/bytemuse`、`/app/web`、时区数据和可写数据目录，不包含 Go/TypeScript 源码、测试或构建工具。容器默认执行 `bytemuse serve`，健康检查调用同一二进制的 `doctor` 命令。

## 镜像构建

镜像统一由 `deploy/Dockerfile` 构建，需要 BuildKit（文件头已声明 `# syntax=docker/dockerfile:1.7`）：

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  --build-arg BYTEMUSE_VERSION=<版本或提交号> \
  -f deploy/Dockerfile -t <容器仓库>/bytemuse:<版本或提交号> .
```

前端产物与架构无关，只有 Go 二进制按 `TARGETARCH` 交叉编译。标签使用明确版本或不可变提交标识，`latest` 只能作为附加标签；镜像由 CI 或本地构建后推送到容器仓库，不写回源码仓库。

自动构建：GitHub Actions 只在 `backend/`、`frontend/`、`deploy/Dockerfile`、`.dockerignore` 变更时构建镜像；只改 `README.md`、`version.json`、`AGENTS.md`、`docs/`、`api/` 契约、Compose 与本地脚本时不会触发构建，`latest` 保持上一次代码构建的版本。

部署配置静态验证：

```powershell
pwsh -NoProfile -File deploy/verify.ps1
```

## 版本记录

`version.json` 是发布版本记录，由 `deploy/version.ps1` 在代码提交后写入，构建流程只读取和校验，不回写仓库：

```powershell
pwsh -NoProfile -File deploy/version.ps1
```

- 版本号规则为 `0.1.<提交计数>`，计数排除只修改 `version.json` 的提交，因此同一次代码提交对应唯一版本，发布提交本身不改变版本号。
- 只有本次提交包含构建输入（`backend/`、`frontend/`、`deploy/Dockerfile`、`.dockerignore`）时才更新；文档、规范、Compose 等提交不写版本记录。
- 提交后自动执行：每个克隆执行一次 `git config core.hooksPath .githooks` 启用 `.githooks/post-commit` 钩子。
- 镜像摘要不在记录内：摘要只能在构建完成后得知，需要时用 `docker buildx imagetools inspect ghcr.io/w2z/byte-muse-go:<版本>` 查询。
- 推送代码前缺少版本记录时，workflow 会直接失败并提示，不会用旧版本号覆盖已有镜像标签。
