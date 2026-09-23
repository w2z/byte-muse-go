# ByteMuse Go

ByteMuse 的 Go 重建版本。一个容器同时提供 Web UI、REST API、SSE、MCP 和后台调度，支持 SQLite、PostgreSQL 与 MySQL。

## 运行形态

生产环境只需部署一个 `bytemuse` 应用容器：

```text
bytemuse serve
├── React 静态站点
├── /api/v1 REST API
├── /events SSE
├── /health/*
└── 后台调度器
```

容器启动时先执行幂等数据库迁移，再启动服务。`migrate status|up` 仅作为人工排障入口，不需要额外 worker 容器。

## 数据库

通过环境变量选择数据库：

```dotenv
DATABASE_DRIVER=sqlite
DATABASE_DSN=file:/data/bytemuse.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)
```

可选值为 `sqlite`、`postgres`、`mysql`。对应的 Compose 示例位于 `deploy/`。SQLite 是默认单机部署方案；PostgreSQL/MySQL 数据库由用户已有服务或同一 Compose 项目提供，但 ByteMuse 本身仍只有一个应用容器。

## 演示数据

不导入旧 `lady.db`。开发环境可设置：

```dotenv
DEMO_SEED_ENABLED=true
```

该选项仅幂等写入两条脱敏影片演示记录，不创建用户、凭据、下载历史或第三方配置。生产默认关闭。

## 开发

```powershell
cd frontend
npm install
npm test
npm run build
# 开发服务器默认请求本地 Go API 127.0.0.1:3750；仅需纯前端演示时设置 VITE_ENABLE_MOCK=true
# 联调隔离实例可用 VITE_API_PROXY_TARGET=http://127.0.0.1:3761 指定后端
npm run dev

cd ..\backend
go test ./...
go run ./cmd/bytemuse serve
```

API 契约位于 `api/openapi.yaml`，架构约束位于 `docs/architecture/implementation-baseline.md`。`未加密代码/` 只用于参照，不参与新应用构建和运行。

