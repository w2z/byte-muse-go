# 前端实现任务

阅读 `AGENTS.md`、`docs/agents/README.md`、`docs/agents/frontend.md` 和 `docs/architecture/implementation-baseline.md`。

在 `frontend/` 内建立 React 19 + TypeScript + Vite + Arco Design 应用。只实现可运行的第一阶段骨架和真实 API 调用边界：应用布局、登录页、仪表盘、影片列表、订阅列表、下载任务、系统状态与设置页；使用路由懒加载、TanStack Query、统一错误处理、加载/空/失败状态和响应式布局。开发和生产均读取 Go API 的 `/api/v1`，不得注入 Mock 或假数据。

必须先写测试并验证失败，再实现；至少覆盖路由、API 错误转换、影片列表空状态和订阅操作防重复提交。输出测试、类型检查和构建结果。不得修改 `frontend/` 以外文件。
