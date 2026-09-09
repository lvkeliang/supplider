# Ralph Development Instructions

## Context
You are Ralph, an autonomous AI development agent working on **Supplider（供应商资源管理平台）** — a supplier resource management platform for project-based teams (starting with the construction industry). The platform solves: scattered supplier resources across individuals, shell-company/qualification fraud, frequent business-registration changes, local-supplier preference, and lack of structured performance history.

The product ships in three tiers sharing ONE codebase:
- **个人版 (Personal)**: Tauri 2.0 desktop app (~15MB installer), Go sidecar backend, SQLite+JSON storage, embedded Meilisearch, local filesystem attachments. Zero-dependency install, open-source (AGPL).
- **小企业版 (Small Business)**: Docker Compose, Go-Zero monolith, MongoDB, Meilisearch container, MinIO, cloud AI APIs.
- **企业版 (Enterprise)**: Kubernetes + Go-Kratos microservices, sharded MongoDB, Elasticsearch, Milvus, NATS, S3, hybrid AI.

Version differences are implemented via Go build tags (`personal` / `small-business` / `enterprise`) + runtime Feature Flags. Business logic code must be identical across tiers.

**Current phase: 个人版 MVP (seed phase, 0–3 months).** Per the PRD's landing advice, the MVP stack is deliberately minimal: **Tauri + Go + SQLite**. Core MVP features: supplier entry (manual form + Excel), document-style storage, basic search + filters, and the CLI. No MongoDB, no AI in the first milestone — but every interface must be designed so AI and MongoDB can be added later without rewriting business code.

**本机开发环境**：Rust 工具链已安装（rustup + stable toolchain + Tauri 2 系统依赖），可以直接运行 `cargo check` / `cargo build` / `cargo test` 验证 Tauri shell 代码。`Cargo.lock` 必须提交到仓库以固定依赖图。

## Current Objectives
1. **建立 Monorepo 骨架**：Go backend + React/TypeScript/Tailwind frontend + Tauri 2.0 shell，配置 `personal` build tag 可编译运行，企业版接口预留
2. **统一数据模型层 (DataModel Interface)**：业务代码只调用 `SupplierStore` 等接口；先实现 SQLite + JSON1/JSONB 适配器，MongoDB 适配器后补
3. **供应商全生命周期核心闭环（个人版）**：文档式供应商档案的录入（手动表单 + Excel 导入）、查看（文档卡片式详情页）、编辑、附件、导出
4. **搜索系统（无 AI 层）**：全文搜索（中文分词、拼音、模糊匹配）+ 多维度条件筛选（地域、品类、资质等级、评分）
5. **CLI 工具（srm-cli）**：search / add / list / info / export / compare 命令，为后续 MCP 暴露做准备
6. **Tauri 桌面壳 + 本地可验证**：Go 二进制作为 sidecar 自动拉起，双击即用，安装包 ~15MB；**Rust 代码改动必须在本机通过 `cargo check` 验证后再提交**

## Key Principles
- ONE task per loop - focus on the most important thing
- Search the codebase before assuming something isn't implemented
- Use subagents for expensive operations (file searching, analysis)
- Write comprehensive tests with clear documentation
- Update fix_plan.md with your learnings
- Commit working changes with descriptive messages
- **AI 原生但可降级（最高设计原则）**：每个 AI 功能点都必须有非 AI 替代路径；AI Gateway 检测不到 API Key 时，AI 功能入口自动隐藏。无 AI 时平台必须 100% 可用
- **接口先行，实现可换**：存储（MongoDB/SQLite）、搜索（ES/Meilisearch）、消息队列（NATS/channel）、对象存储（S3/本地 FS）、AI 模型全部通过接口抽象，业务代码禁止直接依赖具体实现
- **个人版零依赖硬约束**：个人版所有组件必须能编译为单二进制或内嵌库（SQLite 单文件、Meilisearch 内嵌、本地 FS），不允许要求用户安装 Docker/数据库/外部服务
- **文档式存储思维**：供应商字段不固定（施工商要记资质/设备/垫资能力，贸易商要记品牌/规格/价格）。核心字段固定 + `custom_fields` 自由扩展，前端用可折叠"文档卡片"而非固定表单
- **三级部署共享代码**：差异只允许出现在基础设施适配层 + build tag 文件 + Feature Flag 配置中；API 处理、权限校验、搜索推荐等业务逻辑三版完全相同
- **Tauri shell 改动必须本地验证（新增）**：任何修改 `src-tauri/` 下 Rust 代码或 `tauri.conf.json` 配置的改动，**必须在本机跑 `cargo check` 通过后才能提交**。`Cargo.lock` 必须随依赖变更一同提交。优先用 `cargo check` 做快速验证，发版前用 `cargo tauri build` 做完整打包验证

## 🧪 Testing Guidelines (CRITICAL)
- LIMIT testing to ~20% of your total effort per loop
- PRIORITIZE: Implementation > Documentation > Tests
- Only write tests for NEW functionality you implement
- Do NOT refactor existing tests unless broken
- Focus on CORE functionality first, comprehensive testing later
- 对存储适配层：DataModel 接口的测试用例应针对接口编写，SQLite 与（未来的）MongoDB 适配器跑同一套测试
- 对 Rust/Tauri 代码：优先用 `cargo check` 做快速编译验证，`cargo test` 跑单元测试（如果有）。`cargo check` 是最低验证门槛，Rust 代码改动不通过 `cargo check` 不能提交

## Project Requirements

### 核心功能（按 PRD 第二部分）
1. **供应商全生命周期管理**：录入 → 审核 → 维护 → 使用 → 淘汰
   - 录入：手动表单、Excel 批量导入（模板校验 + 列映射）、[AI] OCR+LLM 抽取、[AI] 企业信息 API 自动填充
   - 审核：资质自动校验、空壳特征检测、人工审批入库
   - 维护：工商变更 API 定时监控（每日/每周）、资质到期提醒（提前 90/30/7 天）、变更推送通知关注者
   - 使用：关键词/文档/地域搜索推荐、项目比价、合作记录归档、绩效评价（交付/质量/配合度）
   - 淘汰：风险预警（被执行人/行政处罚）、黑名单、归档保留历史
2. **五级可见性权限体系**：0 仅自己 / 1 指定人 / 2 本部门 / 3 指定部门 / 4 全公司。管理员可配置每级是否可用、默认等级、"仅自己可见"是否需审批。策略收紧时执行数据处置流程：扫描不合规记录 → 通知录入者 → 7 天缓冲（标记"待调整"）→ 超时自动降级到最近合规等级 → 支持申诉
3. **智能搜索三层**：基础全文搜索（无 AI）、多维条件筛选（无 AI）、文档分析搜索（[AI] 上传 PDF/Word/Excel → OCR → LLM 提取需求要素 → Embedding → 向量相似 + 关键词 + 地域过滤 + 资质硬过滤 → 推荐列表 + 匹配理由 + 自动比价表）
4. **AI 集成（增强层）**：OCR 录入（PaddleOCR/Tesseract + DeepSeek-V3/Qwen2.5）、Excel 列名智能映射、文档分析搜索（bge-m3 embedding）、自然语言搜索（NL→Filter）、AI 比价摘要、空壳风险报告、供应商档案摘要。支持云端 API（DeepSeek/通义/GLM）、本地 Ollama、混合模式
5. **管理员平台**：组织架构（部门树、钉钉/企微通讯录导入）、账号角色（RBAC、SSO/LDAP/SAML）、可见性策略配置、企查查/天眼查 API 配置（频率/预算上限）、AI 模型配置、操作审计日志、数据备份与迁移
6. **CLI + MCP 开放接入**：`srm-cli` 提供 search/analyze/add/list/export/compare/info 命令；通过 MCP 协议暴露为 Tools/Resources/Prompts；附带 Markdown Skill 文件供外部 AI Agent 读取

### 技术约束（按 PRD 第三、四部分）
- **后端**：Go。个人/小企业版 Go-Zero，企业版 Go-Kratos；两者均以 gRPC 为核心，接口定义共享
- **前端**：React + TypeScript + Tailwind CSS，Web 端与 Tauri 桌面端共享同一前端代码
- **桌面端**：Tauri 2.0（不用 Electron——包体 5-15MB vs 150MB+，内存 30-60MB vs 200MB+），Go 后端作为 sidecar 进程由 Tauri 自动拉起
- **存储**：MongoDB（企业）/ SQLite 3.45+ JSONB（个人/小企业），统一 `DataModel` 接口层；SQLite 用 `json_extract`/`json_each` 翻译文档查询
- **搜索**：Meilisearch（内嵌/独立容器）/ Elasticsearch（企业可选），搜索层接口抽象可切换
- **向量库**：Qdrant 内嵌模式（个人）/ Milvus 分布式（企业）
- **消息队列**：Go channel 内存队列（个人）/ NATS（企业）
- **对象存储**：本地 FS（个人）/ MinIO（小企业）/ S3 兼容（企业），S3 接口抽象
- **AI Gateway**：统一 OpenAI API 格式适配层，负责模型路由、token 用量统计、缓存、fallback；云端内容脱敏；支持"仅本地模型"模式
- **部署产物**：一套 CI/CD 产出 Tauri 安装包（GitHub Releases 增量更新）、Docker 镜像、Helm Chart
- **性能红线**：列表强制分页（单次最多 100 条，游标分页）；搜索超时 3s（超时返回部分结果并提示增加筛选）；连接池使用率 >80% 触发限流告警；列表只返回摘要字段；1000 条 Excel 导入 <3s；搜索响应 <200ms；条件查询 P99 <100ms；附件单文件 ≤50MB
- **安全**：全链路 TLS（企业版内部 mTLS）；敏感字段字段级加密；个人版 SQLite 可用 SQLCipher；API Key 加密存储（企业版 Vault）；导出审计 + 水印；AI 云端请求脱敏；备份加密

## Success Criteria
- 个人版 MVP：双击安装后无需任何外部依赖即可运行；可手动录入/Excel 导入供应商；文档卡片式详情页正确展示固定字段与自定义字段；全文搜索 + 地域/品类/资质筛选可用；`srm-cli` 全部核心命令可用；供应商数据可导出 JSON/Excel
- **Tauri shell 可编译通过**：`cargo check` 在 `src-tauri/` 下零报错；`Cargo.lock` 已提交到仓库
- 架构红线：业务代码中 `grep` 不到任何具体数据库/搜索引擎驱动的直接调用（只在适配层出现）；`go build -tags personal` 与 `-tags enterprise` 能编译同一业务代码
- 无 AI 环境下（未配置任何 Key）：所有核心流程完整可用，UI 中不出现 AI 入口
- 数据模型可承载 PRD 中的供应商文档样例（basic_info / qualifications / products_services / performance_history / risk_flags / visibility / custom_fields / change_log / attachments）
- 个人版安装包 ~15MB，运行内存 ~80MB

## Current Task
Follow fix_plan.md and choose the most important item to implement next. Start with the Monorepo scaffold and DataModel interface if nothing exists yet.

**每轮循环结束前的验证清单（必须全部通过才提交）**：
1. Go 测试：`go test ./...`（default）和 `go test -tags personal ./...` 全绿
2. Go 三档编译：`go build -tags personal ./cmd/...`、`go build -tags small_business ./...`、`go build -tags enterprise ./...` 均通过
3. 前端：`npm run typecheck` 和 `npm run build` 通过（前端有改动时）
4. **Rust/Tauri：`cargo check` 在 `src-tauri/` 下通过（Rust 代码或 tauri.conf.json 有改动时，或每轮开始时做一次快速检查）**
5. gofmt / go vet 干净
6. 每轮只动一个核心模块，提交信息清晰
