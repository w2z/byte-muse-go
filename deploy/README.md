# 单容器部署

三个 Compose 文件都只启动一个 ByteMuse 应用容器。`serve` 在同一进程中提供 Web/API 并运行后台调度器。

- `compose.sqlite.yaml`：数据库文件保存在命名卷，适合 NAS 单机部署。
- `compose.postgres.yaml`：通过 `DATABASE_DSN` 连接已有 PostgreSQL。
- `compose.mysql.yaml`：通过 `DATABASE_DSN` 连接已有 MySQL 8.0+。

启动前至少设置 32 字节随机 `SESSION_SECRET`。PostgreSQL/MySQL 的 DSN 只通过部署环境传入，不写入仓库。

镜像由 `deploy/Dockerfile` 多阶段构建，最终运行层只包含 `/app/bytemuse`、`/app/web` 和可写数据目录，不包含 Go/TypeScript 源码、测试或构建工具。容器默认执行 `bytemuse serve`，健康检查调用同一二进制的 `doctor` 命令。

部署配置静态验证：

```powershell
pwsh -NoProfile -File deploy/verify.ps1
```
