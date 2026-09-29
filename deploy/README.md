# 单容器部署

三个 Compose 文件都只启动一个 ByteMuse 应用容器。`serve` 在同一进程中提供 Web/API 并运行后台调度器。

- `compose.sqlite.yaml`：数据库文件保存在命名卷，适合 NAS 单机部署。
- `compose.postgres.yaml`：通过 `DATABASE_DSN` 连接已有 PostgreSQL。
- `compose.mysql.yaml`：通过 `DATABASE_DSN` 连接已有 MySQL 8.0+。

启动前至少设置 32 字节随机 `SESSION_SECRET`。PostgreSQL/MySQL 的 DSN 只通过部署环境传入，不写入仓库。

## 运行时约定

- 定时任务：调度器在 `serve` 进程内运行，Cron 表达式按容器本地时间解释，设置页默认排期为 20:00、21:00、21:30、22:00。镜像固定 `TZ=Asia/Shanghai` 并自带时区数据，Compose 可用 `TZ` 覆盖；时区与使用方不一致时排期会整体偏移，日志清理（`0 0 * * *`）同样受影响。
- 镜像来源：Compose 使用 `${BYTEMUSE_IMAGE:-bytemuse-go:local}`。本机构建时保持默认值；使用 CI 推送的镜像时设置 `BYTEMUSE_IMAGE` 后执行 `docker compose -f deploy/compose.sqlite.yaml pull`。
- 运行版本：镜像通过构建参数 `BYTEMUSE_VERSION` 注入版本，未注入时为 `dev`。
- 数据目录：`/data` 是唯一需要持久化的可写卷。容器以非 root 用户 `nonroot`（UID/GID 65532）运行，绑定宿主机目录前需保证该 UID 可写，例如 `chown -R 65532:65532 /your/host/data`。
- 健康检查：镜像内置 `HEALTHCHECK` 调用 `/app/bytemuse doctor`，Compose 使用相同命令；数据库不可用时容器状态为 `unhealthy`。

镜像由 `deploy/Dockerfile` 多阶段构建，最终运行层只包含 `/app/bytemuse`、`/app/web`、时区数据和可写数据目录，不包含 Go/TypeScript 源码、测试或构建工具。容器默认执行 `bytemuse serve`，健康检查调用同一二进制的 `doctor` 命令。

## 镜像构建

镜像统一由 `deploy/Dockerfile` 构建，需要 BuildKit（文件头已声明 `# syntax=docker/dockerfile:1.7`）：

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  --build-arg BYTEMUSE_VERSION=<版本或提交号> \
  -f deploy/Dockerfile -t <容器仓库>/bytemuse:<版本或提交号> .
```

前端产物与架构无关，只有 Go 二进制按 `TARGETARCH` 交叉编译。标签使用明确版本或不可变提交标识，`latest` 只能作为附加标签；镜像由 CI 或本地构建后推送到容器仓库，不写回源码仓库。

部署配置静态验证：

```powershell
pwsh -NoProfile -File deploy/verify.ps1
```
