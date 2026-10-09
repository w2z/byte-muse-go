# ByteMuse 项目历史证据

本文件迁移自2026-09-29整理前的根AGENTS.md，仅在追溯相关业务时按章节读取，禁止作为当前规范执行。其中阶段性状态、旧技术栈、目录限制和历史授权可能已失效，必须以当前用户授权、根入口及专项规范、当前代码和可重复验证为准。旧库统计只描述当时快照；保留异常和未验证结论，不代表当前运行状态。新增记录按日期写清范围、证据和限制，不复制现行强制规则。对标登录凭据不随本次迁移复制。

## 2026-10-09 115 事件生成与受管文件清理

- 新增迁移 42 的 strm_files 表，按本地路径摘要唯一登记账号/映射作用域、文件 ID、父目录、祖先链、相对路径与内容 SHA256；查询使用 scope 索引。只登记本次实际写入，不回填历史未知文件，旧库不删除业务数据，回退保留此表。
- 生成事件按当前文件信息定位映射；目录事件遍历其子树。删除事件类型 22 需详情不存在且父目录完整核验，目录后代另查当前文件状态；仅移除摘要匹配的受管普通文件及空目录。移动、重命名保留旧路径，超窗仅补偿生成。
- SQLite 独立临时库覆盖空库、41 升级到 42、重复迁移、路径唯一归属、账号隔离和关闭重开读取。应用测试覆盖生成、附件、目录删除、外部修改保留、失败游标重试和重启；未操作生产数据库和 NAS 文件，真实 115 删除响应与 PostgreSQL/MySQL 实例尚未联调。

## 2026-09-29 规范整理与执行检查

- 根AGENTS.md压缩为7757字节（约7.6KiB），保留显式加载入口、核心限制和Agent开工/派发/更新/交付流程；4份专项规范位于docs/agents/{workflow,frontend,backend,database}/AGENTS.md。历史审计及任务记录迁移至本文件，阶段性授权不可复用。
- 合并有效职责/测试约束后删除3份过期骨架任务单，更新现有实施基线，纠正未建立Go/React、Bun/Goose、演示seed和内存日志等过时描述；对标凭据不迁入新文档。
- 新增frontend/src/app/standards.test.ts，检查七页栅格及后续带筛选标记的页面、禁止的CSS覆盖、根入口20KiB预算和专项链接；负例验证错误列宽及跨列等能被检测。check:standards接入npm test及prebuild。源码声明检查存在明确覆盖边界，不替代浏览器验收，不保证Agent遵守所有文字要求。
- 前端11测试文件69项测试通过，npm run build含规范前置检查、TypeScript及Vite通过；未修改业务页面、API或数据库。Docker构建阶段补充规范文档输入，最终阶段不复制文档；本机Docker Linux守护进程不可用，镜像构建未实测。无远端CI或分支保护配置变更，无提交、部署或运行数据修改。

## 二、项目目标与当前状态

### 2.1 项目目标

ByteMuse 是自托管的 PT 订阅与媒体库编排工具。旧版能力包括从榜单、厂牌、演员等来源获取番号，按规则筛选资源，提交下载客户端，并与媒体库、消息通知、定时任务和管理 API 联动。

仓库名称为 byte-muse-go，但当前尚未发现可确认的 Go 新版工程或前端工程。后续重建范围、前端框架、Go 框架、数据库选型、部署环境及旧 API 兼容程度，由主 Agent 根据用户任务和项目证据统一确定，不得由各专业 Agent 各自选择。

### 2.2 初期仓库快照（已过时，仅供追溯）

- Git 仓库尚无初始提交，原始参照目录未跟踪。
- 未加密代码/最终源码/app/ 是从旧版 Docker 环境提取的 Python 3.12 源码，主要技术为 FastAPI、SQLAlchemy 2、SQLite 和 APScheduler。
- 未加密代码/最终源码/lady.db 是旧版 SQLite 数据库，核查时为 91,127,808 字节，约 86.9 MiB。2026-09-23 已完成第一轮只读结构、行数、状态分布和代码引用审计；业务语义、异常来源和迁移取舍仍需随相关功能继续核实。
- 未加密代码/git基线/bytemuse-legacy/ 含旧版构建基线；部分站点和数据模块只有面向 CPython 3.12、Linux x86_64 的 .so 文件，不能视为已取得可移植源码。
- 当前未发现 go.mod、package.json、Vite、React、Vue 工程或可确认的旧版前端源码。
- 未加密代码/ 是只读参照。除非用户明确授权，不得直接修改、迁移、格式化或清理。
- 旧版 app/settings.py 在导入时可能创建目录、复制配置模板并写入 SECRET_KEY；旧版启动还会建表、更新表结构并启动调度器。禁止为了查看效果直接运行旧版入口。
- 旧版 app/agent/ 是产品内部 AI 编排功能，与参与开发的各类 Agent 不是同一概念，讨论时必须明确区分。

### 2.3 旧版源码导航

| 路径 | 已确认职责 |
| --- | --- |
| 未加密代码/最终源码/app/api/ | FastAPI 应用、路由和接口服务，统一前缀 /api/v1 |
| 未加密代码/最终源码/app/services/ | 订阅、筛选、排序、下载、通知和推荐等核心业务 |
| 未加密代码/最终源码/app/schduler/ | 榜单、演员、下载和消息定时任务；目录名沿用旧版拼写 |
| 未加密代码/最终源码/app/database/ | SQLAlchemy 会话、模型、建表和启动时结构更新 |
| 未加密代码/最终源码/app/modules/ | 下载客户端、媒体服务器、通知、云盘、站点和第三方适配器 |
| 未加密代码/最终源码/app/agent/ | 产品自身的 OpenAI function calling 编排和工具系统 |
| 未加密代码/最终源码/app/config/、app/core/ | 配置模板、示例和配置说明 |
| 未加密代码/最终源码/lady.db | 旧版历史数据库；只读核查，迁移演练使用副本 |

### 2.4 已确认的旧版功能和接口

旧版路由挂载在 /api/v1。以下接口是参照证据，不自动等于新版必须原样兼容的契约：

| 分组 | 方法与路径 | 已确认用途 |
| --- | --- | --- |
| 订阅 | GET /dashboard、GET /ranks | 仪表盘和榜单 |
| 订阅 | POST /codes/list、POST /codes/recommend、POST /codes/release_today | 番号分页列表、推荐、今日发行 |
| 订阅 | POST /codes/sub、DELETE /codes/cancel、GET /codes/star | 订阅、取消和收藏 |
| 订阅 | GET /complex/search、POST /torrents/download/manual | 综合搜索和手动下载 |
| 订阅 | GET /codes/download/all、GET /rank/subscribe | 执行全部订阅和榜单批量订阅 |
| 演员 | GET /actors、GET /actors/rank、POST /actors/sub、DELETE /actors/cancel | 演员列表、榜单、订阅和取消 |
| 配置 | GET/POST /config、GET /version、GET /logs | 配置、版本和日志 |
| 配置 | GET /status、GET /healthy | 站点状态和模块健康检查 |
| 管理 | GET /login、GET /user/token、GET /user/update | 登录、初始化 token 和更新用户 |
| 消息 | GET/POST /message | 企业微信回调验证和消息接收 |
| 资源 | GET /image-proxy | 图片代理 |

旧版响应封装为 success、message、data。新版是否沿用由 API 契约决定，前后端不得分别猜测。

### 2.5 已确认的数据模型

旧版数据库已通过 SQLite 只读连接核查，PRAGMA quick_check 返回 ok。实际存在 7 张业务表、1 个显式二级索引，无外键和触发器：

| 表 | 行数 | 主键 | 已确认用途和注意事项 |
| --- | ---: | --- | --- |
| code | 46,874 | code | 番号元数据和订阅状态；实际 22 列，含 ORM 未完整声明的本地化字段 |
| actor | 1,432 | name | 演员元数据；47 条 limit_date 非空，表示存在订阅语义 |
| history | 7,343 | hash | 下载历史；3 条 code 找不到对应 code 记录，迁移时不能静默丢弃 |
| cache | 11,761 | id | 当前全部 namespace=rank，主要存榜单缓存 |
| user | 1 | username | 管理用户；不得读取或输出具体密码摘要和 token |
| health | 0 | id | 健康结果表为空；当前代码主要把 Health 当即时返回对象使用 |
| tag | 0 | id | ORM 导出缺少 Tag，但产品 AI 工具引用标签订阅；属于不完整功能证据 |

显式索引只有 tag.name 上的 ix_tag_name。数据库不存在声明式外键，不能据此假设实体间没有业务关联。code 实际字段除旧 ORM 主要字段外，还包括 star、cn_title、local_banner、local_still_photo：star 在当前快照全部为空；cn_title 有 4,106 条非空；local_banner 有 7,143 条非空；local_still_photo 有 6,873 条非空。

### 2.6 已确认的差异和风险

- code 模型注释只列出 UN_SUBSCRIBE、SUBSCRIBE、COMPLETE，取消接口实际还写入 CANCEL。状态必须从模型、服务、任务和真实数据共同推导。
- 部分流程在“种子成功添加到下载器”后就写入 COMPLETE，因此不能直接解释为文件下载完成或媒体库已经存在。新版必须拆清请求受理、资源选中、任务创建、下载完成、媒体入库等语义。
- STRICT 与预加载模式的筛选和状态转换不同，开发前必须阅读 find_torrent、run_sub、手动下载和调度入口。
- 旧版初始化迁移包含创建临时表、复制数据、删除旧表和改名，不得在原始数据库上直接执行。
- 旧版登录、用户更新、JWT 生命周期、密码算法和未鉴权搜索接口只是现状证据，新版不得无安全审查照搬。
- 部分功能依赖只有编译产物的模块。无法从调用方、运行证据或隔离测试确认的行为标记为未验证，不凭空补全。
- code 中有 18 条异常组合 status=STRICT、mode=UN_SUBSCRIBE；其余记录 mode 均为 STRICT。该组合很可能来自历史字段错位或旧迁移，但原因尚未验证，禁止直接交换或修正字段值。
- code.star 在数据库中声明类型为 init，疑似旧迁移把 int 拼错；当前 46,874 条均为空。开发收藏功能时必须结合接口行为和对标系统重新确认目标类型与历史兼容。
- history 有 3 条孤立 code 引用。因为数据库无外键且记录可能仍用于去重，迁移时必须保留并单独报告。
- tag 表为空且当前提取源码缺少 Tag ORM 导出、itag 服务和相关 schema，但产品 AI 工具明确调用标签查询和订阅。该功能属于“源码提取不完整”，不能将空表等同于废弃。
- health 表为空且未发现持久化写入，当前证据支持“疑似未使用的持久化表”，但健康检查功能本身仍在使用 Health 数据结构。

### 2.7 已确认的外部集成和调度

- 下载客户端：qBittorrent、Transmission、迅雷；Aria2 有独立模块，但未在统一 Module 容器中初始化，是否属于有效功能需重新核实。
- 媒体服务器：Emby、Plex、Jellyfin。
- 通知：企业微信和 Telegram；消息入口还会触发订阅和下载任务。
- 资源与数据：M-Team、PTT、NicePT、色花堂、AVBase、JavDB、Library、Brands、Shared 等，其中部分仅有编译模块。
- 其他：CloudNas 离线下载、Docker Hub 版本检查、GitHub 模块以及产品自身的 OpenAI Agent。
- 固定调度：每日 04:00 同步榜单、每日 05:00 运行新闻任务；启用时每 5 分钟监控 qBittorrent 或 Transmission。
- 配置调度：RANK_SCHEDULE_TIME、ACTOR_SCHEDULE_TIME、DOWNLOAD_SCHEDULE_TIME 使用 5 位 cron 表达式。

## 十二、项目动态记录

- MEDIA-COVER-CACHE（2026-09-30）：按用户最终确认，影片主封面改为服务端按番号持久化到 /data/cover（/data 由用户自行挂载），文件名是番号加图片真实格式，例如 sons-1223.png；不再有「图片持久化」开关，缓存固定开启，目录可用 COVER_ROOT 覆盖。新增 GET /api/v1/covers/{code}?source=...（需登录会话）：命中缓存直接返回本地文件，未命中下载后原子落盘，目录不可写、源站失败或番号不能作为文件名时 302 回退源地址，页面表现与无缓存一致；source 只接受不带凭据的 http/https 绝对地址，否则 400。同时删除死配置 JAVDB_HOST（迁移 30）与 ENABLE_PHOTO_CACHE（迁移 32），设置页移除「JAVDB API地址」「图片持久化」。设置页「外网访问地址」（EXTERNAL_DOMAIN）改为页面封面与企业微信封面推送共用的地址前缀：已配置时卡片封面与微信图文 picurl 都用 {外网访问地址}/api/v1/covers/{番号}?source=... 取同一张图，留空时页面用相对地址、微信直接用图床原图；剧照、资料图与详情封面/海报仍用源站地址。只改既有设置页、CodeCard、API client、通知服务、迁移与既有测试，新增 backend/internal/platform/covercache/ 与 transport/httpapi/covers.go 及测试、OpenAPI 与 Apifox 节点、README 挂载说明。后端 build/vet/test、前端 107 测试/类型检查/构建/规范检查通过；隔离实例核验首次请求落盘 sons-1223.png 且源站命中 1 次、二次请求源站命中数不变并返回 image/png 与 private, max-age=86400、不可达源站 302 回退、相对 source 400、匿名 401；浏览器核验设置页无两项已删配置、JAVDB榜单自动订阅选「不订阅」标题下无「请选择」、卡片封面 src 随 EXTERNAL_DOMAIN 在绝对地址与相对地址间切换，390/576/768/1280/1920px 无横向溢出。Apifox 节点的参数、响应码与中文描述已回读一致，但响应示例字段经 MCP 工具多次提交未落库；Docker 镜像与真实图床、企业微信实网未验证。允许创建路径仅上述后端新增文件；禁止临时产物入库，验证副本与图床替身位于系统 TEMP。

- DOWNLOAD-MEDIA-PREVIEW（2026-09-29）：按最终确认，顶部封面保留完整大图；图片资料中的封面、海报为可点击放大的缩略图；抽屉剧照改为统一 16:9 裁切缩略图（桌面 112×63、窄屏 96×54，object-fit: cover）并向下自动换行，不再按原图比例参差排布。点某张缩略图即从该张进入受控 Arco Image.PreviewGroup，预览层保持原样：底部 DragScrollRow 单行缩略图条，点选与左右箭头共用当前索引。BLUR 模式只显示模糊缩略图且不提供放大入口，INVISIBLE 不渲染图片。仅修改既有 CodeCard、CodeCard 测试、公共样式及本记录，不新增路径，不改 API 或数据。20 项相关测试、11 项规范检查、类型检查与构建通过；真实 JUR-868 的 10 张剧照全部加载，抽屉网格换行、点击第 3 张放大到第 3 张、底部缩略图条保持单行且第 3 张高亮、关闭预览保留抽屉、390px 三列无横向溢出均已核验。

- DOWNLOAD-MEDIA-STILLS（2026-09-29）：详情侧栏在图片资料下方显示已有剧照缩略图，空数组不渲染区域；复用 Arco Image.PreviewGroup，支持放大和左右切换，遵守 VISIBLE/BLUR/INVISIBLE 图片设置。主 Agent 仅修改既有 CodeCard、对应测试及本记录；允许创建路径：无，禁止临时产物入库。19 项相关测试及前端构建通过；真实 JUR-868 的 10 张剧照全部加载，切换第二张、关闭预览保留侧栏、390px 两列且无横向溢出已核验。无 API、数据库或历史数据变更；保留既有 React 19 ref 警告。

- DOWNLOAD-MEDIA-COVER（2026-09-29）：按用户跟进，侧栏顶部不再显示影片卡片，改为独立封面图，下方五组资料保持；CodeCard 的 detail 展示模式不挂载卡片及复制/业务按钮，封面优先 banner_url、缺失时用 poster_url，遵守图片设置。只修改既有 CodeCard、下载页面及对应测试和本记录，无新增项目路径、API或数据变更。相关18项测试及构建通过；真实侧栏确认卡片数0、封面加载成功、仅保留关闭按钮、五组资料完整。

- DOWNLOAD-MEDIA-DETAIL（2026-09-29）：下载列表点击番号打开 Arco Drawer，复用 CodeCard 并增加 hideActions、hideStatus、showDetails；完整资料参考 Arco Pro 基础详情页分组，手机单列。详情 GET /media/{mediaId} 新增 details，从已有元数据读取演员、标签、制作商、发行商、系列；缺失发行码、简介、导演、评分、想看人数、历史翻译引擎与分辨率明确返回 null。分类仅按已有 video_type 解释有码与马赛克，不猜测未知分类。主 Agent 只改既有下载页面/测试、CodeCard/测试、API类型、domain/models、database/repository/测试、OpenAPI及本记录；允许创建项目路径：无，禁止临时产物入库。无迁移、无历史数据回填。全量后端 test/vet、前端55测试及构建通过，真实3750详情和5173侧栏核验演员、订阅时间、时长等；390px单列且无页面横向溢出，关闭和Esc通过，无图模式零图片。OpenAPI/Apifox原节点同步并中文回读。在线备份位于系统TEMP/bytemuse-media-detail-before-20260929.db；重启后schema仍21，影片/元数据/订阅/下载/设置逐行比对一致。JUR-868已有译文包含提示词，未更改；浏览器保留既有React19 ref提示，截图工具未回传可见图像。

本节仅记录影响所有 Agent 的项目级事实和决策，由主 Agent 维护。执行 Agent 不并发修改本节，应把新证据反馈给主 Agent。

### 12.1 历史阶段记录

- LOGIN-REDIRECT（2026-09-28）：主 Agent 修复已登录刷新登录页仍显示表单。沿用 main.tsx 挂载前恢复 HttpOnly 会话和现有认证契约，在 routes.tsx 增加访客登录路由，已登录时 replace 到 /dashboard，未登录保留表单。允许新增路径仅 frontend/src/app/routes.test.tsx（长期路由回归）；禁止项目临时文件、过程目录、后端/业务库/旧资料修改。浏览器修复前复现且对标系统会自动跳看板；修复后验证已登录访问 /login 自动跳转、后台刷新保持会话、无会话登录页刷新和后台访问保护。前端 10 文件 50 测试、类型检查和构建通过；无 API、数据库或历史数据变更。控制台仍有既有 React 19 ref 提示；截图工具未回传可见图像，跳转由实际 URL/DOM 核验。看板曾出现一次 502，点击重试后真实数据恢复（媒体 46874、活动订阅 10511）；未修改该接口。

- DOWNLOAD-CONTROL（2026-09-28）：下载列表关联 media.code 显示番号，增加 available_actions、stopped 筛选、按状态操作和两种删除确认；qB 4.x 暂停/继续，5.x 停止/启动，按版本实际能力展示。失败搜索复用任务重试且要求活动订阅和无重复有效任务，失败传输启动原任务；失败仅允许删除任务。控制使用现有租约、防旧快照覆盖、外部结果回查；不新增结构迁移。允许新增 application/download_control.go、ports/download_control.go、platform/database/download_control.go、platform/downloadclient/qbittorrent_control.go及对应测试、transport/httpapi/download_control.go及测试，其他临时文件禁止入库。后端全量test/vet/build、前端46测试/构建通过；隔离真实Go API/SQLite与受控qB验证停止/继续、暂停/继续、失败重试、停止筛选、删除确认和取消；390px页面无横向溢出。当前5173页面及3750 API已显示JUR-868和重试/删除。未操作真实下载器任务/文件；删除后文件系统结果及PostgreSQL/MySQL实库未验收；浏览器截图工具未返回可见图像。OpenAPI与Apifox三接口已同步回读。

- FILTER-GRID（2026-09-28）：提取所有影片页网格为公共filter-grid并应用全部7个列表筛选页，清理固定宽度与单页分列覆盖，搜索结果说明移到网格外。只修改既有相关TSX/CSS和本记录，无新增项目路径。浏览器320/390/1280/1920/3510px验证均分列宽及无横向溢出，演员搜索1条/重置47条；相关4文件18测试、类型检查/构建通过。全量43测试中42通过，SettingsPage测试因找不到“是否使用代理”开关失败，本轮未修改该设置文件。

- ACTOR-FILTER-LAYOUT 按钮跟进：演员筛选使用普通Input保留前置搜索图标，删除Input.Search自带末尾图标；增加独立搜索/重置按钮与表单回车提交。搜索和重置回第一页，条件未变化时仍可刷新。只改既有ActorListPage.tsx、styles.css和本记录，无新增路径。类型检查/构建通过；实际浏览器搜索Tiny Lu返回1条、重置恢复47条，390/1280px下均只有1个输入框搜索图标且筛选栏无溢出。

- ACTOR-FILTER-LAYOUT：演员页沿用标签页的纵向结构，Tabs下方16px放置居左的演员名称筛选，筛选下方使用Arco Divider。移除旧Tabs与搜索同行及专属Tabs覆盖样式。仅修改既有ActorListPage.tsx、styles.css和本记录，无新增路径及API变更。类型检查/构建通过；浏览器390/1280/3510px确认Tabs→筛选→分隔线顺序、同左边界和无横向溢出；仓库无演员页专用测试文件。

- FILTER-LABELS（2026-09-28）：所有列表筛选条件提供可见中文标题，统一标题居左；补齐标签类型/名称、演员名称、搜索关键词、所有影片五项条件和榜单来源/周期，下载与日志沿用原有标题。只修改既有相关Page.tsx、styles.css、SearchPage.css和本记录，无新增项目路径、API及数据修改。手机标签搜索/重置单独换行，390px名称框实测247px。浏览器7页在320/390/1280/3510px下标题存在且无横向溢出，前端41项测试通过。

- PROMPT-LIMIT 字数交互修订（2026-09-28）：用户改为框内右下角字数、超限截断。两类提示词按Unicode码点计数，上限15360字符（最坏4字节/字符兼容后端60KiB），输入和提交统一截断，不切断emoji代理对；达到上限计数使用主题危险色。删除前次字节说明和前置错误弹窗；只改既有SettingsPage、测试、styles及本记录，无新路径。浏览器确认右距16px/下距8px、15361字符输入截断至15360并变红，验收草稿重置未保存；设置8项测试和构建通过，全量42项中41通过，1项无关标签页重复搜索测试失败。

- FILTER-RESPONSIVE 标签对齐修正：按用户最新截图要求，类型与搜索组紧邻居左（间距12px），列数设置继续居右，替代此前搜索居中规则。只改既有styles.css和本记录，无新增路径。浏览器390/1280/3510px核查通过，宽屏类型180px、搜索组360px且间距12px，窄屏上下排列同左边界；类型检查和构建通过。

- FILTER-RESPONSIVE 宽屏修正：用户截图确认无上限flex增长导致演员搜索框在3516px视口达到约2870px。公共筛选字段停止分配多余空白，演员搜索280px、标签类型180px/搜索组360px、独立搜索组480px、日志普通控件330px、日期范围340px；手机布局仍填满可用空间。只修改既有styles.css、SearchPage.css、LogsPage.css及本记录，无新增项目路径；所有影片布局和公共分页两行规则保持不变。浏览器核查7页320/390/768/1280/1920/3516px，超宽屏宽度上限与窄屏不溢出已确认；截图存系统TEMP，工具未回传可见图像，视觉细节不宣称完整验收。

- PROMPT-LIMIT 前端跟进（2026-09-28）：主 Agent 仅修改既有 SettingsPage.tsx、SettingsPage.test.tsx 和本记录，无新增路径、API或数据库变化。两类提示词实时展示原始草稿UTF-8字节用量/61440上限，Arco错误态与超出字节说明保留完整草稿；按钮保存和Ctrl+S共用前置拦截，并聚焦超限输入框。前端40项测试、类型检查及构建通过；5173独立验收页确认中文超限1字节、Ctrl+S拦截、emoji恰好61440字节无错误、重置恢复。测试草稿未写入数据库，验收页已关闭。

- PROMPT-LIMIT（2026-09-28）：自定义 System Prompt 草稿 13449 UTF-8 字节被普通配置 8192 字节上限拒绝。主 Agent 仅修改既有 application/settings.go、settings_test.go、OpenAPI 和本记录，无新增源码路径；两类提示词各允许 61440 字节，其他配置仍 8192，超限错误包含上限/当前字节数且整批不写入。无需迁移，数据库保持版本20。先失败后通过的容量/中文/emoji/清空/原子拒绝测试及后端全量 test、vet、build 通过；3750 后端已重启，浏览器保存后 GET、刷新回显与 SQLite 均为6004字符/13448字节（沿用首尾空白清理），超限61441字节返回400且原值不变。Apifox 保存接口和请求模型已同步回读；其他配置未修改，未调用第三方AI。

- FILTER-RESPONSIVE 分页跟进：公共分页容器不超过640px时固定两行，上行页码居中，下行总数居左、页长选择居右；更宽容器保留单行。只改既有styles.css与本记录，无新增项目路径。浏览器320/390/550/768/1280/1920px尺寸核验通过，窄屏上下行位置与总数/页长同高已确认，键盘翻到第2页后仍显示总数914；类型检查和构建通过。鼠标自动点击因浏览器工具超时未验证，截图保存于系统TEMP。

- COLLECT-CF-01（2026-09-28）：用户明确 CF 拦截先使用既有爬虫增强重试，需要登录 Cookie 再报告；主 Agent 允许新增 collector/bypass.go、bypass_test.go 正式源码与测试，修改现有 client/启动装配/队列错误映射/HTTP 回归、README、OpenAPI 和本记录。禁止修改业务库、旧资料、订阅下载或无关前端；不新增过程文档，不部署或重启在用实例。
- COLLECT-CF-01 实现：只对明确 CF 标记增强一次，普通 403/429 不增强；FlareSolverr 根地址或完整 /v1 使用 request.get，cloudflare_bypass_for_scraping 使用已核实 GET /html。增强响应再次分类、限长、校验同源后才解析，返回 Cookie 不保存；源站登录要求为 source_cookie_required，增强失败 bypass_unavailable，地址/未支持协议 bypass_config_invalid。Scrapling HTTP 封装协议未确认，未猜测实现。配置启动时读取，增强服务使用自身网络出口。
- COLLECT-CF-01 实测：已配置 FlareSolverr 3.5.2 健康端点在线；Go 六站复测 JavDB/JavLibrary/Jable/SupJav 增强超时，AVBase 增强浏览器 ERR_CONNECTION_RESET，JavBus 普通拒绝访问；没有确认需要登录 Cookie 的入口，也没有新增可用来源。后端全量 test/vet/build 通过，错误映射先失败后通过；采集进度接口与模型错误说明已同步 Apifox 并回读。真实增强尚未取得可解析数据，不声称外站接通；无正式库采集或历史数据变化。
- COLLECT-CF-01 审查边界：只读审查 Agent 两次未收到任务正文，未形成独立审查；主 Agent 已核对协议、源站白名单、增强结果校验及测试，不宣称独立审查通过。Netflav 搜索/详情最终实网复验正常，搜索 20 条。增强改动尚未重启启用，无数据库结构变更。
- BYPASS-PROXY-01（2026-09-28）：用户最新要求覆盖自动带代理规则：下拉与备注显示 ByPass，协议键 cloudflare_bypass_for_scraping 不变；爬虫增强下增加“是否使用代理”，默认关闭，选择“不使用”立即关闭并禁用，再次启用不自动打开。仅开关 true 且 PROXY 非空时透传给增强服务，服务连接本身直连。FlareSolverr 鉴权代理通过独占 session create/get/destroy，失败也清理；ByPass 通过 proxy 查询参数；Scrapling HTTP 协议仍未确认，不声称集成。
- BYPASS-PROXY-01 运行启用（2026-09-28 23:44 后）：用户授权备份和重启，原 PID48060 启动于22:30，早于23:12新增设置代码，造成 BYPASS_USE_PROXY 无效。在线/停机备份和演练副本在 E:/系统文件夹/文档/ByteMuse-bypass-backup-20260928-234423/；20→21 及重复迁移验证仅新增开关，旧业务表和旧设置逐项不变。新后端 PID49056，dev/backend.pid 已同步，真实鉴权 PUT 保存并 GET 回读 true，随后重启加载，ready=ok，完整性ok、外键错误0。用户要求测试开启代理，故仅保存 BYPASS_USE_PROXY=true；原 FlareSolverr 类型、服务地址、代理地址不改，ByPass 仅在测试进程指定。
- BYPASS-PROXY-01 ByPass实测：用户指定 内网代理地址，OpenAPI确认 GET /html 支持 proxy，六站Go测试显式开启代理；未获取新增可解析数据。直接带代理访问首页确认 JavLibrary/JavBus/Jable 为400“localhost and private IPs are not allowed”，AVBase/SupJav/Avgle 为500“Failed to bypass Cloudflare protection”；JavDB增强也失败。无代理对照Jable同样400，不能断言是内网代理被拒；服务端目标URL校验/DNS仍需排查，不绕过安全校验。未发现明确登录Cookie要求，不注册新来源，不写入影片。
- BYPASS-PROXY-01 范围：主 Agent 修改已有 SettingsPage 及测试、settings 服务及测试、collector client/bypass 及测试、bootstrap、migrations、pt_site_settings_test、README、OpenAPI 和本记录。允许创建路径：无；明确禁止新过程目录、临时文件入仓库、业务库或旧资料写入、无关下载/UI 修改。迁移 21 只新增非敏感字符串布尔配置 BYPASS_USE_PROXY=false，不改表结构或覆盖已有配置；配置仍启动时读取，不重启在用实例。
- BYPASS-PROXY-01 验证：设置页最终 9 项测试和后端 application/collector/database 测试及 vet 通过；bootstrap/httpapi 测试在本轮较早检查通过。SQLite 20→21 升级及重复升级保留代理地址与已保存开关。实际 5173 页面确认 ByPass 选项和备注、开关开启、增强关闭时禁用、重新启用保持关闭、重置恢复；未在在用后端保存新字段，未改正式库，未做带代理外站实测。截图工具未返回可见图像；控制台有既有 React 19 element.ref 兼容提示。全前端此前 43/44 通过，下载列表 TEST-001 单元格断言失败；全后端下载客户端 TestQbittorrentListTransferStates 失败，后续全量 vet 又被并发下载改动的 Qbittorrent.Capabilities 未定义阻塞，不改本轮范围外代码。前端构建、后端 build 通过；设置模型新字段已同步 Apifox 并回读。

- FILTER-RESPONSIVE（2026-09-28）：筛选控件按内容区伸缩，下载/日志共用 filter-layout、filter-field、filter-control；所有影片按可用宽度自动分列，演员搜索可伸缩。标签页按容器宽度切换三列左中右、两列及单列，分页按完整分组换行。主 Agent 仅修改既有 styles.css、TagListPage、DownloadListPage、LogsPage TSX/CSS 和本记录；允许创建路径：无，禁止新增项目过程文件、临时产物及修改后端/业务数据。前端9文件37测试、类型检查/构建通过；在用5173浏览器核查标签、下载、日志、所有影片、演员、搜索、榜单在320/390/768/1280/1920px下筛选区域无横向溢出，标签名称搜索返回1条。控制台仍有既有React19 ref兼容提示；截图保存在系统TEMP，但工具未向模型回传可见图像，视觉细节仅有DOM尺寸证据。无API、数据库及历史数据修改。

- TAG-SUB-02 列数控件：筛选区下增加 Arco Divider，右侧增加每行显示个数 Slider（1–10，步长1，联动数字输入），默认5。所选值作为列数上限，CSS Grid 按卡片最小240px自动减少列数，极窄屏单列填满容器，窗口放宽后恢复所选上限，不请求API；顶部筛选保持固定。修改既有 TagListPage/styles/测试，无新增路径；6项标签页测试、类型检查/构建通过，在用浏览器核验默认5时1920px五列、1280px三列、390px一列且无横向溢出，手选10后窄屏仍自动单列。

- TAG-SUB-02 布局跟进：标签路由使用 innerScroll 元数据，公共 AppLayout/PageSurface 通过可选填满高度支持标签列表独立滚动，Tabs/筛选/分页固定；标签网格按最小 240px 自动排列，名称输入框宽240px并支持窄屏换行。仅修改已有 routes/AppLayout/PageSurface/styles/TagListPage，无新增路径、API或数据库修改。标签页5项测试及前端类型检查/构建通过；在用浏览器核验滚动334px时筛选位置不变、外层scrollTop=0，1920px为6列、390px为1列无横向溢出，翻页列表回顶，演员页维持原外层滚动。控制台仅发现既有React19 ref兼容提示。

- TAG-SUB-02 分页跟进：全部标签改为 100/200/300/400/500，默认 100；订阅中沿用常规分页并独立保留页长，切换或修改页长回首页。标签 HTTP/业务/仓储统一 MaxTagPageSize=500，不扩大其他列表上限；OpenAPI/Apifox GET /tags 已同步并回读。2026-09-28 最终在用 3750 鉴权接口五档各返回对应数量（total=914），5173 浏览器确认默认100、500档第一页500/第二页414、改回100重置第一页；当前后端全量测试通过，标签页5项测试与构建通过。无需新迁移，本轮未再次重启；隔离3768实例已停止。

- TAG-SUB-02 正式启用（2026-09-28 21:32）：确认 COLLECT-04 已收尾且当前后端全量测试通过后，按用户此前等待采集完成统一重启的授权执行。在线及停止旧后端后的最终备份保留于 C:/Users/admin/Documents/ByteMuse-backup-20260928-213054-tags/；正式 dev/bytemuse.db 19→20，14 张旧业务表逐行摘要不变，完整性 ok、外键无异常。3750 已从当前源码重启，dev/backend.pid 已更新，就绪和鉴权 API 正常；active 返回 0、all 返回 914，5173 浏览器切换验证相同结果。旧版 759 条源于忽略 subscription 参数，已随新后端启用解决。未新增真实标签订阅或人工触发采集/下载；浏览器仍有既有 React 19 ref 兼容警告。

- COLLECT-04（2026-09-28）：用户要求除 ThisAV 外接入其他来源，随后明确不可用则不添加。按后台实际请求验收：Netflav 搜索 20 条及详情复验通过；JavLibrary、AVBase、JavBus、Jable、SupJav 请求 source_blocked，暂不开放来源目录和入队；Avgle 超时/API 520 不添加；ThisAV 明确排除。既有 JavDB 保留，新增详情解析已测试但实网受阻，不能声称可用。五站解析器仅保留离线回归，不注册运行时入口。
- COLLECT-04 由主 Agent 收尾，沿用 COLLECT-02 契约；修改既有采集 ports/registry、解析测试、来源持久化测试、HTTP 集成测试、README、OpenAPI 和本记录，不新增项目路径；此前正式采集适配器/测试保留。禁止修改旧资料、原库、在用业务库、无关前端及订阅下载流程。无需新迁移、不回填历史、不全库匹配，遵守等待统一重启的既有安排。
- COLLECT-04 验证：先复现 GET 来源错误返回七站，再验证仅返回两站、七个排除来源 POST 返回 400 且批次为零；仓储拒绝排除来源。后端 go test ./...、go vet ./...、go build ./... 通过；Netflav 实网搜索 20 条、详情演员 1 条且有发行日期。OpenAPI 来源/提交接口及 CollectionRequest 已同步 Apifox 并回读。未部署、未重启在用实例、未对正式业务库执行采集。

- TAG-SUB-02 界面跟进（2026-09-28）：按用户要求给 Tabs 与筛选区增加 16px 间距，InfoCard 增加右侧垂直居中操作布局，标题链接保持正文颜色，修改既有标签页面/操作/InfoCard/公共样式及测试，无新增路径。真实在用 3750 接口 active/all 均返回 759 条且仅有 name/media_count，确认仍为旧后端；遵守用户等待采集完成统一重启决定，不做前端全量过滤。新版隔离实例验证全部 305 条、订阅后活动 1 条、取消后活动 0 条；桌面 1280 与窄屏 390 无横向溢出，标题访问后颜色一致，按钮居中。标签页 5 项测试、后端标签 HTTP 测试、前端类型检查与构建通过，截图仍无可见图像。

- TAG-SUB-02（2026-09-28）：用户要求标签分类、订阅中/全部、订阅/取消/编辑与标题跳转搜索，并明确授权按起始日期自动追新，保留既有下载流程。源站编辑仅修改限制日期。主 Agent 负责；允许新增 database/tag_taxonomy.go、tag_subscriptions.go、tag_subscriptions_test.go 及 frontend/src/features/tags/TagSubscriptionActions.tsx、搜索页正式测试；允许修改现有标签各层、迁移、搜索契约/页面、bootstrap、InfoCard、OpenAPI、README、本记录及相关测试。禁止项目过程目录、临时文件、旧参照/原库修改、源站写入与无关改动。
- TAG-SUB-02 实施顺序：先以隔离库验证分类迁移与标签订阅持久化、日期校验、精确标签匹配和重复执行，再接通现有标签定时任务及搜索页，最后浏览器验收和 Apifox 同步。分类字典取源站页面，未知名称保持未分类；订阅配置与影片处理记录独立持久化，取消标签只停止追新，不撤销已有影片订阅/下载；处理记录防止重复运行恢复被用户取消的影片订阅。迁移不改历史影片和标签文本，不批量订阅；用户保存订阅规则或定时触发才匹配。
- TAG-SUB-02 交付与验证：迁移 20 保存源站 305 个唯一标签及七种分类，精确名称归类，未知名称显示未分类；服务端完成状态/类型/名称过滤与分页，标签标题进入精确标签搜索。保存规则及 TAG_SCHEDULE_TIME 共用本地目录追新，起始日含当天、未知发行日不匹配，不新增下载流程。SQLite 副本 19→20 及重复升级通过，14 张旧表逐行摘要保持一致，46874 部影片保留，完整性和外键检查通过，规则初始 0 条。隔离浏览器确认订阅、编辑日期、取消保留影片订阅、类型筛选、空结果、搜索与返回恢复；前端 33 项测试、类型检查和构建通过，后端标签相关 application/database/httpapi/bootstrap 测试、全量 vet/build 通过。最后一轮后端全量测试受并行采集任务 TestStreamMetadata 失败影响，不能宣称全量通过。OpenAPI 与 Apifox 标签 GET/PUT/DELETE、搜索 GET 已同步回读。PostgreSQL/MySQL 实库和截图视觉验收未完成；独立审查 Agent 未收到任务正文，仅完成主 Agent 自审。
- TAG-SUB-02 启用安排：用户明确要求等待采集任务完成后统一重启；当前未升级在用 dev/bytemuse.db（仍版本 19），未重启 3750。隔离测试服务 3768 已停止、临时浏览器页已关闭；系统 TEMP 的 bytemuse-tag-sub-359634fe395b4fb78fbd166f79a0c9d9 删除被环境策略拒绝，暂留验证库副本与程序，不在仓库。统一启用前需协调采集任务最终状态、重新验证并备份在用库。

- 下载任务列表新增服务端状态及加入/完成时间筛选。传输状态由 qBittorrent 只读轮询取得；下载失败包含搜索/提交失败与下载器失败，历史未同步字段保持 NULL，不将 submitted 当作下载完成。

- 当前阶段：新版 Go 后端、React/Arco 前端与 API 基线已建立，继续按业务切片扩展旧版功能。
- 已完成：统一 Agent 规范；旧版目录、接口、ORM、调度、主要集成和关键订阅状态的静态核查；旧数据库第一轮只读结构、行数、状态和代码引用审计。
- 尚未完成：对标系统全页面和流程清单；数据库异常数据的业务原因；旧功能的完整重建范围与 API 兼容策略。当前技术栈为 Go、React/Arco，支持 SQLite、PostgreSQL、MySQL；首批业务切片已实现，未接入模块仍需逐项建立契约。
- 新版代码已存在于 backend/、frontend/，API 契约位于 api/openapi.yaml。前端开发和生产构建均直连 Go API，不使用浏览器 Mock 或假数据；所有页面数据必须来自后端数据库。已实现且页面有后端接口支撑：登录、看板、媒体库、订阅、下载任务、演员、上新、推荐、榜单、搜索、设置、任务、日志。标签、厂牌、账户、通知仍缺 Go API，页面明确标记为未接入。设置页的分组、字段命名与旧版/对标站已完全对齐（16 个分组），配置项的消费方（下载器、媒体服务器、通知、过滤排序）仍待按模块接入。

### 12.2 历史决策快照（现行规则见专项规范）

- 筛选表单网格（最新规则）：所有影片、演员、标签、搜索、下载、日志、榜单在页面直接使用 Arco Grid.Row/Grid.Col；Row gutter={[12, 12]}、justify=start、align=center，所有条件与按钮组均使用 xs={24} sm={12} md={8} xl={4}。1200px起六等份、768px起三等份、576px起两等份、更窄单列；条件少时剩余位置留白，日期范围也只占一列。已删除公共filter-grid及其自定义分列媒体查询，CSS仅负责内部标题/控件/按钮布局与表单留白。保留Tabs下方筛选、分隔线、分页两行及标签列数控件窄屏隐藏。设置和编辑表单不属于本轮列表筛选范围。2026-09-29浏览器核验七页390/576/768/1280/1920px等宽及无横向溢出，相关4文件22测试、类型检查及构建通过。仅修改已有页面、公共CSS、相关测试和本记录，无新增项目路径。

- 筛选操作统一布局：搜索、查询、重置及同栏操作按钮紧跟最后一个筛选控件，整体居左，控件与操作组间距12px、组内按钮间距8px；不足时操作组换到下一行并居左。禁止通过space-between、自动左外边距或弹性占位把筛选操作推到屏幕最右侧。Arco Grid与公共filter-actions为权威实现，不再增加页面级反向覆盖；标签页列数调整在按钮后的网格中显示。

- 清理系统日志固定按服务所在时区每天 00:00 执行（`0 0 * * *`）；执行时读取 `LOG_RETENTION_DAYS`，空值或 0 继续表示不自动清理。修改调度后需重新启动后端才能生效。

- 所有 Agent 只以本文件作为 Agent 规则和项目统一说明。
- 未加密代码/ 保持只读，旧应用不得直接启动用于探索。
- 历史数据默认不修改；数据库审计使用副本和只读方式。
- 数据库结构变更禁止生成增量或临时 SQL；建库脚本或全量 SQL 是结构权威，历史库通过程序化版本迁移无损升级。
- PostgreSQL 和 MySQL 可使用本地 Docker 隔离调试，数据库操作和回查优先使用 dbx MCP。
- Git 只提交源码和可复现构建所需资产，打包目录和发布文件必须通过根目录 .gitignore 排除。
- Docker 最终运行镜像只保留打包产物和必要运行资源，源码、测试、.git、开发依赖和构建工具不得进入最终镜像。
- UI 必须经过浏览器实际交互和截图验收。
- 页面内容区不渲染任何可见标题与顶部工具栏：页面名已由顶层面包屑 .app-breadcrumb 承担，PageHeader 只输出一个 .sr-only h1（供无障碍名称与路由断言），原有的计数、更新时间等操作区已删除。内容第一块与面包屑之间的间距只有面包屑自身的 margin-bottom 16px，所有页面一致。
- 输入框与按钮规格以对标站 react-pro.arco.design/form/step、/form/group 实测为唯一标准：输入框、Select、InputNumber 与所有按钮（含设置页底部通栏操作栏的保存、重置）统一 32px（Arco size-default），页面组件一律不写 size 属性，显式写 size="default" 也算多余；设置页操作栏栏高 60（上下内边距 12 + 按钮 32 居中）；表单字段纵向间距 20px；控件内文字 14px。统一由 styles.css 的 --control-font-size、--field-gap-y 与 Arco 默认尺寸维护，新增控件不得另立规格。
- 列表分页以服务端分页为唯一权威，前端一律复用 shared/ui/ListPagination（内部为 Arco Pagination）：总数走 showTotal，页码走默认分页按钮，每页条数走 sizeCanChange，选项固定为 15/30/50/100/200，默认 15；分页条在内容区左对齐，不再自行拼装「上一页/下一页」文本分页。每页条数由页面持有并按 page_size 写入请求参数（同时进 react-query 的 queryKey），切换条数必须把页码重置为 1；page_size 上限由 backend/internal/ports 的 MaxPageSize 单点定义，HTTP 层、业务层与仓储层共同引用，前端选项不得超过该值。
- 系统设置项以旧版 app/config/template.env（即对标站 /config）的键名为唯一权威，页面按对标站 16 个分组维护：站点、Emby、Plex、Jellyfin、微信、Telegram、Qbittorrent、Transmission、迅雷、CloudDrive2、过滤、排序、定时任务、翻译、Agent、其他。
- 配置项取值域在 backend/internal/application/settings.go 的 writableSettings 一处声明与校验，前端控件类型与之对应：文本、布尔（统一存 true/false）、非负整数、JSON 对象（DEFAULT_FILTER）、封闭枚举（IMAGE_MODE、MAIN_SITE、RANK_TYPE）、排序标签（DEFAULT_SORT）。越界或未声明的键返回 400 invalid_setting 并回传具体原因。
- 漂移过的设置键名 WECHAT_PROXY_URL、PROXY_URL、CD2_PASSWORD、CD2_SAVE_PATH、EXTERNAL_URL 已废弃，统一为 WECHAT_PROXY、PROXY、CLOUDNAS_PASSWORD、CLOUDNAS_SAVEPATH、EXTERNAL_DOMAIN；库中原本无这些键的存量数据，无需迁移。
- 开放 API 变更必须通过 apifox-new-mcp 同步并回读。apifox MCP 不可用时，只能改 api/openapi.yaml 并在交付说明中明确记录未同步的接口清单，不得静默跳过。
- 日志能力由进程内环形缓冲承担，不引入新的持久化表：backend/internal/logging 的 Logger 保留最近 500 条日志（超出丢弃最旧），进程重启即清空，因此日志只反映当前进程生命周期，历史日志仍以 stderr 输出为准。日志记录统一为 UTC RFC 3339 时间，级别为 debug/info/warning/error，分类为 采集同步/订阅查询/下载/媒体库/通知/系统/Agent/其他，敏感键（token、password、secret、cookie 等）在写入缓冲前过滤。查询契约 GET /logs 支持 page、page_size、level、category、keyword；清空契约 DELETE /logs 返回 {cleared}，只清当前进程缓冲，不删任何业务数据。前端日志页按 5s 间隔轮询该接口，不建长连接。
- 三个及以上相互独立的同类任务优先并行，结束后统一审查。

### 12.3 数据库未使用对象台账

| 对象 | 分类 | 数据证据 | 代码证据 | 可能关联功能 | 风险 | 下次核查触发点 | 决定 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| health 表 | 疑似未使用持久化 | 0 行 | 有 Health 结构及即时健康返回，未发现持久化读写 | 模块健康检查 | 删除可能影响未提取代码或未来持久化 | 健康检查重建前 | 保留并复核 |
| tag 表 | 证据不足，源码缺口 | 0 行，有 ix_tag_name | AI 工具查询 Tag，并调用缺失的 itag 和 schema；ORM 导出无 Tag | 标签查询和追新订阅 | 当废表删除会丢失未完成功能语义 | 标签功能或 Agent 重建前 | 保留，先补齐证据 |
| code.star | 疑似错误类型且当前未用数据 | 46,874 行全部为空，声明类型 init | 旧接口存在收藏写入，旧 ORM 期望 Boolean | 收藏 | 直接按现状迁移会固化错误类型 | 收藏功能和 code 迁移前 | 目标使用布尔类型，历史取舍待定 |
| code.cn_title | 确认使用 | 4,106 条非空 | 产品 AI 搜索与订阅优先使用 | 中文标题 | 旧 ORM 未声明会导致遗漏迁移 | 番号展示和搜索重建前 | 保留并纳入目标模型 |
| code.local_banner | 确认使用 | 7,143 条非空 | 产品 AI 搜索优先本地横幅 | 本地图片缓存 | 迁移遗漏会丢失本地资源映射 | 图片与番号展示重建前 | 保留并核实存储策略 |
| code.local_still_photo | 数据确认、调用证据不足 | 6,873 条非空 | 当前文本搜索未发现明确消费者 | 本地剧照缓存 | 可能由编译模块或未提取前端使用 | 图片和详情功能重建前 | 保留并在对标系统复核 |
| code 异常状态/模式 | 异常历史数据 | 18 条 status=STRICT、mode=UN_SUBSCRIBE | 与模型和服务语义相反，未确认来源 | 订阅迁移 | 自动纠正可能破坏历史含义 | 状态机和迁移设计前 | 原样保留并隔离报告 |
| history 孤立 code | 异常关联但可能仍有用 | 3 条 code 无对应 code 行 | history 用于种子哈希去重 | 下载历史和去重 | 删除会允许重复下载 | history 迁移前 | 保留，迁移报告单列 |
| Aria2 模块 | 疑似未接入 | 有 aria.py | 未在统一 Module 容器初始化，未发现主流程调用 | 下载客户端 | 可能是遗留或未完成适配 | 下载客户端范围确定时 | 复核对标系统后决定 |

### 12.4 项目级任务记录格式

#### AGENT-TEST-01：OpenAI 设置草稿测试（2026-09-28）

- 主 Agent 负责。允许新增 application/openai.go、openai_test.go 与 transport/httpapi/openai.go、openai_test.go 正式源码测试；修改既有 translation.go、handler.go、SettingsPage 及测试、OpenAPI、README 和本记录。禁止新增过程目录、修改旧资料或业务库；无需数据库迁移。审查子 Agent 仅只读，不允许创建路径。
- Agent 表单底部“测试 OpenAI”读取未保存的地址、模型及 API Key，不依赖开关、不保存设置；测试期间禁用按钮，结果通过统一 Message 显示。POST /system/settings/openai/test 要求登录；成功与失败格式分别为 OpenAI 连接成功 (892ms) 和 OpenAI 连接失败 (892ms)：失败原因，耗时实测。客户端网络故障使用浏览器等待耗时。
- 与翻译共用 chat/completions 请求、30 秒超时、非空回复判定、禁止重定向及错误脱敏。只验证连接与简短对话能力，不执行 Agent 工具。
- 验证：设置页 6 项回归、应用与 HTTP 包测试和 vet、前端构建及后端编译通过；覆盖未保存草稿、重复点击、失败恢复、鉴权、无效输入、空回复、非 JSON、重定向、超时取消及翻译兼容。隔离实例实测成功 1509ms、失败 1514ms，测试前后设置完全一致。OpenAPI 与 Apifox 已同步回读。
- 浏览器已验证按钮位于 Agent 表单下及 Message 错误提示；最终耗时文案复验受浏览器 URL 安全策略阻断，截图与最终浏览器验收未完成。真实用户 OpenAI 服务未调用。在用后端未重启，新增接口需重新启动当前源码后生效。并行采集模块补齐后，最终 go test ./... 与 go vet ./... 已通过。本次独立审查子 Agent 停留在 pending_init，已中止，未获得审查结论。临时进程已停止，TEMP/bytemuse-openai-e6661baae1ac4e738096872471d0d611 删除被策略拒绝，保留虚构验收数据和测试程序，不在仓库。

#### SITE-AUTH-02：站点密钥搜索与取种（2026-09-28）

- 用户明确要求将 SITE-AUTH-01 的密钥字段接入真实搜索和取种；统一沿用 ResourceSearcher/PrivateTorrentSource，资源只保存站点与 ID，不保存秘密下载地址。PTFans/NicePT 共用 NexusPHP 令牌协议，RousiPro 独立协议，PTTime 先核实 PassKey 搜索能力；密钥模式不回退 Cookie。
- 主 Agent 负责集成；允许新增 torrentsearch/nexusphp.go、nexusphp_test.go、pttime.go、pttime_test.go、site_http.go、site_http_test.go 与 bootstrap/download_sites.go、download_sites_test.go 正式源码测试，修改现有 RousiPro 适配与测试、bootstrap、SettingsPage 及测试、OpenAPI、README 和本记录。RousiPro 子任务独占 rousipro.go 与 rousipro_test.go，其他文件主 Agent 独占。禁止新增过程目录、临时脚本、真实凭据文件或修改旧资料、业务库。
- 无新增数据库结构；凭据沿用迁移 19 与加密设置。受控 HTTP 验证鉴权隔离、分页、空结果、拒绝异常/跨域/重定向与种子校验；真实验收只搜索和取种，不添加 qB、不下载内容、不创建或轮换站点令牌、不部署或重启在用实例。未确认的站点能力不宣传为已支持。
- 用户后续确认：不接受有限 RSS 代替全站搜索；缺少完整密钥搜索/下载能力的站点只提供 Cookie。PTTime 移除密钥与 UID 表单，运行时仅使用 PTT_COOKIE；历史密钥字段加密保留但不读取，不改写历史配置。允许 NicePT 子任务修改现有 ptfans.go、ptfans_test.go 并新增 nexus_cookie_test.go；允许主 Agent 移除已失去消费者的 settings-site-key-row 样式。
- 交付实现：三站密钥搜索分页及取种已装配到订阅手动/定时下载的共享运行时。PTFans cas、NicePT 使用 NexusPHP Bearer API；RousiPro 个人 Key 使用兼容 API 的 keyword/category/page/page_size 和详情返回短时 capability URL，不能混用其浏览器 Cookie API 的 query/category_id/offset 协议。Cookie HTML 搜索复用 NexusCookieSearcher，PTTime adults.php、NicePT torrents.php、PTFans special.php，包含断种与全部结果页。资源 URI 只含站点和 ID，取种限同源、拒绝重定向、校验 info hash；密钥模式不携带 Cookie。
- 真实证据：Go 适配器无 Cookie 搜索及取种，PTFans 1 条/38815 字节、NicePT 1 条/17913 字节、RousiPro 1 条/80745 字节，全部通过 Bencode/info hash；PTTime 浏览器 Cookie 搜索 SNOS-315 1 条并取种20135字节，Go Cookie 解析/分页/下载为受控 HTTP 验证，尚未使用真实 Cookie 驱动 Go 客户端。未请求 qB、未下载内容、未保存真实凭据、未改在用库或重启部署。
- 验证：后端全量测试、vet、build；前端31项测试、typecheck、build通过。浏览器确认三站鉴权切换、PTTime仅Cookie、旧提示移除与无横向溢出；存在既有React 19 element.ref兼容警告，构建有chunk体积提示。OpenAPI/Apifox设置GET和PUT引用的两个模型已同步并回读。子Agent任务正文传递失败未产生修改，最终由主Agent实施与审查，不宣称独立审查通过。正式新增仅nexusphp.go/nexusphp_test.go、pttime.go/nexus_cookie_test.go及bootstrap/download_sites.go/download_sites_test.go；无临时仓库产物。

#### SITE-AUTH-01：站点凭据模式设置（2026-09-28）

- 范围：PTTime、PTFans、RousiPro、NicePT 标题下增加密钥/Cookie 单选，分别保存凭据；馒头保持存取令牌。未保存模式默认密钥，已保存的选择保持不变，切换不清除隐藏字段；PTTime 密钥模式为同一行排列的 PassKey 与 UID。H&R 文案保持不变。
- 主 Agent 负责；仅修改现有 SettingsPage 及测试、settings 服务及测试、migrations 和 pt_site_settings_test、bootstrap、OpenAPI 与本记录，不创建项目新路径、过程文档，不改旧源码或原库，不部署，不保存用户提供的真实令牌，不调用 qB。
- 契约 SITE-AUTH-01：四站 *_AUTH_TYPE 取 key/cookie，空值默认密钥；PTT_PASSKEY 和三站 *_API_KEY 加密保存，空值未配置；PTT_UID 为正整数字符串，空值未配置。迁移 19 只新增 app_settings 配置记录，不改结构或覆盖旧凭据。
- 边界：此任务仅交付设置表单与持久化，不扩展站点密钥搜索下载适配；密钥模式在页面明确提示未接入，运行时不得回退使用历史 Cookie。PTTime 无 Cookie 取种已核验，密钥搜索尚未确认。
- 验证：前端 7 文件 28 测试、类型检查及构建通过，后端 go test ./...、go vet ./...、编译通过；SQLite 18→19 迁移保留旧 Cookie、重复升级保留用户模式。隔离真实实例确认四站切换、保存刷新保留、隐藏 Cookie 恢复、重置、无横向溢出及未捕获控制台错误。设置 GET/PUT 与字段描述、请求/响应示例已同步 Apifox 并回读。未部署或升级在用库，未填真实令牌、未调用 qB；截图返回字节但未显示图像，未完成截图视觉验收。
- 清理：隔离服务已停止；系统 TEMP 下 bytemuse-site-auth-e9c05f094f6c4565930344c9c189d5d4 清理被环境策略拒绝，保留测试可执行文件和仅含虚构凭据的 SQLite 库，不影响业务库。仓库未新增路径。

#### COLLECT-TAGS-01：少量影片标签追加（2026-09-28）

- 2026-09-28 在用本地实例启用：用户明确允许备份、迁移和重启。SQLite 在线备份及停止旧后端后的最终备份保留于 C:/Users/admin/Documents/ByteMuse-backup-20260928-163440/（before-v15.db、before-restart.db）；副本 15→19 升级和重复迁移通过后，原 dev/bytemuse.db 已升级至 19，新后端 3750 就绪且 dev/backend.pid 已同步。按旧表旧列逐项比对确认 46874 部影片、10511 条订阅、1432 位演员、1200 条榜单及全部旧业务内容未变，既有配置值未改、仅新增 11 项，完整性与外键检查通过；未新增采集或下载任务，未执行全库匹配。
- 在用浏览器验收：5173/tag 实际显示 759 个标签、每页 15 条；第二页、从第二页搜索 4K 后回到第一页（1 标签、关联 152 部）、无匹配提示及清空搜索恢复均通过。标签目录只读，不提供追新订阅。截图调用未返回可见图像，视觉截图仍未验收；控制台出现既有 React 19 element.ref 兼容提示。启动日志四项旧凭据解密警告在重启前的日志中同样存在，本轮未改写凭据，不将其视为本轮修复范围。
- 2026-09-28 后续 TAG-CATALOG-01：用户反馈占位页面不可见，补齐只读 GET /tags 与 TagListPage。主 Agent 允许新增 backend/internal/ports/tags.go、application/tags.go、platform/database/tags.go、transport/httpapi/tags.go、tags_test.go 及 frontend/src/features/tags/TagListPage.test.tsx 正式源码/测试；修改已有 handler、bootstrap、API types、OpenAPI、README 与本记录。禁止过程目录、业务库写入、订阅功能扩展及无关迁移。数据库内拆分 genres、按影片去重、搜索、排序、分页，不拉全库到浏览器；无新表或结构迁移。Arco 页面复用 InfoCard、PageState、ListPagination。
- 标签目录验证：HTTP SQLite 集成测试覆盖去重、计数、字面百分号搜索、无匹配、越界页、非法分页、鉴权；标签页 3 个测试通过，前端构建通过，后端全量测试及 vet 通过。全前端测试当时 30 通过、1 项已有设置页“馒头”标签多匹配失败，未改无关测试。Apifox 8873619 默认模块根目录新增 GET /tags 并回读参数、Cookie、响应及示例。隔离浏览器实例仅复制 20 部已有影片资料，显示 16 标签，翻页、搜索重置和空结果均核实；截图工具未返回图像，视觉截图未验收。
- 运行边界：本地 dev/bytemuse.db 已有 3246 部影片带 genres，在用库仍是迁移 15，而工作区新增其他任务的迁移 16～19；未擅自重启升级在用实例。需协调这些迁移后再启用在用后端的新 /tags 路由。PostgreSQL/MySQL SQL 已实现但实库未验收；兼容字段的只读聚合仍有数据库扫描开销，未建立持久化标签索引。
- 临时标签页已关闭，截图能力两次均未返回图像；浏览器控制台读取无 error/warn。系统 TEMP 下 bytemuse-tag-page-77795195c79e44dfa004a615df0f44bb 的清理被环境策略拒绝，留有隔离库、可执行文件和日志，不在仓库；未删除用户文件。

- 用户确认临时复用 legacy_media_metadata.genres，不新增标签表；只需部分影片匹配，不做全库扫描或历史批量回填。主 Agent 修改现有 database/collection.go、collection_test.go、ports/collection.go、README 和本记录；不创建新路径，不改旧资料、业务库、页面、调度和订阅下载模块。沿用 COLLECT-02 API，无参数、响应或迁移变化。
- 唯一写入规则 saveCollectionTx 按可靠番号找到实际 media.id，仅追加去空、去重后的标签；已有文本和顺序不重写，空标签不清空；缺少兼容资料行时仅建标签资料，旧状态/模式留空表示未知。来源快照保留原始标签数组。与单影片队列共用可串行化事务，失败回滚该影片的标签和来源快照。
- 真实核查纠正：Netflav 搜索首页 20 条记录均无 tags，详情页才有标签；不把搜索成功表述为已采标签，不自动展开全部详情。明确选少量 source_id 调用已有 detail 入口即可。
- 验证：先复现新影片标签未规范化、已有标签未补充、NULL 和缺失资料行的失败，再通过重复采集、人工字段保留和队列失败回滚回归。真实抓取一页搜索及最多三条详情，在自动清理的隔离 SQLite 库中保存 3 部带标签影片，重复处理两轮无重复增长；在用库写入 0 条。尚未部署或重启在用实例，PostgreSQL/MySQL 实库未验收。

#### CATALOG-TYPE-01：影片类型与所有影片筛选（2026-09-28）

- 用户要求四种影片类型和“所有影片”筛选。单值 video_type 为 censored/uncensored/uncensored_cracked/leaked；NULL 为未分类，历史影片不猜测回填。迁移 15 增加可空 VARCHAR(32)、默认 NULL、值约束和复合索引；数据库升级由正式迁移执行，不手改业务库。
- 主 Agent 负责；允许修改现有 domain/ports/application/collector/database/httpapi 相关源码、AllFilmsPage.tsx、共享 API 类型、OpenAPI、README；允许新增 database/media_type_test.go、httpapi/media_type_test.go、films/AllFilmsPage.test.tsx 正式测试。禁止过程目录、旧源码和原库修改、无关界面调整；其他同时出现的资源选择/订阅下载改动保持不动。
- API 沿用 /media，新增 video_type 单选筛选及响应属性，unknown 查询 NULL；枚举在业务层验证，SQL 参数化过滤、统一总数及分页。采集保存明确提供的类型，JavDB 有码榜新影片写 censored；已有影片分类不覆盖。
- 验证：SQLite 14→15 及重复升级、旧状态不变、四种分类保存与非法类型拒绝、HTTP 组合筛选与分页均通过；浏览器临时真实库 35 条记录，从第 2 页筛出 7 条并重置至第 1 页，搜索组合空结果、未分类、流出均核实，网络请求 video_type=leaked 返回 200。Apifox /media 及 Media 字段、枚举、示例已同步回读。
- 未验收项：PostgreSQL/MySQL 实库未运行；浏览器截图接口未返回图像且 CDP 截图超时，无法完成视觉截图验收；存在既有 React 19 ref 警告。仅临时实例执行迁移，未重启在用实例或部署；全工作区测试受并发新增下载模块中间状态影响，不能据早先通过结果声称最终全量通过。
- 最终本切片相关 database/collector/httpapi/application/bootstrap 测试与 vet、后端 build 通过；前端当时 19 测试及构建通过。临时前后端进程及浏览器标签已关闭；临时 SQLite 目录 bytemuse-types-f2f10ceeff02415a8c1daa557521f074 删除被环境策略拒绝，保留在系统 TEMP，含 35 条虚构验收记录，不影响业务库。

#### COLLECT-03：榜单采集与订阅配置解耦（2026-09-28）

- 用户明确采集不需要填写 RANK_TYPE；旧版 sync_rank 固定采集各榜，RANK_TYPE 仅用于订阅筛选。定时和任务立即执行统一将已接入的 JavDB 有码日、周、月榜全部入队，各榜独立登记；原配置保留。
- 主 Agent 负责；允许修改 bootstrap/collection.go、bootstrap.go、collector/registry.go、README.md 及本记录，允许新增 bootstrap/collection_test.go 长期回归测试；禁止新增过程目录、修改旧源码/原库、变更订阅下载及部署。沿用 COLLECT-02 API，不修改接口或数据库结构。
- 源站核查：JavDB 导航另有无码、欧美、FC2、热播、TOP250、FANZA 奖项和演员月榜；部分入口跳转登录，未验证其完整周期和分页。以上未接入，不把已实现三个周期表述为源站全部榜单。JavLibrary 历史 1～5 缓存及演员榜、旧厂牌榜不等于新版采集能力。
- 回归范围：RANK_TYPE 为空、仅 daily、全部三周期时均登记三个批次及首页队列；单榜登记失败不阻止其余周期。只使用自动清理的临时 SQLite 库，不写业务库。
- 验证结果：先复现空配置入队 0 个、仅 daily 入队 1 个的失败，再修正并通过三种配置与单榜故障回归；go test ./...、go vet ./...、go build ./... 通过。未部署，源站全类型采集仍未完成。

#### COLLECT-02：持久化逐视频采集队列（2026-09-28）

- 用户确认：JavDB 日周月榜采完全部页；视频逐项事务保存；采集后仅异步翻译，不判断订阅、不执行下载。
- 负责人主 Agent；允许新增和修改 backend/internal/{ports,application,platform/database,transport/httpapi,bootstrap}/collection*.go、platform/collector 正式源码测试；修改迁移注册、启动装配、handler.go、README.md、api/openapi.yaml。禁止项目临时产物、过程文档、修改原始旧库、前端无关改动和部署。
- 契约 COLLECT-02：POST /collection/runs 返回 202 与持久化 run_id；GET /collection/runs/{runId} 查询各阶段计数及错误。任务按分页、视频、翻译三阶段恢复；租约及令牌防止过期消费者提交；失败有限重试。榜单按批次留存，完整非空且视频均成功后短事务切换当前榜单，失败视频不回滚已保存影片。
- 实施与验收：先测试持久化重启恢复、重复领取和单视频失败隔离；复用唯一资料保存规则，将翻译任务与视频保存同事务登记；再测试 100 页以上榜单、循环页失败、旧批次不覆盖新榜、空榜保留、取消恢复；最后鉴权 API、调度、全量测试、构建及 Apifox 同步回读。历史影片属性与订阅下载状态保持不变。
- 状态：已实现并通过本地集成验证，未部署。迁移 14、持久化三阶段队列、202 受理与状态 API、单视频事务、缺失译文后台任务、完整榜单切换均已接入；不判断订阅、不下载。
- 最终检查：go test ./...、go vet ./...、go build ./... 和 git diff --check 通过；OpenAPI 与 Apifox 已同步 202 契约、新增状态查询并回读字段、鉴权、示例；原有无关改动保留，未启动在用实例、未执行正式库迁移。
- 验证：102 页榜单回归、分页失败与循环页、坏视频不影响成功视频、租约恢复/过期提交拒绝、较旧批次防覆盖、翻译失败三次止重试/人工译文保护、后台启动停止、13→14 升级与重复迁移、鉴权 API 状态查询均已测试；真实 Netflav 队列处理 20 视频、116 演员，未写在用库。JavDB 实际访问仍受验证限制；PostgreSQL/MySQL 实库及真实付费翻译未验收。新增进度接口无前端页面，历史批次目前保留不自动清理。

#### COLLECT-01：首批站点元数据采集（2026-09-28）

- 已获用户确认：统一采集接口及 AVBase、JavDB、JavBus、Netflav 首批适配；JavLibrary、厂牌、PT 站及 Avgle、ThisAV、Jable、SupJav 继续列入后续范围。
- 负责人：主 Agent 集成；站点解析与数据库实现按独立文件并行。允许创建 backend/internal/ports/collection.go、application/collection*.go、platform/collector/ 下正式源码和测试、platform/database/collection*.go、transport/httpapi/collection*.go、bootstrap/collection*.go；允许修改启动注册、依赖、权威迁移及 api/openapi.yaml、README.md。禁止创建过程文档、临时 SQL、仓库内调试产物，未加密代码/ 保持只读。
- 契约 COLLECT-01：显式 POST /collection/runs 单页采集，GET /collection/sources 查询能力；查询界面原 GET 不产生隐式外站写入。来源快照 source + source_id 唯一；可靠番号才建立媒体记录，采集不得覆盖既有媒体元数据、订阅日期及业务状态。
- 实施顺序：冻结 ports 契约与公共请求错误分类；并行核实四站解析和事务仓储；接入已鉴权 API 与榜单调度；验证解析异常、空结果、重复执行、迁移升级、历史数据保护；同步 OpenAPI 和 Apifox 并回读。
- 验收：脱敏结构夹具和 HTTP 受控响应测试、SQLite 独立库事务及迁移测试、真实外站低频只读核验。外站验证页返回明确失败，不作为空结果；不获取视频流、不调用下载器、不回填历史库。
- 当前状态：部分实现并测试，未完成四站整体交付。统一接口、来源快照迁移 13、事务入库、JavDB search/rank 与 Netflav search/detail 已实现；AVBase、JavBus 返回验证页，未发布未经核实的解析能力。
- 2026-09-28 核验：Netflav 的 Go 实际请求搜索返回 20 条，详情可读演员和发行日期；JavDB PowerShell 请求可读列表，但 Go 实际请求返回 source_blocked，不能标为真实采集通过。原始旧库和在用实例未改动，未部署；SQLite 独立库验证幂等、历史状态保护、空榜保留、坏批次回滚、12 至 13 升级与 API 入库后列表回查。PostgreSQL/MySQL 尚未进行真实数据库验收。
- 交付证据：go test ./...、go vet ./...、go build ./... 通过；真实 Netflav 搜索在临时 SQLite 库保存 20 条来源、20 条影片、116 条去重演员。两个采集接口已同步 Apifox 项目 8873619 并回读路径、Cookie、参数、错误响应和示例。未增加前端采集操作入口，暂由已鉴权 API 调用。独立审查 Agent 未收到任务正文，本次仅完成主 Agent 代码审查，不能视为独立审查通过。

新增项目级任务时在本节下方按以下字段记录；完成后保留关键决策和验收结论，移除无长期价值的过程细节：

#### DOWNLOAD-01：订阅资源搜索下载（2026-09-28）

- 目标：活动订阅按番号从 PT 与 BT 搜索资源、复用统一筛选规则、持久化任务并提交下载器；BT 使用 sukebei.nyaa.si，产品名 Nyaa BT。
- 当前实现：PT 已接入 M-Team，BT 已接入 Nyaa BT；下载器仅 qBittorrent。手动入队接口和 `DOWNLOAD_SCHEDULE_TIME` 调度共用持久化流程；配置在每次处理批次读取。submitted 仅表示下载器按 info hash 回查到任务；模糊结果保持 unknown，不自动重投。
- 验证边界：隔离 SQLite、受控 HTTP 的 PT/BT 解析、选种、提交、重启恢复和鉴权 API 测试通过；Nyaa RSS 只读请求返回条目。真实 M-Team API 结构、私有种子与 qB 端到端提交，以及 PostgreSQL/MySQL 实库迁移尚未验收；未改在用数据库、未部署。其他 PT 站及 Transmission、aria2、迅雷下载提交未接入。

- 任务编号与业务目标：
- 负责人及角色：
- 参照证据与目标差异：
- 范围与排除项：
- 契约版本及允许修改范围：
- 上游依赖、下游消费者和并行条件：
- 正常、异常和恢复验收场景：
- 外部操作与历史数据授权范围：
- 当前状态：未开始、已实现、已测试、已联调、已验收或阻塞。
- 验证证据、未验证事项和剩余风险：

#### MESSAGING-01：Telegram 与企业微信对话、菜单与 Agent（2026-09-29）

- 目标：外部渠道（Telegram、企业微信）实现斜杠命令菜单、直接发送番号订阅与 OpenAI function calling Agent 对话；优先 Telegram。
- 负责人：主 Agent 冻结共享契约与启动装配；Telegram 适配、企业微信适配、Agent 工具集按独立文件并行实现。
- 允许新增：backend/internal/ports/messaging.go、internal/application/agent/、internal/platform/telegram/、internal/platform/wechat/、internal/transport/httpapi/channels.go、internal/bootstrap/channels.go 及配套测试；允许修改 handler.go 路由与依赖、bootstrap.go 装配、api/openapi.yaml。前端设置页字段已存在，未改动前端。
- 契约 MESSAGING-01：InboundMessage{Channel,ChatID,UserID,Text} 为渠道统一入口，ChannelSender 为统一发送出口，CallbackVerifier 负责企业微信 GET 校验与 POST 解密，ChannelMessageHandler 由长轮询与 HTTP 回调共用。业务分流只在 application/agent 的 Router 实现一次：斜杠命令 → Agent 对话 → 番号订阅 → 未启用指引。
- 渠道行为：Telegram 长轮询 getUpdates，启动或配置变化时注册 setMyCommands 菜单，白名单非空时拒绝名单外用户；企业微信走 /api/v1/message 回调，不要求登录会话，安全性由回调签名与 AES 解密保证，先返回空响应再异步处理以满足 5 秒响应时限。
- 配置：统一读取 app_settings 的 TELEGRAM_*、WECHAT_*、OPENAI_*、AGENT_ENABLE；保存设置后无需重启即可生效（渠道监督器每 15 秒对齐，回调处理器每次请求实时解析）。
- 当前状态：已实现，通过本地单元测试与受控 HTTP 集成验证；未做真实第三方联调，未部署。
- 验证证据：go build ./...、go vet ./...、go test ./... 全部通过。Telegram 与企业微信适配层单测覆盖长消息分片、access_token 缓存与刷新、签名与 AES 解密边界、picurl 降级；新增 Dispatcher 端到端测试覆盖「入站消息 → 模型 → 工具 → 最终回复 → 渠道投递」、send_message 主动推送、斜杠命令菜单与未启用降级；新增 HTTP 回调测试覆盖未配置 503、GET 校验原样回写、POST 解密投递与篡改拒绝；新增装配测试覆盖发送器解析与凭据变更重建、设置驱动的回调处理器、白名单解析与未配置空转。OpenAPI 与 Apifox（项目 8873619）已同步 /message 的 GET/POST 两个接口和 AGENT_ENABLE 字段并回读确认。
- 未验证事项与剩余风险：无真实 Bot Token 与企业微信 corpid/secret/agentid，真实 Telegram、企业微信端到端链路未联调；Agent 工具的数据库读写仅经假仓储验证。2026-09-29 12:39 已重启本地后端，未配置时 GET /api/v1/message 实测返回 503，路由已在运行实例中生效。

#### ACTOR-FOLLOW-01：演员追新订阅（2026-09-29）

- 目标：演员保存或修改限制日期后，用本地已入库影片按旧版规则自动建立追新订阅，并由「同步热门演员」定时任务持续补抓作品。
- 规则来源：未加密代码/最终源码/app/services/__init__.py 的 subscribe_code_by_actor（casts 非空、release_date 严格晚于 actor.limit_date、番号不含 VR、该片演员数不超过 MAX_ACTOR、服务器无此文件）。旧源码保持只读。
- 允许修改：新增 backend/internal/platform/database/actor_follow.go、actor_follow_test.go、backend/internal/bootstrap/actor.go；修改 ports/actor.go、ports/collection.go、application/actor.go、transport/httpapi/handler.go、platform/database/migrations.go、collector 适配与测试、application/agent 假仓储、api/openapi.yaml 及本记录。无前端改动，无历史数据回填。
- 契约：PUT /actors/{actorName}/subscription 保存后立即追新，追新未完成返回 500 actor_follow_pending；POST /tasks/同步热门演员/run 触发 actorFollowJob。AVBase 采集新增 kind=actor（/talents/{name}?page=N）。迁移 22 新增台账 actor_subscription_matches(actor_name,media_id,processed_at)。
- 实现要点：ActiveNames 取 limit_date 非空演员逐个入队 avbase/actor 采集；Follow 分批（LIMIT 200）取候选，逐影片在 Serializable 事务内锁 actors/media 行、复核受保护状态与 library_status 后按 strict 订阅写入并登记台账，因此重复执行幂等且不会恢复用户手动取消的订阅；MAX_ACTOR 缺失或非法回退 3，0 表示不限制演员数。
- 验证证据：go test ./...、go vet ./...、go build ./... 通过；actor_follow_test 覆盖严格晚于截止日、VR 排除、演员数上限、重复执行幂等、退订不恢复、放宽上限只补新增、旧库升级与历史数据不变。
- 实网验收：dev/bytemuse.db 应用迁移 22 与 23 后重启本地后端，手动触发「同步热门演员」；47 个 avbase/actor 采集批次全部 done，台账 753 行、按规则新增 strict 订阅 197 条（活动订阅 10511→10708），入库影片 46949→47218。触发前在线备份位于系统 TEMP/bytemuse-backup-20260929-123838.db。
- 未验证与剩余风险：PostgreSQL/MySQL 未做真实库验收；AVBase 演员作品分页上限与源站改版未评估；本次执行按功能预期在开发库新增订阅，生产库执行前需确认 MAX_ACTOR 与订阅范围。

#### TRANSLATION-FILL-01：翻译设置迁移冲突与补全写锁修复（2026-09-29）

- 现象一：演员追新与翻译模型拆分两个并行任务同时占用迁移版本 22，执行器按版本号判断是否已应用，导致 dev/bytemuse.db 已记录 22=actor_subscriptions 后 translation_model_settings 被静默跳过，TRANSLATION_OPENAI_URL/MODEL/API_KEY 三个设置键从未创建。
- 现象二：译文补全命令在 12:41 与 12:45 两次以 save translated title: database is locked (5) (SQLITE_BUSY) 整批中断，剩余 34134 条未翻译。
- 处理：保留已应用的 actor_subscriptions=22，翻译迁移改为 23；新增 backend/internal/platform/database/migrations_test.go 断言三个方言的迁移版本号唯一且严格递增；BackfillTranslations 的逐条写入改为有界退避重试（最多 6 次、0.5s 递增），并新增 isWriteConflict 分类与单测。
- 验证：副本升级 21/22→23 通过且重复执行幂等，三个翻译设置键按 TRANSLATION_ENGINE=openai 从 OPENAI_* 复制；开发库升级后 GET /api/v1/system/settings 实测返回同一 URL 与模型；重启后的补全进程在采集并发写入下持续运行无中断。go test ./...、go vet ./...、go build ./...、gofmt 全部通过。
- 未验证与剩余风险：译文补全仍在进行（重启时 13153/47218），未全部完成；SQLITE_BUSY 重试只覆盖译文写入路径，其他 CLI 写操作仍会直接失败；PostgreSQL/MySQL 未验证该重试分支。

#### SETTINGS-01：翻译模型与对话 Agent 配置分离（2026-09-29）

- 目标：把翻译使用的 OpenAI 兼容接口、模型、密钥与 Prompt 从对话 Agent 的 OPENAI_* 中拆出，在「AI 模型」分组内用页签分别配置，使 Agent 可换用支持 function calling 的模型，而翻译继续使用原模型。
- 允许修改：application/settings.go 新增 TRANSLATION_OPENAI_URL、TRANSLATION_OPENAI_MODEL、TRANSLATION_OPENAI_API_KEY 三个键；platform/database/migrations.go 新增迁移 23；bootstrap.go 翻译装配改读新键；前端 SettingsPage 增加分组内页签与样式；api/openapi.yaml 与本文档同步。对话 Agent 的 OPENAI_* 语义与既有翻译引擎选择未变。
- 契约 SETTINGS-01：翻译引擎取 openai 时使用 TRANSLATION_OPENAI_*，其余引擎不受影响；对话 Agent 始终使用 OPENAI_*；两套配置互不覆盖。设置页页签只决定展示哪组字段，草稿、变更判断与保存始终覆盖整个分组。
- 迁移 23（translation_model_settings）：仅当 TRANSLATION_ENGINE='openai' 时把当前生效的 OPENAI_URL、OPENAI_MODEL、OPENAI_API_KEY 复制为 TRANSLATION_OPENAI_*，保证升级前后翻译行为不变；目标键已存在时不覆盖，重复执行安全；API Key 与 Agent 共用 SESSION_SECRET 加密，直接复制密文。
- 当前状态：已实现，后端与前端测试通过，已在真实 dev 库与真实浏览器完成联调；未部署。
- 验证证据：go build ./...、go vet ./...、go test ./... 通过；npm run check:standards、npm test、npm run typecheck、npm run build 通过。dev/bytemuse.db 已应用迁移 23 并复制出 TRANSLATION_OPENAI_*（地址与模型同 OPENAI_* 一致，密钥密文长度一致）。真实浏览器在 390/576/768/1280/1920px 验证「AI 模型」两个页签切换、字段正确、两个测试按钮分别返回「OpenAI 连接成功」；实测只改翻译模型名保存后 OPENAI_MODEL 不变，随后已还原原值。
- 未验证事项与剩余风险：翻译服务在启动时装配（collectionService.SetTranslator 仅允许后台消费者启动前调用），修改翻译配置需重启后端才用于采集自动翻译，已在翻译模型页签标注；对话 Agent 配置每次请求实时读取，无需重启。OpenAPI 已同步；当前环境无可用 Apifox MCP 工具，未同步 Apifox。

#### CRON-SCHEDULE-01：定时任务 cron 恢复每日执行并新增保存校验（2026-09-29）

- 现象：dev/bytemuse.db 的 RANK_SCHEDULE_TIME、ACTOR_SCHEDULE_TIME、DOWNLOAD_SCHEDULE_TIME 被写成 `* * 5 * *`、`* * 3 * *`、`* * 2 * *`。5 段 cron 是「分 时 日 月 周」，这三个值表示每月 5/3/2 日全天每分钟触发，与旧版 template.env 默认的每天 20:00/21:00/22:00 不一致；表达式本身合法，所以调度器启动不报错，属于静默语义错误。TAG_SCHEDULE_TIME 仍为默认 `30 21 * * *`，未被改坏。
- 处理一（校验）：internal/application/settings.go 新增 settingCron 取值域，四个 *_SCHEDULE_TIME 由 settingText 改为 settingCron，保存时用 github.com/robfig/cron/v3 的 ParseStandard 校验标准 5 段表达式（与 internal/scheduler 的解析器一致）；非法表达式返回 400 invalid_setting，空值仍表示不注册该定时任务。此前任意字符串都能写入，一旦写入非法值，下次启动 scheduler.New 注册失败会导致后端起不来。
- 处理二（数据）：通过 PUT /api/v1/system/settings 将三个键恢复为 `0 20 * * *`、`0 21 * * *`、`0 22 * * *`，TAG_SCHEDULE_TIME 保持 `30 21 * * *`。修改前用 VACUUM INTO 在线备份到系统 TEMP/bytemuse-backup-cronfix-20260929-131229.db。
- 处理三（前端）：SettingsPage 定时任务分组为四个 cron 字段补充「5 段 cron（分 时 日 月 周）」格式说明与示例。
- 处理四（契约）：api/openapi.yaml 的 SystemSettings 与 SystemSettingsUpdate 两个 Schema 补充四个键的类型、默认值、示例与取值说明；Apifox 项目 8873619 的 316776654、316817665 已同步并回读确认。
- 验证证据：go test ./...、go vet ./...、go build ./...、gofmt -l internal cmd 全部通过；npm run check:standards、npm test（75 个）、npm run typecheck、npm run build 通过；重启本地后端后 GET /api/v1/tasks 返回 cron 为 同步榜单 `0 20 * * *`、同步热门演员 `0 21 * * *`、标签追新 `30 21 * * *`、订阅下载 `0 22 * * *`、清理系统日志 `0 0 * * *`；PUT 非法值 `99 99 * * *`、`* * 5 *`、`not a cron` 均返回 400 invalid_setting，合法值返回 200。
- 未验证与剩余风险：本次只在 SQLite 开发库验收，PostgreSQL/MySQL 未单独验证该校验分支。定时任务原先在启动时按设置注册，修改 cron 后必须重启后端才生效，该限制已由 CRON-SCHEDULE-02 解除。

#### CRON-SCHEDULE-02：定时任务保存后即时重排，无需重启后端（2026-09-29）

- 现象：定时任务只在 bootstrap 的 scheduler.New 时按设置注册，PUT /api/v1/system/settings 保存 *_SCHEDULE_TIME 后进程内排期不变，必须重启后端才生效；设置页也标注了该限制。
- 处理一（调度器）：internal/scheduler/scheduler.go 把 Job.Spec 语义改为「初始表达式」，空值表示只注册不排期；jobState 新增 spec 与 entryID；新增 Manager.Apply(specs) 运行时增量重排，先整批校验未知任务名与 cron.ParseStandard 表达式，任一项失败整批拒绝且不改动现有排期；空值或未出现在 specs 中的任务取消排期，但保留 lastRun/running 状态与 RunNow 能力。Tasks() 改为返回当前排期。
- 处理二（设置）：internal/application/settings.go 新增 ScheduleApplier 回调与 SetScheduleApplier；Update 在落库并 Get 后调用回调，同步失败只记录警告日志，不把已成功的保存报成失败。
- 处理三（装配）：internal/bootstrap/bootstrap.go 的 scheduleDefinitions 成为设置键与任务名的唯一映射，configuredJobs 不再携带静态表达式，新增 scheduleSpecs 生成期望排期；scheduler.New 后立即 Apply 一次，并把 manager.Apply 注入 settingsService。日志清理是固定任务，用 logCleanupTaskName / logCleanupSpec 常量始终包含在 scheduleSpecs 中，避免被 Apply 当作未配置而取消排期。
- 契约 CRON-SCHEDULE-02：Apply 是幂等的全量对齐，表达式未变化时不重建 cron entry；四个 *_SCHEDULE_TIME 空值表示不排期，非法表达式保存即返回 400 invalid_setting；日志清理固定 `0 0 * * *`，不通过设置修改。
- 验证证据：gofmt -l internal cmd 无输出；go build ./...、go vet ./...、go test -count=1 ./... 通过；新增 internal/scheduler 的 TestApplyReconcilesSpecs、TestApplyWorksWhileRunning，internal/application 的 TestSettingsUpdateNotifiesScheduleApplier，internal/bootstrap 的 TestScheduleSpecsAlwaysIncludesLogCleanup、TestScheduleReconcileKeepsLogCleanup。重启本地后端后 GET /api/v1/tasks 基线为 同步榜单 `0 20 * * *`、同步热门演员 `0 21 * * *`、标签追新 `30 21 * * *`、订阅下载 `0 22 * * *`、清理系统日志 `0 0 * * *`；PUT RANK_SCHEDULE_TIME=`*/5 * * * *` 后不重启立即 GET /api/v1/tasks，同步榜单变为 `*/5 * * * *`，其余任务与清理系统日志不变；PUT `99 99 * * *` 与 `0 20 * *` 均返回 400 invalid_setting 且排期不变；恢复 RANK_SCHEDULE_TIME=`0 20 * * *` 后 tasks 立即回到基线，数据库四个键与修改前一致。api/openapi.yaml 的 SystemSettings 与 SystemSettingsUpdate 已同步「保存后立即重排，无需重启后端」文案，Apifox 项目 8873619 的 316776654、316817665 已写入并回读确认。
- 未验证与剩余风险：只在 SQLite 开发库与本地源码运行验收，PostgreSQL/MySQL 未单独验证该重排分支；调度同步失败时只记警告，仍靠重启按落库设置重新注册兜底。

#### NOTIFY-01：消息渠道按渠道独立的通知与对话开关（2026-09-29）

- 目标：在设置页「消息渠道」的「微信」「Telegram」两个分组末尾各增加 6 个开关（两渠道各自独立），后端按开关推送业务通知或控制该渠道的自然语言对话；开关横向排成一行，名称按用户确认去掉「通知」二字，两个分组底部均注明「该设置只针对于微信、TG」。
- 允许修改：新增 backend/internal/application/notification.go 及 notification_test.go、subscription_notify_test.go、platform/database/notification_settings_test.go、platform/database/transfer_transition_test.go、application/agent/router_test.go；修改 application/settings.go、application/services.go、application/subscription_download.go、application/agent/{router,business,tool}.go、ports/{messaging,subscription_download}.go、platform/database/{migrations,subscription_download}.go、bootstrap/bootstrap.go、transport/httpapi/download_control_test.go、frontend/src/features/system/SettingsPage.tsx 及 SettingsPage.test.tsx、frontend/src/app/styles.css、api/openapi.yaml 及本记录。禁止新增其他项目路径和临时产物。
- 契约 NOTIFY-01：开关键 = 渠道前缀 + 事件后缀，前缀取 WECHAT / TELEGRAM，后缀取 NOTIFY_SUBSCRIBE、NOTIFY_SUBSCRIBE_FAILED、NOTIFY_DOWNLOAD_START、NOTIFY_DOWNLOAD_COMPLETE、NOTIFY_DOWNLOAD_FAILED、NOTIFY_AGENT_CHAT，共 12 个；权威定义在 application/notification.go 的 notificationChannels 与 notificationEvents，设置页、迁移种子与推送实现共用同一份定义，渠道与事件顺序即界面顺序。
- 默认值与语义：5 个推送类开关默认 false，升级后不会在用户未确认的情况下推送；NOTIFY_AGENT_CHAT 默认 true，表示该渠道是否允许自然语言对话，关闭后回复关闭提示，斜杠命令与直接发送番号订阅不受影响。推送目标取 WECHAT_TO_USER 与 TELEGRAM_CHAT_ID，目标为空或渠道未配置时跳过；通知失败只记录日志，不影响业务流程。
- 实现要点：Notifier 是通知唯一出口，NotificationService 复用 channelRegistry.Sender，不另建发送逻辑；订阅成功/失败在 SubscriptionService.Create 内触发（失败文案含原因，与落库原因同源），开始下载在提交下载器成功后触发，下载完成与下载失败由 SaveTransferStates 的 transfer_status 跃迁触发（仅当旧值不等于新值且新值为 completed/failed 时通知一次，重复同步不重复通知）。
- 迁移 24（channel_notification_settings）：仅新增 12 个设置键，不改表结构、不覆盖已有配置；MySQL 用 INSERT IGNORE，SQLite/PostgreSQL 用 ON CONFLICT DO NOTHING，重复执行安全。
- 前端：SettingField 增加 section 分节标题与 inline 行内标记、SettingGroup 增加 note 分组备注；连续标记 inline 的字段由 renderFieldSequence 合并进同一个 .settings-inline-row 容器横向排列，空间不足时自动换行并保持左对齐，行内与单行开关统一沿用页面既有的「名称在左、开关在右」写法；设置键后缀仍为 NOTIFY_*，只有界面名称去掉「通知」；控件沿用页面既有 Arco Switch，未引入新控件类型；分组备注移出字段网格改为分组级元素，与上一行间距按字段说明的 8px 取值。
- 验证证据：gofmt -l 无输出，go build ./...、go vet ./...、go test -count=1 ./... 全部通过；npm run check:standards（11 项）、npm test（75 项）、npm run typecheck、npm run build 通过。dev/bytemuse.db 已应用迁移 24 并 seed 12 个键且默认值正确；重启本地后端后浏览器在 390/576/768/1280/1920px 确认「消息」小标题、6 个开关与底部备注渲染、无横向溢出；用户真实视口下 6 个开关同处一行（表单列 720px，实际占用 669px），窄列自动换行为 2–3 行且左对齐；备注与开关行间距实测 8px，与站点设置表单的字段说明一致；真实保存往返验证微信分组开关写回数据库、Telegram 分组保持独立，验收中改动的开关已恢复默认值。
- 未验证与剩余风险：未主动触发订阅或下载以产生真实渠道推送，第三方通知端到端未联调；api/openapi.yaml 的 SystemSettings 与 SystemSettingsUpdate 已同步 12 个键，当前环境无可用 Apifox MCP 工具，未同步 Apifox；迁移只在 SQLite 开发库验收，PostgreSQL/MySQL 未做真实库迁移；推送类开关默认关闭，需用户在设置页手动开启后才会推送。

#### TRANSLATION-GUARD-01：OpenAI 翻译引擎未配置时的禁用与回落（2026-09-29）

- 目标：「AI 模型 → 翻译模型」的接口、模型、密钥未填全（Prompt 除外）时，翻译分组的 OpenAI 选项不可选；若此前已选 openai 再删除翻译模型配置，翻译引擎自动回落默认值。
- 契约 TRANSLATION-GUARD-01：OpenAI 翻译依赖 TRANSLATION_OPENAI_URL、TRANSLATION_OPENAI_MODEL、TRANSLATION_OPENAI_API_KEY，三者任一为空即视为未配置，TRANSLATION_PROMPT 不参与判定；该判定与后端 NewTranslationService 的 openai 分支及 bootstrap.translationConfigFromSettings 一致。前端只做展示与即时反馈，后端装配判定仍是唯一权威。
- 允许修改：frontend/src/features/system/SettingsPage.tsx 及 SettingsPage.test.tsx、api/openapi.yaml 及本记录。无后端改动，无迁移，无历史数据回填。
- 实现要点：SettingOption 新增 requires 依赖键；TRANSLATION_ENGINE 的 openai 选项声明 translationOpenAIKeys，渲染时用 hasRequiredValues 决定 disabled。applyTranslationEngineGuard 统一收敛不变式——引擎为 openai 且依赖不全时回落 none；草稿同步（服务端返回值）与单字段编辑（setValue）都经过它，因此删除任一项配置后引擎立即回落。buildPayload 增加跨分组提交：引擎值与基线不一致时一并写入 payload，保证在「AI 模型」分组删除翻译模型配置并保存时，回落结果随该分组一起落库，不会出现设置值与实际装配不一致。
- 语义说明：界面上「关闭」对应 none，即用户口语的「自动/默认」；未配置齐全时回落 none，与后端 none（不启用翻译）一致。
- 顺带修复：布尔字段的 .settings-toggle-row 此前实际渲染为开关在左、文字在右，本次把 label 移到 Switch 之前，与「文字在左」的既有约定及消息渠道开关保持一致；文字点击经 label 的 htmlFor 仍能切换开关。
- 验证证据：npm run check:standards（11 项）、npm test（78 项）、npm run typecheck、npm run build 全部通过。真实浏览器在设置页验证：三项齐全时 OpenAI 可选且选中；逐项清空模型名称 / 接口地址 / API Key 时 OpenAI 立即 disabled、none 选中，三种清空路径均通过；点「重置」还原后 OpenAI 恢复选中。dev/bytemuse.db 的 app_settings 翻译配置未被改动。同源 iframe 探针在 390/576/768/1280/1920px 确认无横向溢出。
- 未验证与剩余风险：api/openapi.yaml 已补充该语义描述，当前环境无可用 Apifox MCP 工具，未同步 Apifox；翻译服务在启动时按设置装配，改动翻译配置后需重启后端才对采集翻译生效；回落只在设置页与后端装配两层保证，直接改数据库写入非法组合仍会由后端报 incomplete。

#### CARD-01：渠道番号卡片与按钮操作（2026-09-29）

- 目标：用户在消息渠道发送番号后，先收到一张「上方封面、下方操作按钮」的卡片，按钮随当前状态变化（已入库未订阅显示订阅、下载中显示暂停、已暂停显示继续、失败显示重试），点击按钮才执行对应操作；番号与按钮这条链路不接入 AI。
- 契约 CARD-01：按钮载荷统一为 bm:<动作>:<番号>，动作集合为 sub / unsub / dl / pause / resume / retry，标识使用归一化番号而不是内部 ID，重复点击或过期卡片只会按番号重新取当前状态。按钮与状态映射的唯一权威是 agent.cardButtons；按钮是否允许由既有 task.AvailableActions（application.downloadActions）判定，卡片不重复实现下载器状态规则。回复统一为 ports.Reply{Text, Card, Refresh}，由 agent.Dispatcher 投递；卡片投递失败时降级为文本并回到「直接订阅」的原有行为（Router.CardFailed）。已入库番号在 Agent 开启时也优先返回卡片，未入库的类番号文本保持原有分流（交给 Agent 对话或给出提示），避免把普通英文文本误判成番号。
- 允许修改：新增 backend/internal/ports/messaging.go、internal/application/agent/card.go、internal/platform/telegram、internal/platform/wechat、internal/bootstrap/channels.go、internal/transport/httpapi/channels.go 及各自测试；修改 internal/application/agent/router.go、dispatcher.go、services.go、internal/platform/database/repository.go、internal/ports/repositories.go、internal/bootstrap/bootstrap.go、api/openapi.yaml 和本记录。无数据库迁移，无历史数据回填，前端无改动。
- 渠道能力：Telegram 使用内联键盘（reply_markup.inline_keyboard）与 callback_data，每行两个按钮，按钮点击经 callback_query 更新送回（getUpdates 的 allowed_updates 已加入 callback_query），并调用 answerCallbackQuery 结束客户端加载态；动作执行后用 editMessageReplyMarkup 刷新原消息按钮，封面缺失或 sendPhoto 被拒时降级为带按钮的 sendMessage，caption 按 1024 rune 截断。企业微信使用模板卡片（msgtype=template_card、card_type=button_interaction，按钮 1–6 个），按钮点击以 Event=template_card_event、EventKey=按钮载荷回调；平台更新已发送卡片需要发送时返回的 response_code，而点击回调不携带该值，因此 ReplaceButtons 在该渠道按接口约定返回 nil，不额外维护映射表。
- 实现要点：ports.ChannelSender 增加 SendCard 与 ReplaceButtons；ports.InboundMessage 增加 Action（按钮点击时 Text 为空）；ports.DownloadListQuery 增加 MediaID，DownloadService 增加 LatestForMedia，使卡片能按「某部影片的最近一条下载任务」给出可执行操作；Telegram 轮询器不再自行发送回复，改为持有 ports.ChannelMessageHandler，与 HTTP 回调共用同一个 Dispatcher，业务分流与投递只实现一次；通道开关 NOTIFY_AGENT_CHAT 只约束自然语言对话，番号卡片不受其影响。
- 验证证据：gofmt -l 无输出；go build ./...、go vet ./... 无输出；go test -count=1 ./... 全部通过（含 agent 卡片用例：载荷往返与非法载荷拒绝、七种状态下的按钮映射、卡片与文本降级、订阅/取消订阅/暂停的动作执行与按钮刷新；telegram：sendPhoto+内联键盘、封面失败降级、caption 截断、editMessageReplyMarkup、answerCallbackQuery、callback_query 解析与回执；wechat：模板卡片请求体、缺少封面、按钮数量越界、文案截断、ReplaceButtons 空操作、模板卡片事件解析）。按钮动作走的是既有 DownloadService.Control 链路，暂停用例断言下载器实际收到 pause 且回查状态写回后按钮刷新为「继续」。
- 未验证与剩余风险：真实 Telegram / 企业微信联调未执行，需要用户向机器人发送番号、企业微信侧配置回调后才能确认端到端；企业微信模板卡片在真机上未验证，且点击后无法刷新按钮（平台限制，按钮可能显示过期状态，重复点击只按番号重新取当前状态）；在用后端未重启，源码改动需重启后才生效；本机无 gcc，-race 无法运行；api/openapi.yaml 已同步 /message 的按钮事件语义，当前环境无可用 Apifox MCP 工具，未同步 Apifox。
#### NOTIFY-02：推送封面与防剧透设置接线（2026-09-29）

- 现象：「消息渠道」里的 Telegram「推送防剧透」和微信「微信封面推送」保存后没有任何可观察效果。
- 根因（代码确认）：TELEGRAM_SPOILER 只有设置项声明、设置页标签与迁移默认值三处，全仓库没有任何消费方；WECHAT_BANNER 只在 wechat.Client.SendPhoto 里被读取，而 NotificationService.Notify 只调用 SendText，推送链路从不发图片，SendPhoto 实际是死代码，开关自然无效。
- 对标语义（未加密代码/最终源码）：推送一律是「封面 + 文案」的图文消息；TG 用 has_spoiler=TELEGRAM_SPOILER 让封面先在客户端打码，微信用 picurl = 本次封面 if WECHAT_BANNER else WECHAT_PHOTO（WECHAT_PHOTO 即「推送横幅图地址」）。
- 契约 NOTIFY-02：通知载荷改为 application.NotificationMessage{Text, CoverURL}；有封面走渠道 SendPhoto，没有封面回落 SendText，发送方式只在 application.sendNotification 分流一次，渠道差异由适配器承担。封面取值规则只有一处：Go 侧 application.MediaCover（横幅图优先、缺失回退海报图），SQL 侧 platform/database.mediaCoverColumn 与之同名同义，用于下载任务查询。TG 的 has_spoiler 只作用于推送图文消息，交互式番号卡片不打码（用户主动发送番号，卡片封面不应被遮挡）。
- 允许修改：internal/application/notification.go、services.go、subscription_download.go、internal/ports/subscription_download.go、internal/platform/database/repository.go、subscription_download.go、internal/platform/telegram/client.go、internal/bootstrap/bootstrap.go、channels.go 及各自测试；api/openapi.yaml 未改（/system/settings 未逐键声明这两个键，无契约变化）。无数据库迁移，无历史数据回填，前端无改动。
- 实现要点：SubscriptionDownloadAttempt / PendingSubmission / TransferTransition 增加 Cover，三条任务查询（SaveTransferStates 的 current、Claim、ClaimPending）补 mediaCoverColumn("m") 并连接 legacy_media_metadata（以 media_id 为主键，一次主键查找）；telegram.NewClient 增加 spoiler 参数，渠道注册表把 TELEGRAM_SPOILER 纳入发送器签名，保存设置后无需重启即可生效；轮询器只读更新与回执，不携带防剧透配置。
- 顺带修掉的真实缺口：sqlSubscriptionRepository.Create 过去只返回订阅行，不带 Media，而订阅成功通知的标题与封面都取自 item.Media，因此线上那条推送实际只有番号、没有标题也没有配图（测试用替身返回了 Media，所以此前没暴露）。现在 Create 与 List 一样调用 attachMedia 带出影片快照；订阅此时已提交，快照只影响文案与配图，读取失败只记日志，不把已成功的创建报成失败。POST /subscriptions 的响应因此与 GET /subscriptions 一样包含 media，属既有字段的补齐。
- 验证证据：gofmt -l internal cmd 无输出；go build ./...、go vet ./... 无输出；go test -count=1 ./... 全部通过（新增：TestNotifySendsPhotoOnlyWhenCoverPresent 覆盖「有封面走图文、无封面走纯文本」、TestMediaCoverPrefersBannerThenPoster 覆盖横幅优先/回退海报/空白横幅/都缺失、TestSendPhotoAppliesSpoilerSetting 断言开启时请求体带 has_spoiler=true 且关闭时不含该字段、TestChannelRegistryRebuildsTelegramSenderOnSpoilerChange 断言开关变化后重建发送器、TestSubscriptionCreateReturnsMediaSnapshot 断言创建订阅带出影片快照；transfer_transition 与 subscription_download 两个真实 SQLite 用例新增封面断言，覆盖横幅优先与海报回退，并顺带验证 legacy_media_metadata 连接可用）。订阅通知用例已断言封面随通知下发。
- 未验证与剩余风险：真实 Telegram / 企业微信推送未联调（需要用户侧机器人与企业微信应用凭据）；封面按 URL 交给平台抓取，源站若拒绝平台抓图会降级为纯文本（TG 与微信适配器都会回落，不会丢文案）；本机无 gcc，-race 无法运行；当前环境无可用 Apifox MCP 工具，未同步 Apifox。

#### NOTIFY-03：图文消息标题必填与企业微信推送取证（2026-09-29）

- 现象（NOTIFY-02 之后复检）：Telegram 推送已能带封面，但企业微信一侧仍可能表现为「开关没作用」。
- 根因（代码确认）：NotificationMessage 只有 Text，sendNotification 把 SendPhoto 的 title 传成空串；企业微信 news 消息的 articles[].title 是必填项，空标题会被平台拒绝整条图文消息。对标实现里标题始终非空（番号{code}已加入订阅列表 / 番号{code}开始下载 / 番号{code}已完成下载），正文才是影片标题或站点信息。
- 契约 NOTIFY-03：NotificationMessage 增加 Title；sendNotification 把 Title 与 Text 分开交给渠道（Telegram 拼 caption，企业微信填 news.title/description），无封面时用 NotificationPlainText 合成纯文本；NotificationHeadline 生成「番号X + 动作」标题，番号缺失时只保留动作，保证标题始终非空。原 NotificationLabel 由 NotificationHeadline 取代并删除，避免两套文案拼装口径并存。
- 允许修改：internal/application/notification.go、services.go、subscription_download.go、internal/bootstrap/bootstrap.go 及各自测试、internal/platform/wechat/client_test.go。无数据库迁移，无历史数据回填，前端无改动，api/openapi.yaml 无契约变化（通知文案不进接口契约）。
- 实现要点：各触发点标题统一为「番号X已加入订阅列表」「番号X订阅失败」「番号X开始下载」「番号X已完成下载」「番号X下载失败」，正文分别为影片标题、站点、失败原因。
- 可观测性：Notify 成功新增 INFO 日志「消息通知已发送」（字段 channel/event/with_cover）。此前只有失败才留痕，静默成功与「开关没接线」无法区分，这正是本次问题难以自查的原因。
- 验证证据（真实联调，不是只跑构建）：① 用后台已配置的 bot token 直接调用 sendPhoto 发同一张封面到 TELEGRAM_CHAT_ID，带 has_spoiler=true 时返回 ok=true 且 has_media_spoiler=true，不带该字段时该属性缺失，证明「推送防剧透」确实改变平台侧行为；② 通过 POST /subscriptions 真实创建订阅（OFJE-697），运行中的后端日志出现 {"msg":"消息通知已发送","channel":"tg","event":"subscribe","with_cover":true}，证明开关启用、渠道解析、封面取值、SendPhoto 分流整条链路生效；③ POST /subscriptions 响应包含 media 快照（code/title/banner_url/poster_url），证明 Create 带出影片快照的修复已生效；④ 测试数据已清理：两个测试订阅均取消，订阅总数回到 10708，两个番号回到 subscription_status=none。
- 静态检查：gofmt -l internal cmd 无输出；go build ./...、go vet ./... 无输出；go test -count=1 ./... 12 个包全部 ok。新增 TestNotificationHeadlineAlwaysKeepsAction、TestNotificationPlainTextJoinsTitleAndText、TestDownloadNotificationsCarryTitleAndCover；wechat 的 TestSendPhotoPictureSelection 增加 news 标题/正文断言，锁住「标题不得为空」。
- 未验证与剩余风险：企业微信无法真机验收——WECHAT_CORP_ID 与 WECHAT_AGENT_ID 为空，渠道未配置，后端会跳过该渠道（既不报错也不发送）。即使补齐凭据，WECHAT_BANNER=false 且 WECHAT_PHOTO 为空时 picurl 为空，图文消息仍会按对标语义降级为纯文本；要收到带封面的图文消息，需要打开「微信封面推送」（用影片封面）或填写「微信默认推送图片」（用固定图）。封面按 URL 交给平台抓取，源站若拒绝平台抓图会降级为纯文本。本机无 gcc，-race 无法运行；当前环境无可用 Apifox MCP 工具，未同步 Apifox。

#### NOTIFY-04：订阅日志统一用番号标识（2026-09-29）

- 现象：系统日志里出现「订阅编号」，订阅列表与下载任务列表也只报数量，用户在界面上找不到日志对应的影片。
- 根因（代码确认）：订阅创建/编辑/取消的日志用 subscription_id 或 media_id 标识对象。两者都是不对用户暴露的内部主键，与任何界面元素都无法对应；查询类日志只有 count。
- 契约 NOTIFY-04：订阅相关日志一律用番号（media.code）标识对象；番号取不到时省略该字段，绝不回退到内部 ID——快照读取失败时日志只说明「影片信息获取失败」并带原因，推送退化为不带番号的标题。application.subscriptionLogAttrs 是订阅日志属性的唯一拼装点。订阅创建、编辑、取消的响应统一内嵌 media 快照（含 code），客户端与通知不再按 media_id 反查。查询类日志在 count 之后追加 codes 列表，前端压缩为「（A、B）」。
- 允许修改：internal/application/services.go、platform/database/repository.go、transport/httpapi/handler.go、application/agent/card.go；新增 internal/application/subscription_log_test.go，扩展 platform/database/subscription_create_test.go，调整 internal/application/subscription_notify_test.go 的仓储替身；frontend/src/features/system/logMessage.ts 及 logMessage.test.ts；api/openapi.yaml 的 Subscription.media 描述。无数据库迁移，无历史数据回填。
- 实现要点：仓储层 Create/Cancel/Update 改为与 List 同一返回形状（带 media 快照），快照读取失败只记日志、不把已提交的操作报成失败，日志改为「订阅已保存/订阅已取消/订阅已编辑，影片信息获取失败」加原因，不再写 media_id；通知在番号缺失时只保留动作，不回退到 media_id；HTTP handler 删除重复的订阅日志，服务层是订阅日志的唯一出口；logMessage.ts 把 code 标签改为「番号」，删除 subscription_id 与 media_id 标签（旧格式行也不再展示这两个字段），并新增 created/channel/event/with_cover 标签。
- 验证证据（真实联调）：以番号 OFJE-697 走完 创建 → 编辑 → 取消 → 取消不存在的订阅（404），运行中的后端 GET /logs 实际返回 `[订阅查询] 订阅已保存 attrs={"code":"OFJE-697","created":true}`、`[订阅查询] 订阅已编辑 attrs={"code":"OFJE-697"}`、`[订阅查询] 订阅已取消 attrs={"code":"OFJE-697"}`、`[订阅查询] 取消订阅失败 attrs={"error":"subscription not found"}`，均不含订阅 ID 与 media_id；同批 `[通知] 消息通知已发送 attrs={"channel":"tg","event":"subscribe","with_cover":true}`。测试订阅已取消，数据无残留。
- 静态检查：gofmt -l internal cmd 无输出；go build ./...、go vet ./... 无输出；go test -count=1 ./... 12 个包全部 ok（新增 TestSubscriptionLogsUseCodeInsteadOfInternalIDs、TestSubscriptionLogsRecordFailureReason、TestSubscriptionSnapshotFailureLogsWithoutInternalID）；前端 npm run check:standards 11 项、logMessage 用例 11 项、npm test 80 项、typecheck 与 build 全部通过。api/openapi.yaml 与 Apifox 项目 8873619 的 Subscription 模型（316776647）已同步 media 字段描述并回读确认。
- 浏览器验收（2026-09-29，Chrome 实机打开 http://127.0.0.1:5173/logs）：首屏 15:19:21 起的记录显示「订阅已保存，番号：OFJE-697，新建：是」「订阅已编辑，番号：OFJE-697」「订阅已取消，番号：OFJE-697」「取消订阅失败，原因：subscription not found」「消息通知已发送，渠道：tg，事件：subscribe，带封面：是」「影片详情查询完成，番号：OFJE-697」；全页文本检索不再出现 subscription_id 与「订阅编号」；15:09:46 的旧格式行只显示「订阅已取消」「订阅记录已删除」，不再露出英文原始字段名。 2026-09-29 复检：15:09:26 与 15:07:59 的旧格式「订阅已保存，新建：是」也不再显示「媒体编号」，全页检索无 subscription_id、订阅编号、媒体编号。
- 未验证与剩余风险：PostgreSQL/MySQL 未单独验证带出快照的新分支；快照读取失败与 media 缺失都只是防御分支——开发库实测 10708 条订阅全部能关联到 media，media.code 均非空（47218 条），且 subscriptions.media_id 外键为 ON DELETE RESTRICT，media 缺失当前不可达；快照失败路径由单元测试用故障注入覆盖，未在真实库上制造该故障。

#### TAG-01：标签统一简体中文并去重（2026-09-29）

- 现象：标签页存在重复标签——同一标签的繁体写法与简体写法并存（如 `舔陰` 与 `舔阴`、`單體作品` 与 `单体作品`），部分影片的标签串内部还重复列出同一标签（78 行）；采集新增标签时也不校验库内是否已有等价标签。
- 根因（数据确认）：`tag_catalog` 的 305 条是对标站的繁体字典；影片标签存 `legacy_media_metadata.genres`（逗号分隔文本），采集直接写入来源原文，入库前没有「是否已存在」的判定，也没有繁简归一，因此同一标签以繁体、简体、日文、英文多种写法各存一份。
- 契约 TAG-01：标签权威名只有一个来源——`TagNameService`（字典 + 别名 + 翻译）。入库时先查字典与别名，命中直接复用；未命中才翻译成简体中文并登记，来源写法记为别名。`tag_catalog` 是权威名字典，`tag_aliases`（迁移 25，alias 为主键，canonical 指向 `tag_catalog.name`）保存「来源写法 → 权威名」。归一化命令与采集入库共用同一套规则。
- 实现要点：纯汉字标签走逐字繁简转换提示词，含假名或拉丁字母的标签走跨语言翻译提示词；模型输出经 `SanitizeTagName` 校验（空值、换行、噪声短语、句末标点、成对引号、逗号、`->` 与 `→`、纯汉字长度变化、超长），不合格就保留原文并记日志，单个标签问题不阻断入库；纯汉字标签整词结果被模型改写时（`舔陰 → 舔阴部`）逐字重试，只接受 1:1 的汉字结果。`normalizeTagName` 折叠空白与零宽字符，全角折叠只处理字母数字——全角逗号折叠成半角会与 `genres` 的分隔符冲突，把对标站字典里的「和服，喪服」改写成无法按标签解析的值。
- 数据变更：`bytemuse migrate tag-normalize` 在单个事务内按同一映射重写 `tag_catalog`（按权威名合并，保留对标站人工分类，缺失的以「未分类」补齐）、`legacy_media_metadata.genres`（按出现顺序去重）、`tag_subscriptions`（合并时保留更早的起始日）与 `tag_subscription_matches`（按主键去重，保留首次处理时间）；解析阶段的别名登记独立提交，命令可重复执行，第二次不再调用翻译引擎。
- 预演与验证（副本先验证，再改开发库）：副本预演与开发库实际执行结果一致——扫描标签名 917，字典重命名/合并 186，字典补齐 629，影片标签行 2254，追新规则 0，追新台账 0；`media`/`legacy_media_metadata`/`tag_subscriptions`/`tag_subscription_matches` 行数不变（47218/47218/1/3），标签出现次数 12480 → 12399，行内重复 78 行 → 0，去重标签 917 → 748，`tag_catalog` 305 → 748，影片标签全部命中字典（未命中 0），`PRAGMA integrity_check=ok`、`foreign_key_check` 为空。
- 浏览器验收（2026-09-29，Chrome 实机 http://127.0.0.1:5173/tag）：全部标签页显示「共 748 条」，列表标签全部为简体中文且无重复；搜索「舔阴」返回 1 条（关联影片 16 部，类型行为），繁体「舔陰」返回 0 条；1920px 每行 5 个、390px 单列，390/576/768/1280/1920px 均无横向滚动。
- 静态检查：gofmt -l 无输出；`go build ./...`、`go vet ./internal/platform/database/` 通过；`go test -count=1 ./internal/platform/database/` 通过；application 标签用例通过（用 overlay 屏蔽与本任务无关的既存编译失败）。
- 未验证与剩余风险：仍有 3 个日文汉字专有名词未被转换（`小野塚小町`、`栞叶琉璃`、`犬走楓`），模型对这些名称返回原样或错字，按「无法处理则保留原文」处理，需要人工决定是否转换；`南ことり` 同样保留原文。`和服，丧服`、`新娘，年轻妻子` 这类来源自带的复合标签与 `genres` 中同样存在的 `和服、丧服`、`新娘、年轻妻子` 仍并存，拆分复合标签不在本次范围内。归一化改写了历史标签数据，执行前已做在线备份（`$env:TEMP\bytemuse-dev-before-tagnorm.db`，integrity ok）。执行期间另有任务（`bytemuse-tr2-20260929 migrate translation-fill`）在写同一个开发库，本次写入为短事务，未出现锁失败。本次无接口变更，`api/openapi.yaml` 与 Apifox 无需同步。

#### NOTIFY-05：图片防剧透覆盖番号卡片并补齐通知日志（2026-09-29）

- 现象：TG 的「推送防剧透」打开后，机器人发出的封面仍能直接看清；系统日志里也没有任何能说明防剧透是否生效的记录。
- 根因（代码 + 真机确认，分三条）：推送链路本身是通的——`TELEGRAM_SPOILER=true` 时渠道注册表会重建带 spoiler 的客户端，`SendPhoto` 会附带 `has_spoiler`；用后台已配置的 token 直连 Telegram 复现同一请求体（JSON body + URL 封面），返回 `has_media_spoiler=true`。真正漏打码的是**番号卡片**：用户发送番号后由 `SendCard` 发出的那张「封面 + 按钮」消息完全不经过 spoiler，而它正是机器人最近发出的带封面消息。日志缺失有三处：卡片投递成功不记任何日志；通知只在发送成功时记一条「消息通知已发送」，不体现封面是否打码；开关未启用、推送目标为空、渠道未配置时完全静默。
- 契约 NOTIFY-05：`TELEGRAM_SPOILER` 的语义是「机器人发出的所有图文消息封面是否打码」，不按发送场景分叉；`telegram.Client.applySpoiler` 是唯一实现点，`SendPhoto`（推送）与 `SendCard`（番号卡片）共用。通知层对每个渠道都必须给出结论：`消息通知已跳过`（reason = 事件开关未启用 / 推送目标未配置 / 渠道未配置）或 `消息通知已发送`；Telegram 适配器在真正发出图片后记 `Telegram 图文消息已发送`（kind = 推送通知 / 番号卡片，spoiler = 实际是否打码）。设置项标签由「推送防剧透」改为「图片防剧透」，与对标站 `TELEGRAM_SPOILER`（图片是否启用防剧透）一致。
- 允许修改：internal/platform/telegram/client.go、card_test.go、client_test.go；internal/application/notification.go；frontend/src/features/system/logMessage.ts、logMessage.test.ts、SettingsPage.tsx；本记录。无数据库迁移，无接口契约变化（`/system/settings` 只回传 values 映射），`api/openapi.yaml` 与 Apifox 无需同步。
- 实现要点：新增 `applySpoiler(payload)` 与 `logPhoto(kind)`；`SendCard` 的 sendPhoto 分支补打码与日志（降级为 sendMessage 时不记图片日志）；通知层用局部 `skip` 闭包统一记录跳过原因，跳过与发送都留痕；`logMessage.ts` 新增 kind / reason / spoiler 三个标签。
- 企业微信现状（数据确认）：`WECHAT_CORP_ID` 与 `WECHAT_AGENT_ID` 为空，渠道未配置，所有微信推送被跳过；`WECHAT_BANNER=false` 且 `WECHAT_PHOTO` 为空，即使补齐凭据也会降级为纯文本。因此「微信封面推送没作用」是必然结果，与开关实现无关；本次改动让这种跳过在日志里可见（`消息通知已跳过，渠道：wx，原因：渠道未配置`）。
- 验证证据（真实联调）：重启后端（16:08:41，`/health/ready`=ok）后用 API 新建 SSIS-082 订阅，运行中的后端实际写入 `[订阅查询] 订阅已保存 {"code":"SSIS-082","created":true}`、`[通知] Telegram 图文消息已发送 {"kind":"推送通知","spoiler":true}`、`[通知] 消息通知已发送 {"channel":"tg","event":"subscribe","with_cover":true}`、`[通知] 消息通知已跳过 {"channel":"wx","event":"subscribe","reason":"事件开关未启用"}`；测试订阅随后取消（status=canceled），数据无残留。真机 API 复现：sendPhoto 带 `has_spoiler` 与 `reply_markup.inline_keyboard`（卡片形态）返回 message_id=40、`has_media_spoiler=true`，证明平台接受「卡片 + 打码」组合；另有两条推送形态测试消息（message_id 37、38）同样返回 `has_media_spoiler=true`，用户可在 TG 客户端直接核对打码效果。
- 静态检查：`gofmt -l internal cmd` 无输出；`go build ./...`、`go vet ./...` 无输出；`go test -count=1 ./...` 全部 ok（新增 TestSendCardAppliesSpoilerSetting）；前端 `npm run check:standards` 11 项、`npm test` 82 项、`typecheck` 与 `build` 全部通过。
- 浏览器验收（2026-09-29，Chrome 实机 http://localhost:5173/logs 与 /settings）：日志页首屏显示「订阅已保存，番号：SSIS-082，新建：是」「Telegram 图文消息已发送，消息类型：推送通知，防剧透：是」「消息通知已发送，渠道：tg，事件：subscribe，带封面：是」「消息通知已跳过，渠道：wx，事件：subscribe，原因：事件开关未启用」「订阅已取消，番号：SSIS-082」；设置页「消息渠道 → Telegram」分组显示「图片防剧透」且处于勾选状态。
- 未验证与剩余风险：番号卡片的真机入站路径未走通——本机无法向机器人发送消息（getUpdates 只能由真实 Telegram 客户端触发），卡片打码由单元测试（断言 has_spoiler）与真机 API 复现（同一请求体）共同覆盖，需用户在 TG 里发一次番号确认；企业微信渠道未配置，微信侧只验证到「跳过原因已入日志」，未验证真实图文消息，`WECHAT_PHOTO` 为空时微信封面推送仍需先补齐企业微信凭据与默认配图；若用户客户端不支持 Telegram 的 spoiler 效果，服务端已无法进一步保证显示层打码。
#### ACTOR-03：热门演员定时同步与 gfriends 演员目录（2026-09-30）

- 目标：定时更新热门演员，并把 gfriends 官方演员库纳入演员列表；热门页与全部演员页使用不同数据边界。
- 契约：GET /actors?subscription=all 返回系统全部演员（含 gfriends 导入），subscription=hot 只返回最近一次成功发布的演员榜；名称搜索同时匹配演员名和 gfriends 别名。固定任务「同步演员目录」每天 04:00 执行；「同步热门演员」按 ACTOR_SCHEDULE_TIME 先采集 JavDB 月榜，再继续原有订阅追新。目录导入不触发订阅或下载。
- 数据变更：迁移 31 新增 actor_aliases(actor_name, alias) 及别名索引；演员资料逐条独立事务保存，已有订阅日期与非空头像不覆盖；热门发布在单事务内替换 rank_entries.rank_type='actors'，源站失败或部分资料失败保留旧榜。
- 实网证据：gfriends Filetree.json 解析 28,881 个按目标文件名去重演员、9,080 个别名；开发库首次导入后重跑结果为获取 28,881、新增 0、保存 28,881、失败 0；JavDB 演员月榜获取 9 人并成功发布。开发库迁移后演员总数 29,162、别名 9,068，PRAGMA integrity_check=ok。
- 验证证据：go test -count=1 ./...、go vet ./...、go build ./... 通过；演员前端 Tabs 单测通过。未完成真实浏览器多宽度验收及 Apifox 回读同步；头像仅保存来源 URL，不下载二进制。

#### SCAN-TASK-01：扫描与生成任务持久化（2026-10-01）

- 原扫描/生成进度仅保存在请求与页面内存中，刷新断连会取消执行。新增迁移 36 的 scan_tasks 台账保存任务标识、类型、状态、完整 JSON 快照和创建时间；按类型与创建时间索引读取最新任务。
- application.ScanTasks 是手动扫描/生成任务状态的权威实现，两类任务独立互斥。暂停在安全处理点确认，继续保留当前调用栈；取消不回滚已完成业务结果。单实例服务重启将活动任务标为 interrupted，不自动重放全量生成。
- 已验证 SQLite 空库初始化、版本 35 升级、重复迁移、重开数据库保留任务、请求断开不取消执行、两类任务隔离与暂停/继续/取消。历史媒体不回填、不重算；此前未保存的任务进度无法恢复。PostgreSQL/MySQL 迁移声明已覆盖，尚未实库验证。
- 运行库副本已从 35 升到 36，媒体数升级前后均为 47294，原运行库未迁移、运行进程未重启。隔离组件浏览器检查覆盖暂停/继续/取消、动态分母及 390/576/768/1280/1920px，无横向溢出，未验证真实网盘端到端流程。Apifox 外部写入被自动审批拒绝，待明确授权后同步及回读。

### 2026-10-08 扫描与媒体任务断点台账

- 迁移 40 新增 task_checkpoints：task_id（VARCHAR(64)，任务归属）、checkpoint_key（VARCHAR(64)，稳定 SHA256 键）、payload（TEXT，MySQL LONGTEXT，JSON 内容），均非空且无默认值，前两列组成主键。无记录表示未确认完成，不回填旧任务、不修改历史媒体；回退程序时保留此追加表，生产升级前保留数据库备份。
- 扫描、STRM 生成与媒体信息刷新用同一单项断点仓储；保存目录页、必要输入和成功项，失败项不标完成。续跑保留任务 ID；全量生成按映射保留清理完成标记，避免重删已写出的文件。业务操作和断点非同一事务，崩溃确认间隙允许幂等重试，不承诺第三方恰好执行一次。
- SQLite 独立测试覆盖空库初始化、39 升级到 40、重复迁移、断点更新、关闭重开后恢复、重复续跑拒绝、扫描已提交批次跳过与未提交尾批恢复、全量生成保留已完成文件、媒体刷新只重试失败项。无生产数据库写入，MySQL/PostgreSQL 未实库验证。
- 前端 154 项测试、规范检查、类型检查、生产构建，以及后端 go test ./...、go vet ./...、go build ./... 通过。本地隔离服务与浏览器验证三个红色续跑入口、终态隐藏进度条及 390/576/768/1280/1920px 无横向溢出；未验证真实网盘和 Emby 端到端恢复，未部署 NAS。OpenAPI 和 Apifox 七个任务接口已更新并回读。

### 2026-10-07 网盘上传台账

- 上传恢复与队列控制扩展沿用迁移 39 的 JSON 快照，不新增表列、不回填历史媒体。新增阶段 backing_up/cleanup/restoring/discarding；快照记录远端身份、重试时间、队列意图和隐藏标志，旧记录缺失字段按零值读取，无法确认身份的旧备份保留并显示需处理。
- 暂停状态跨重启保留；停止结束当前批次，后续新版本可入队；清除和删除只隐藏记录并保留去重凭据。已验证最终文件后清理失败保持 100% 并仅重试清理。115 未完成分片仍需重传，不声明分片断点续传。
- 独立 SQLite 测试覆盖真实仓储、暂停重建服务、停止与删除去重；故障替身覆盖同名预留、源版本变化、替换及删除响应丢失、备份身份变化和恢复旧名称。真实网盘传输与生产容器重启未验证。

- 新增迁移 39：cloud_upload_records，record_key 为 SHA256 主键（VARCHAR(64)，非空、无默认值），state 为提交阶段（VARCHAR(16)，非空、无默认值，索引检索未完成替换）；snapshot 为上传阶段 JSON（TEXT；MySQL LONGTEXT，非空、无默认值）；由上传服务 Get/Save/PendingCommits 实际使用。
- app_settings 新增 CLOUD_UPLOAD_PATHS=[]、CLOUD_UPLOAD_ENABLE=false、CLOUD_UPLOAD_CONFLICT=skip，原配置不覆盖。未操作原始 lady.db 或生产数据库。
- SQLite 独立测试覆盖空库初始化、38 升级到 39、重复迁移与台账保留；MySQL/PostgreSQL 尚未实库验证。表仅追加，回退保留台账并关闭监控。
