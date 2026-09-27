# ByteMuse Go 实施基线

## 运行形态

- 单个 `bytemuse` 容器同时提供 React 静态资源、REST API、SSE、MCP 和后台调度。
- `bytemuse serve` 是唯一常驻命令；启动时先执行幂等数据库迁移，再启动 HTTP 与调度器。
- `bytemuse migrate status|up` 仅用于排障或手工维护，不单独部署 worker。
- 不实现旧数据库导入器，不读取或修改旧 `lady.db`。
- 首次开发/演示环境可显式运行 seed，写入两条脱敏影片演示记录；生产默认不 seed。

## 技术栈

- 前端：React 19、TypeScript、Vite、React Router、Arco Design、TanStack Query、Zustand、Vitest、Playwright。
- 后端：Go 1.27、chi、OpenAPI 3.1、oapi-codegen、Bun ORM、Goose、robfig/cron、slog。
- 数据库：SQLite、PostgreSQL 16+、MySQL 8.0+；统一仓储和业务规则，各方言显式迁移。

## 业务模块

`auth`、`catalog`、`discovery`、`subscription`、`torrent`、`download`、`library`、`notification`、`integration`、`scheduler`、`translation`、`agent`、`system`。

HTTP、调度任务、MCP 与 Agent 工具必须调用同一应用服务，不得复制业务规则。影片订阅状态、下载任务状态、媒体库存在状态独立存储和返回。

## 数据与接口约束

- 主键采用 ULID 字符串；时间统一 UTC，API 使用 RFC 3339。
- 容量使用字节整数；枚举使用受控字符串。
- SQLite 启用 WAL、外键和 busy timeout；PostgreSQL/MySQL 使用连接池。
- REST 主路径 `/api/v1`，健康检查 `/health/live` 与 `/health/ready`。
- OpenAPI 是前后端接口唯一权威来源。
- 敏感配置不写日志、响应、演示数据或仓库。

## 目录所有权

- 前端 agent：仅写 `frontend/`。
- 后端 agent：写 `backend/`，但不写 `backend/internal/platform/database/migrations/` 和数据库仓储实现。
- 数据库 agent：仅写 `backend/internal/platform/database/` 及其测试。
- 主 agent：维护根配置、`api/`、`deploy/`、共享文档和最终集成。

## 验收

- `go test ./...`、前端单元测试、类型检查和构建通过。
- 同一仓储契约至少在 SQLite 实测；PostgreSQL/MySQL 通过容器矩阵或明确标注环境未验证。
- 单镜像能启动，API 与调度器共享数据库且不会重复启动相同任务。
- 演示 seed 只生成两条影片，不生成用户、凭据、下载历史或外部配置。
