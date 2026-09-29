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

影片表通过迁移 15 增加 video_type（VARCHAR(32)，可空，默认 NULL）：censored 有码、uncensored 无码、uncensored_cracked 无码破解、leaked 流出；NULL 表示未分类。历史数据不推断回填，不改变原订阅或媒体库状态。“所有影片”支持服务端 video_type 筛选（unknown 查询未分类，省略不限），与搜索及其他状态组合，先过滤再分页。当前新采集的 JavDB 有码榜影片可明确保存 censored；无可靠类型的搜索结果保持 NULL，重复采集不覆盖已有影片分类。

应用不注入演示数据或浏览器端替代数据。影片、订阅、下载、系统设置等业务数据均来自配置的数据库；
如需初始化内容，请通过正式导入命令或业务接口写入数据库。

### 站点元数据采集

登录后使用 `GET /api/v1/collection/sources` 查询已接入能力，以 `POST /api/v1/collection/runs` 显式采集一页：

```json
{"source":"netflav","kind":"search","query":"待查询番号","page":1}
```

- Netflav：`search` 按关键词查询，`detail` 的 query 为搜索结果中的 source_id；已通过真实搜索和详情核验。演员优先使用日文原名，避免多语言别名重复建档。
- JavDB：`search`、`detail`、`rank`（period 为 daily/weekly/monthly，有码榜）；详情读取显式番号、发行日期、时长、演员和标签。
- 2026-09-28 接入边界：按用户“不可用则不添加”的要求，JavLibrary、AVBase、JavBus、Jable、SupJav 因后台请求 source_blocked 暂不加入来源目录，不能提交采集任务；源码中只保留解析器及离线回归测试，不代表已接通。Avgle 主页超时、API HTTP 520，不添加；ThisAV 明确排除。
- JavDB 保留此前已接入的搜索、榜单及本轮详情解析；当前后台访问仍可能 source_blocked，不宣称实时采集通过。Netflav 后台搜索和详情已实测通过。来源目录不是实时健康检测；已启用源站后续访问失败必须明确报错，不能当作空数据。

提交后返回 HTTP 202 和 run_id，仅代表任务已持久化；用 GET /api/v1/collection/runs/{runId} 查询进度。榜单从第 1 页自动采到末页，不设固定 100 页上限；search 仍按指定页处理（1..100），detail 仅第 1 页，省略或 0 视为 1。每个页面、视频、翻译任务分别最多执行 90 秒。不会下载视频、判断订阅或触发下载。

页面解析后把视频逐个登记到数据库队列。每个视频单独事务保存，失败不回滚其他视频。来源快照按 source/source_id 幂等更新；有可靠番号才新增影片，新演员进入演员列表。标签临时保存在 legacy_media_metadata.genres：仅对本次采集按番号命中的影片追加去重标签，保留已有文本和顺序，空标签不清空原数据；来源快照保留完整 tags 数组。其他已有影片元数据、订阅状态、媒体库状态和演员订阅日期不变；仅对本次采集涉及且缺少译文的影片登记翻译任务，不回填全部历史库。翻译调用在事务外执行，完成时只填写仍为空的译文，不覆盖人工译文；翻译禁用时不创建新翻译任务。页面读取不再触发同步翻译。

标签无需全库匹配，也不会自动遍历影片补全。Netflav 当前搜索页不提供 tags，需对选定的少量 source_id 提交 detail 采集才有标签；不自动展开搜索结果的所有详情。JavDB 当前列表适配器不提供标签。不做跨语言标签别名合并。

标签页面提供订阅中/全部标签、名称搜索、七类标签及未分类筛选，使用 GET /api/v1/tags 的 subscription/category/search 与服务端分页。迁移 20 新增源站标签分类字典、标签订阅规则和影片处理台账，不修改历史影片；无关联影片的源站标签也可订阅。PUT /tags/{tagName}/subscription 创建或编辑 limit_date（含起始日），保存后匹配本地目录，后续 TAG_SCHEDULE_TIME 定时追新；DELETE 幂等取消标签规则，保留影片订阅和下载历史。追新精确匹配完整标签，未知发行日期不匹配，已入库/已有活动订阅/已提交或未知下载记录不重复建单，处理台账防止恢复用户手动取消的影片订阅。新增订阅复用 strict 规则，下载仍由现有流程处理。标题跳转 /search?tag=名称，GET /complex/search?tag=名称 按标签精确检索，tag 非空时优先于 q。

迁移 14 新增 collection_runs（批次）、collection_work（阶段队列及各批次视频记录）、collection_pages（页面指纹）、collection_rank_heads（榜单发布指针）。均为正式持久化表，不自动清理批次；任务重启恢复，领取使用 2 分钟租约和令牌防止旧消费者提交。单进程有分页、入库、翻译三个消费者，各阶段独立串行；SQLite 写入按连接串行，不承诺并行写事务。失败最多 3 次（重试间隔 5 秒、10 秒），最终失败保留任务并由状态接口显示；需重新提交批次重新采集，不静默无限重试。

队列表字段均非空：标识与状态为 VARCHAR，计数为 INTEGER，毫秒时间与版本号为 BIGINT，规范化资料与计数 JSON 为 TEXT（MySQL 队列 payload 为 LONGTEXT）；计数及租约默认 0，错误码和令牌默认空串，其余字段显式写入。空错误表示没有错误，空 code 表示未知番号；不保存原始页面、凭据或视频流。

迁移 13 新增 collection_records 表，不回填历史库。来源与站内 ID 为联合主键，code 空串表示未知番号，payload_json 保存规范化资料，collected_at 记录 UTC 采集时间；字段均不可为空。SQLite/PostgreSQL 使用 TEXT 资料列，MySQL 同样使用 TEXT（单条最多 65535 字节），时间列按数据库分别为 TEXT/TIMESTAMPTZ/DATETIME(6)。回退程序无需删表；不要手工删除迁移记录。

“同步榜单”的定时触发和任务页立即执行均将已接入的 JavDB 有码日、周、月榜全部分别入队，不需要填写 RANK_TYPE。RANK_TYPE 是旧自动订阅筛选配置，不参与采集；现存配置值保留。RANK_SCHEDULE_TIME 仍控制定时任务注册与执行时间。这里的全部仅指已实现的三个周期，不代表已接入源站其他分类、专题榜、JavLibrary 或厂牌榜；直接调用 POST /collection/runs 仍指定单个来源与周期。

当前榜单只在所有页面采完、视频全部保存成功且结果非空时短事务切换，旧影片、订阅和下载状态不删除；视频已保存成功不依赖整榜成功，翻译失败不阻止榜单发布。空结果、分页失败和坏视频保留旧榜，重复页面判为异常；旧批次不能覆盖较新批次。批次状态分别报告采集页数、入库成功/失败/待处理、翻译状态及榜单是否曾发布。每次记录来源、周期、新旧来源数与实际新增数。其他未接入调度保持原状。PROXY、BYPASS 和翻译配置在启动时读取，修改后需重启；不使用 JAVDB_HOST 替换来源。

爬虫增强：识别 CF 验证响应（包括 HTTP 200 验证页）后最多增强一次，返回 HTML 再经错误分类及站点解析。FlareSolverr 使用 POST /v1，地址可填服务根地址或完整 /v1；ByPass（协议键 cloudflare_bypass_for_scraping）使用 GET /html?url=。下方“是否使用代理”（BYPASS_USE_PROXY）默认关闭，开启且 PROXY 非空时将代理传给远程浏览器；增强选择“不使用”时强制关闭且禁用开关，再次启用增强不自动打开代理。连接增强服务本身仍直连。FlareSolverr 普通代理使用 proxy.url，带凭据代理使用独占会话并在结束或失败时销毁；ByPass 使用 proxy 查询参数，应避免服务访问日志记录含凭据的 URL。代理地址必须能从增强服务所在主机访问，localhost 不会自动替换。Scrapling 的 HTTP 封装协议尚未确认，选用时返回 bypass_config_invalid。增强阶段上限 55 秒，FlareSolverr 求解上限 45 秒，独立会话清理最多额外 5 秒；抓取仍受队列 90 秒约束。相关配置在启动时读取，保存后需重启生效。

错误区分：source_cookie_required 表示明确登录要求；source_interactive_verification 表示源站要求人工年龄/驾驶验证；source_blocked 表示普通拒绝或验证未通过；bypass_unavailable 表示增强服务未知失败或返回无效页面；bypass_timeout 表示增强等待超时；bypass_captcha_required 表示增强明确要求人工验证码；bypass_proxy_failed 表示浏览器代理连接或认证失败；bypass_browser_failed 表示远程浏览器启动失败或崩溃；bypass_session_failed 表示远程会话失效；bypass_target_unavailable 表示浏览器访问目标站点的网络错误；bypass_config_invalid 表示增强地址或协议配置不可用。普通 403 和 429 不自动增强，导航栏登录链接不判为登录要求。增强返回 Cookie 不保存、不输出；未自动获取或使用用户登录 Cookie。JavDB、JavLibrary、AVBase、Jable、SupJav 已通过带代理增强实测解析，JavBus 返回源站年龄验证页，需要人工交互；未注册新来源。

常规回归：在 backend 运行 `go test ./...`。真实外站核验需显式设置 `BYTEMUSE_LIVE_COLLECTION=1` 后运行 `go test ./internal/platform/collector -run TestLiveCollection -v`；该测试不保存真实数据，受站点网络与访问策略影响，失败不能当作解析测试通过。

## 开发协作规范

新对话与执行 Agent 从 [AGENTS.md](AGENTS.md) 开始，按任务读取 docs/agents 下的专项规范；历史证据独立存放，不作为现行指令。前端新增 `npm run check:standards`，检查筛选栅格及规范入口，`npm run build` 会先自动执行该检查；`npm test` 同样包含规范测试。该检查不替代浏览器验收，尚未配置远端 CI 合并保护。

## 开发

下载任务页可按下载中、暂停、下载失败、下载完成以及加入时间、下载完成时间筛选，均由服务端先过滤再分页。传输状态和时间来自 qBittorrent 只读轮询；下载失败也包含搜索或提交阶段失败的任务。历史和未同步任务的传输字段保持空值，不回填或推测。日期范围按本地自然日选择，接口使用 UTC RFC3339 的含起点、不含终点时间。

订阅页的“搜索下载”会通过 `POST /api/v1/subscriptions/{subscriptionId}/download` 持久化任务，重复请求返回同一任务 ID；后台按 `DOWNLOAD_SCHEDULE_TIME` 批量入队，并定期处理。BT 来源为 Nyaa BT（sukebei.nyaa.si）；PT 来源包括使用存取令牌的 M-Team、支持访问令牌或 Cookie 的 PTFans（9KG）、NicePT，以及支持个人 API Key 或 Cookie 的 RousiPro（9KG）。PTTime 使用 Cookie 搜索 9KG 并取种，不使用有限 RSS，历史 PassKey/UID 仅保留不参与运行。三站密钥模式不会回退使用隐藏的 Cookie；PTFans/NicePT 令牌需要种子列表与详情权限，RousiPro 需要读取与搜索种子、下载种子权限。各站按番号、订阅筛选与排序规则选择资源。当前仅 qBittorrent 提交已实现：BT 提交磁力链接，PT 上传私有种子文件。任务的 `submitted` 表示下载器回查已接收，不代表文件下载完成或媒体入库；不确定结果保留为 `unknown`，不自动重复提交。相关设置在后续处理批次读取；当前不会回填或批量修改历史下载记录。Transmission、aria2、迅雷下载提交尚未接入。站点联调只验证搜索与种子字节，不代表真实 qB 端到端提交已经验收。

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

设置页 Agent 表单底部提供‘测试 OpenAI’：使用当前未保存的接口地址、模型与 API Key 发起简短对话，无需启用 Agent。测试不保存设置，成功/失败通过 Message 显示，例如 OpenAI 连接成功 (892ms)，数字为实际耗时；失败时追加脱敏原因，例如 OpenAI 连接失败 (892ms)：鉴权失败（HTTP 401），请检查 API Key 和模型权限；后端最多等待 30 秒。接口为 POST /api/v1/system/settings/openai/test，要求登录。
