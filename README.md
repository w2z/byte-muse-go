# ByteMuse Go

ByteMuse 的 Go 重建版本。一个容器同时提供 Web UI、REST API 和后台调度，支持 SQLite、PostgreSQL 与 MySQL。

## 运行形态

生产环境只需部署一个 `bytemuse` 应用容器：

```text
bytemuse serve
├── React 静态站点
├── /api/v1 REST API
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

## 数据来源

应用不注入演示数据或浏览器端替代数据。影片、订阅、下载、系统设置等业务数据均来自配置的数据库；
如需初始化内容，请通过正式导入命令或业务接口写入数据库。

## 开发

```powershell
cd frontend
npm install
npm run typecheck
npm run build
# 开发服务器和生产构建均只请求真实 Go API；请先启动本地 Go 服务（默认 127.0.0.1:3750）
# 联调隔离实例可用 VITE_API_PROXY_TARGET=http://127.0.0.1:3761 指定后端
npm run dev

cd ..\backend
go build ./...
go run ./cmd/bytemuse serve
```

API 契约位于 `api/openapi.yaml`，架构约束位于 `docs/architecture/implementation-baseline.md`。`未加密代码/` 只用于参照，不参与新应用构建和运行。
