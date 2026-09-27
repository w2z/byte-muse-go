# 后端实现任务

阅读 `AGENTS.md`、`docs/agents/README.md`、`docs/agents/backend.md` 和 `docs/architecture/implementation-baseline.md`。

在 `backend/` 建立 Go 模块化单体骨架：`bytemuse serve` 同时启动 HTTP、静态资源占位与后台调度；保留 `migrate status|up` 维护命令。实现配置、日志、优雅关闭、健康检查、影片列表/详情、订阅创建/取消、下载任务列表、系统状态的应用服务与 HTTP handler。数据库访问只能依赖 `internal/ports` 仓储接口，不写具体 SQL 和迁移。

必须先写测试并验证失败，再实现；覆盖健康检查、影片分页、订阅幂等、取消、调度器单实例启动和优雅关闭。不得写数据库迁移目录，不得修改 `backend/` 以外文件。
