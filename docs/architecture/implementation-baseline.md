# ByteMuse 当前实施基线

本文件保留仍有效的架构约束；Agent工作规则从根目录AGENTS.md进入。2026-09-29以当前代码修正早期骨架规划，具体版本以依赖清单为准，不将规划中的工具或接口宣称为已实现。

## 运行形态

- 一个bytemuse应用容器提供React静态资源、REST API和后台调度。镜像入口 `bytemuse supervise` 常驻并管理一个 `bytemuse serve` 子进程；升级只替换子进程，容器保持不变。`migrate status|up`用于维护；不另建重复业务worker。
- 容器内升级下载固定发布仓库的按架构运行包，校验 SHA256、完整性及启动协议后提交 `/data/upgrades` 内的原子切换记录。前后端使用同一版本目录；新服务就绪后才报告成功。旧镜像首次使用需更新镜像。新服务仍按现有入口迁移数据库，失败不自动降级或回滚数据库。
- 启动按版本执行迁移后启动HTTP与调度；数据库迁移与业务执行共享配置，但业务规则不得复制。
- 每次应用启动后异步执行一次全部已注册任务（包括未配置 Cron 的任务），仅跳过「清理系统日志」的启动执行；固定任务「同步上新」每天 03:00 通过 AVBase 日期采集入队，原有 Cron 排期不变。启动执行复用任务互斥、状态和日志，不等待任务完成才启动 HTTP，退出时取消并在关闭超时内等待启动任务收尾。
- 应用不注入演示数据。测试使用独立库或可清理夹具；旧库导入命令的存在不构成运行授权，原始lady.db只读，不自动迁移或回填。
- 本地开发前后端分开运行，Vite通过代理访问真实Go API；生产由应用提供构建后的静态资源。

## 已确认技术栈和目录

| 范围 | 当前实现及来源 |
| --- | --- |
| 前端 | React 19、TypeScript、Vite、React Router、Arco Design、TanStack Query、Zustand、Vitest；见frontend/package.json |
| 后端 | Go 1.27、chi、robfig/cron及slog；见backend/go.mod和internal/bootstrap |
| 数据库 | database/sql，SQLite/PostgreSQL/MySQL方言与驱动；自有版本迁移执行器，见internal/platform/database |
| 契约 | api/openapi.yaml，API修改需同步Apifox；不能假定存在自动生成HTTP实现 |
| 业务边界 | application服务、ports接口、platform适配、transport/httpapi传输；从实际调用核查模块能力 |

旧文档中的Bun、Goose、oapi-codegen、SSE/MCP统一交付、演示seed及“标签尚无API”等描述不作为当前事实。外部来源是否可用应查来源目录和真实运行证据，源码存在不等于已接入。

## 数据与接口

- API主要前缀 `/api/v1`，健康检查 `/health/live`、`/health/ready`。前端只通过统一请求客户端访问接口。
- 时间统一明确时区，API使用RFC3339；容量使用字节整数，受控枚举使用一致字符串。已有ID和历史数据以当前模型/契约为准，不因旧规划批量改写。
- SQLite启用WAL、外键和busy timeout；PostgreSQL/MySQL使用连接池。新库与旧版本升级均经过唯一版本迁移来源。
- 影片订阅、下载受理/完成、媒体库存在是不同状态。HTTP、调度和其他消费者复用应用服务，不各自实现状态转换。
- 日志已持久化到数据库Store；轮询接口、保留期、清理与计数以当前logging/数据库/调度实现为准。
- 敏感配置不写日志、文档或测试快照；浏览器和真实第三方操作必须处于当前授权范围。

## 实现与验收

- 前端、后端、数据库的默认写入范围分别为frontend、backend服务层及backend/internal/platform/database；跨范围由主Agent分配，契约与共享文件指定唯一负责人。
- 前端执行规范检查、相关测试、类型检查、构建及真实浏览器验收；后端执行适用test/vet/build。
- 仓储至少验证SQLite；涉及PostgreSQL/MySQL时核对方言、迁移与目标版本，未实测环境明确报告。
- 发布只交付运行产物；最终镜像启动、健康、静态资源、配置注入和迁移须真实验证。源码测试不替代镜像与第三方业务验收。

## 代码提交与镜像发布

- 主仓库为 `https://github.com/w2z/byte-muse-go`，全部开发提交、测试、迁移、规范和部署定义，以及 `README.md`、`version.json`、docker 构建相关目录与文件都由该仓库维护。
- 另有一个自建 Gitea 远端保留同一份提交历史作为同步副本；每次推送必须让两个远端保持一致，禁止只推其中一个造成历史分叉。
- `未加密代码/` 只在本地工作区保留，由 `.gitignore` 排除，不进入任何远端仓库；公开推送前必须完成敏感信息检查。
- 功能完成后由执行 Agent 按协作规范 4.9 自动生成提交信息、提交并推送，不要求用户手动提交。
- 镜像构建由 GitHub Actions 在主仓库推送后自动触发，构建最新提交并读取 version.json 中的版本；版本记录由 deploy/version.ps1 在代码提交时写入，构建流程不回写仓库；同一分支只保留一个构建任务，新提交取消仍在运行的旧构建。

## 文档整理说明

原docs/agents下的frontend-task.md、backend-task.md、database-task.md是已完成骨架阶段的一次性任务单，含不存在的引用与过期seed/边界要求。有效的真实API、统一服务/仓储、测试和职责边界已合并到根入口及专项规范，原任务单删除，不再作为新任务指令。
