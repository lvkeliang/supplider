# Ralph Fix Plan

> 版本节奏依据 PRD 落地路径：**种子期（0-3 月）= 个人版 MVP**（Tauri + Go + SQLite，开源）→ 验证期（3-6 月）= 小企业版（权限/部门/API 对接/AI）→ 增长期（6-12 月）= 企业版（微服务/K8s）。
> 原则：AI 与 MongoDB 全部后置，但接口必须先行预留。

## High Priority — 个人版 MVP（种子期核心闭环）

- [ ] **Monorepo 项目骨架**：目录结构（`backend/` Go、`frontend/` React+TS+Tailwind、`desktop/` Tauri、`cli/`）；Go module 初始化；build tag 约定（`personal` / `small-business` / `enterprise`）；`make build-personal` 可产出二进制
- [x] **统一数据模型层 DataModel Interface**：`datamodel.SupplierStore`（Put/Get/Delete/List/Ping/Close）+ `SupplierFilter`/`Sort`/游标分页；业务代码只依赖接口；`datamodel/contract` 契约测试套件约束所有适配器行为（memory 与 sqlite 跑同一套 15 例）
- [x] **SQLite + JSON1/JSONB 存储适配器**（`internal/datamodel/sqlite`，personal + small-business 共用）：文档经 `jsonb(?)` 存 JSONB BLOB 列、`json(doc)` 读回；过滤/排序/游标字段反范式为类型化列（province/city/district/rating/qual_rank/owner/status/visibility/时间戳）+ `supplier_categories` 品类边表（EXISTS OR 语义）；WAL 模式、busy_timeout、外键级联；关键字走 `search_text` LIKE（ESCAPE 处理 %/_）与 memory 语义对齐。无 build tag（适配器属基础设施层，允许全 tier 编译），由 storefactory 按 tag 接线
- [ ] **供应商文档模型**：按 PRD 样例实现 `Supplier` 结构 —— `basic_info`（公司名/信用代码/法人/注册资本/成立日期/经营范围/省市区地域）、`qualifications[]`、`categories[]`、`products_services[]`、`performance_history[]`、`risk_flags`、`visibility`、`owner`、`shared_with`、`custom_fields`（自由扩展）、`change_log[]`、`attachments[]`；ID 规则 `sup_2026_000xxx`
- [ ] **供应商 CRUD API**（Go 后端 HTTP/gRPC）：创建、读取、更新（自动写 change_log：字段/旧值/新值/时间/来源）、删除（归档语义）、列表
- [x] **手动录入表单（前端）**：`frontend/`（Vite + React 18 + TS + Tailwind）；核心字段表单 + 动态资质/产品/绩效行 + 动态自定义字段（字段名 + 类型 文本/数字/布尔，不预定义）；可见性选择按 `features.visibility_levels` 门控；create POST / edit PATCH（服务端 diff 自动写 change_log）——2026-09-07
- [x] **供应商详情页"文档卡片"UI**：基本信息/资质/品类/产品服务/绩效/自定义字段/附件/变更记录各为独立可折叠 `DocumentCard`；自定义字段泛化渲染；风险标记横幅；归档/恢复/编辑操作；列表摘要卡 + 关键词搜索 + 地域/品类/资质/评分筛选 + 游标"加载更多"（列表只取 summary 字段）——2026-09-07
- [ ] **Monorepo 前端骨架（部分完成）**：`frontend/` 已就位（dev 走 Vite 代理 /api→127.0.0.1:7612；产物 dist/ ~55KB gzip）；**待办**：Tauri 2.0 `desktop/` Rust 壳（需安装 Rust 工具链，本机暂无 cargo）——sidecar 自动拉起 + 内嵌 dist/ + 打包 Win/macOS/Linux；**Tauri 接入前需给 Go HTTP 层加 localhost CORS**（Tauri origin `tauri://localhost` 直连 sidecar）
- [ ] **附件管理**：本地文件系统存储（用户目录下 `attachments/`），上传/下载/列表；单文件 ≤50MB 限制
- [ ] **Excel 批量导入**：模板下载、上传解析、列映射（先做手动映射 UI，AI 映射后置）、校验报错行报告、批量写入（1000 条 <3s）
- [ ] **基础全文搜索**：内嵌 Meilisearch（或先用 SQLite FTS5 过渡，搜索接口不变）；中文分词、模糊匹配、拼音、同义词；搜索结果缓存 5 分钟；响应 <200ms
- [ ] **条件筛选**：地域（省/市/区）、品类、资质等级、评分范围组合过滤 + 排序；结构化查询走存储层
- [ ] **列表页性能防护**：游标分页（禁 OFFSET 深翻页）、单页最多 100 条、列表只返回摘要字段（名称/品类/地域/评分）、搜索 3s 超时
- [ ] **srm-cli 命令行工具**：`search`（--city/--category/--qual-level）、`add <json> --visibility`、`list --visibility mine`、`info <id> --include history,attachments`、`export --format xlsx/json --filter`、`compare <ids...> --criteria price,delivery,qual`
- [ ] **数据导出与备份**：JSON / Excel 导出；个人版本地文件备份；导出数据可被小企业版导入
- [ ] **Tauri 2.0 桌面打包**：React 前端嵌入；Go 后端作为 sidecar 随应用启动自动拉起（用户无感知）；Windows .exe / macOS .dmg / Linux .AppImage；安装包 ~15MB、运行内存 ~80MB
- [ ] **Feature Flag 基础框架**：运行时配置控制功能开关（为三版功能矩阵打底），未配置 AI Key 时 AI 入口隐藏的前端/后端机制
- [ ] **MVP 验收走查**：全新机器双击安装 → 零依赖运行 → 录入 → Excel 导入 → 搜索筛选 → CLI 查询 → 导出，全流程无外部服务

## Medium Priority — 小企业版（验证期）

- [ ] **五级可见性权限体系**：等级 0-4（仅自己/指定人/本部门/指定部门/全公司）；查询时按 owner + shared_with + 部门归属过滤；个人版先开放 0/1，企业开放全部
- [ ] **用户与部门管理**：用户账号、部门层级树、角色（录入者/部门管理者/管理员）；RBAC + 可见性双轨；组织架构 Excel 导入
- [ ] **管理员可见性策略配置**：各等级启用/禁用、默认等级、等级 0 需审批开关
- [ ] **策略收紧数据处置流程**：扫描不合规记录 → 生成待处置清单 → 通知录入者 → 7 天缓冲（"待调整"态，仅本人+管理员可见）→ 超时自动降级到最近合规等级 → 申诉流程
- [ ] **供应商审核入库流程**：录入后管理者审批 → 入库；审批状态机
- [ ] **企查查/天眼查 API 对接**：入库时按企业名称+统一社会信用代码自动填充工商信息；API Key 加密存储；费用预算上限与预警
- [ ] **工商变更自动监控**：定时任务（每日/每周轮询）检测法人/经营范围/注册资本变更 → 自动更新档案 + 写 change_log（source=api_sync）→ 推送通知关注者
- [ ] **资质到期提醒**：提前 90/30/7 天通知 owner/关注者
- [ ] **风险标记与空壳检测**：rule engine 扫描频繁变更法人/认缴资本异常/被执行/行政处罚 → `risk_flags` 标记 + 风险预警；黑名单/归档生命周期
- [ ] **合作记录与绩效评价**：项目合作归档；交付/质量/配合度评分；绩效历史进入档案并影响搜索排序
- [ ] **基础项目比价（非 AI）**：多供应商横向对比表（价格/交付/资质），手工选择供应商生成对比
- [ ] **AI Gateway 统一适配层**：OpenAI 兼容格式封装 DeepSeek/通义/GLM/Ollama；模型路由（按任务类型）、token 用量统计与月度报告、相同请求缓存、主模型失败 fallback；无 Key 时整体降级
- [ ] **AI 辅助录入**：OCR（PaddleOCR/Tesseract）识别营业执照/资质证书 → LLM 抽取结构化字段自动填表；Excel 列名 AI 智能映射
- [ ] **文档分析搜索**：上传 PDF/Word/Excel/图片 → OCR → LLM 提取需求要素 → bge-m3 embedding → Qdrant 内嵌向量相似搜索 + 关键词 + 地域/资质硬过滤 → 推荐列表（按匹配度排序）+ 每条匹配理由 + 自动比价表
- [ ] **自然语言搜索**："杭州本地能做市政工程的二级资质以上供应商" → LLM 转结构化 Filter
- [ ] **AI 比价与风险报告**：多报价方案对比摘要推荐；工商变更记录 → 空壳风险评估报告；供应商档案一段话摘要
- [ ] **MCP Server**：CLI 命令映射为 MCP Tools；供应商数据映射为 Resources；预置 Prompt 模板；随包发布 Markdown Skill 文件（命令语法 + 典型用法，token 友好）
- [ ] **MongoDB 存储适配器**（`//go:build enterprise` / small-business）：BSON 文档操作、复合索引（可见性+品类+地域）；与 SQLite 适配器跑同一 contract test 套件
- [ ] **Docker Compose 一键部署**：Go-Zero 单体 + Nginx + MongoDB + Meilisearch + MinIO；安装脚本
- [ ] **MinIO/对象存储抽象**：S3 兼容接口，本地 FS / MinIO / S3 可切换
- [ ] **操作审计日志**：录入/修改/删除/可见性变更/导出全记录，按时间/用户/类型查询（小企业版基础级）
- [ ] **异步任务队列**：内存 channel 实现抽象接口，为 NATS 替换做准备（变更监控、AI 推理任务）
- [ ] **通知系统**：变更推送、到期提醒、审批通知（应用内通知起步）

## Low Priority — 企业版与增强（增长期及以后）

- [ ] **Go-Kratos 微服务拆分**：供应商服务/搜索服务/AI 服务/权限服务/同步服务 + API Gateway；gRPC 接口定义共享
- [ ] **MongoDB 副本集 + 分片**：按可见性等级分片（"全公司可见"热数据单独分片）
- [ ] **Elasticsearch 集群适配**：替换 Meilisearch 的搜索接口实现
- [ ] **Milvus 分布式向量库**：替换内嵌 Qdrant
- [ ] **NATS 消息队列**：替换内存队列实现
- [ ] **K8s Helm Chart 部署**：Helm chart、水平扩缩容、数千并发/百万数据验证
- [ ] **SSO/LDAP/SAML 对接**；IP 白名单、设备指纹、登录行为审计；CLI/MCP 接入 SSO
- [ ] **企业安全增强**：服务间 mTLS、HashiCorp Vault 集成、字段级加密、SQLCipher 个人版、导出水印、异地灾备/快照
- [ ] **完整审计与合规**：审计日志企业级完整版、导出权限与数据量限制
- [ ] **AI 数据安全**：云端请求脱敏管线、"仅本地模型"模式、BYOC 部署模式、本地 Ollama GPU 节点调度
- [ ] **Tauri 自动更新**：GitHub Releases / 自建更新源增量更新
- [ ] **共享贡献度激励**：共享越多搜索优先级越高、贡献度指标（对抗"平台退化为个人工具"风险）
- [ ] **钉钉/企业微信通讯录同步**
- [ ] **行业模板扩展**：装饰/园林/市政/弱电行业预置供应商分类与字段模板
- [ ] **CI/CD 三产物流水线**：单仓提交 → `-tags personal/small-business/enterprise` 三目标编译 → Tauri 包 / Docker 镜像 / Helm Chart → GitHub Releases / Registry / Helm Repo

## Completed
- [x] Project initialization（Ralph 脚手架、PRD 转换完成）
- [x] Backend monorepo 骨架：Go module、build tag 三 tier 接线（storefactory/tier/featureflag）、domain 文档模型、supplier service（change_log diff、评分聚合、归档/恢复）、HTTP API、srm-cli 雏形、memory 参考适配器
- [x] DataModel 接口 + contract 契约套件 + SQLite JSONB 适配器（personal/small-business 已接线；enterprise 占位待 Mongo）——2026-09-07
- [x] 前端（React+TS+Tailwind/Vite）：列表搜索筛选 + 游标分页、手动录入/编辑表单（动态自定义字段）、文档卡片详情页；AI 入口按 /features 隐藏——2026-09-07

## Notes
- **前端架构要点**：前端不引入 react-router 等重依赖，用 App 内 `View` 联合状态做三屏路由（list/new/detail/edit）；`src/api.ts` 是唯一发 fetch 的地方，业务/UI 只调类型化函数；`types.ts` 是 Go domain 的 TS 投影（以后端 JSON tag 为准）。dev 用 Vite proxy 同源转发 sidecar；Tauri 生产构建用 `VITE_API_BASE=http://127.0.0.1:7612` 直连。AI 入口（OCR/文档搜索/NL/Excel 智能映射）本轮不渲染，靠 `features.ai_*` 为 false 自动隐藏——无 Key 即无 AI 入口
- **下一轮候选（按 MVP 闭环）**：① 附件本地 FS 上传（objectstore/localfs 已有端口，需 HTTP multipart + ≤50MB 校验 + 前端附件区）；② Excel 批量导入（Go excelize + 手动列映射 UI + 校验报错行）；③ 导出 JSON/Excel + srm-cli export/compare 补齐；④ 数据模型目前全端内存 filter/关键字，搜索质量下一步用 SQLite FTS5 落地（featureflag 已标 fts5）；⑤ Tauri 壳（装 Rust 后）
- **SQLite 驱动选型（已定）**：用 `modernc.org/sqlite`（纯 Go、无 cgo）而非 `mattn/go-sqlite3`（cgo）——Tauri sidecar 需交叉编译 Win/macOS/Linux 单二进制，cgo 要求目标平台 C 工具链，与"零依赖双击安装"冲突。modernc v1.58 内含 SQLite 3.51，原生支持 JSONB。代价：go directive 升至 1.25（工具链自动下载），二进制略大（可接受，~15MB 目标仍可达）。驱动只允许出现在 `datamodel/sqlite` 适配器包内
- **SQLite 适配器设计要点**：① `doc BLOB` 列用 `jsonb(?)` 写入、`json(doc)` 读回，custom_fields 等自由结构免迁移；② 过滤/排序字段反范式成类型化列 + 索引，比 `json_extract` 谓词简单且快，json_extract 留给 ad-hoc custom_fields 查询；③ 品类用 `supplier_categories` WITHOUT ROWID 边表 + EXISTS 实现 OR 语义；④ 游标分页是 `(sort_col, id)` 元组比较（`col < ? OR (col = ? AND id < ?)`），rating 游标键在绑定参数时转回 float；⑤ `:memory:` DSN 每连接独立库，必须 `SetMaxOpenConns(1)`；⑥ 文件库用 WAL + busy_timeout(5000) + foreign_keys
- **适配器不打 build tag**：`datamodel/sqlite` 包无 tag（基础设施层允许任意 tier 编译），差异只在 storefactory 的 `tier_*.go` 接线文件；contract 套件因此 `go test ./...` 无 tag 也能覆盖 SQLite
- **MVP 刻意精简**：PRD 风险提示明确建议 MVP 阶段只做 Tauri + Go + SQLite，MongoDB 与 AI 后续引入——但所有接口（存储/搜索/队列/对象存储/AI）必须从第一天就抽象，否则后期重写业务代码
- **目标行业切口**：建筑/工程行业（皮包公司、垫资、资质造假、本地化偏好痛点最集中），数据模型与分类先按施工服务/货物采购/定制开发三类供应商设计
- **开源策略**：个人版 AGPL 协议开源，防竞品直接商用；开源也是建筑行业客户的数据安全信任基础
- **API 成本风险**：企查查/天眼查 1-5 元/次，个人版限制为手动查询（用户自付），小企业版设费用上限，企业版谈批量折扣——计费/限额设计要在对接时就内建
- **中文处理是硬需求**：全文搜索必须有好的中文分词 + 拼音 + 同义词；LLM 场景优先国产模型（DeepSeek-V3 约 2 元/百万 token，成本远低于 GPT-4 级）
- **性能红线勿忘**：分页上限 100 条、搜索 3s 超时、连接池 80% 限流——这些在 MVP 列表/搜索接口中就要内建，不是后期优化项
- 数据源参考：企查查开放平台（变更后 24h 内同步）、天眼查 API（200+ 接口实时）、国家企业信用信息公示系统（免费无 API）、全国建筑市场监管平台（资质/注册人员/处罚，每日更新）
