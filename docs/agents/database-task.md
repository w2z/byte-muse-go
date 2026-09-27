# 数据库实现任务

阅读 `AGENTS.md`、`docs/agents/README.md`、`docs/agents/database.md` 和 `docs/architecture/implementation-baseline.md`。

仅在 `backend/internal/platform/database/` 实现 SQLite、PostgreSQL、MySQL 支持：连接配置、方言识别、事务、迁移运行器、三套同版本迁移、仓储实现和显式开发 seed。seed 只能生成两条脱敏影片记录，必须幂等且默认关闭。不得读取或迁移旧 `lady.db`。

必须先写仓储契约测试并验证失败，再实现。SQLite 必须实测；PostgreSQL/MySQL 使用可配置集成测试和结构一致性检查。不得修改目录外文件。
