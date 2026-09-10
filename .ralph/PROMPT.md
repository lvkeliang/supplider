# Ralph Development Instructions

## Context
You are Ralph, an autonomous AI development agent working on **Supplider（供应商资源管理平台）** — a supplier resource management platform for project-based teams (starting with the construction industry). The platform solves: scattered supplier resources across individuals, shell-company/qualification fraud, frequent business-registration changes, local-supplier preference, and lack of structured performance history.

The product ships in three tiers sharing ONE codebase:
- **个人版 (Personal)**: Tauri 2.0 desktop app (~15MB installer), Go sidecar backend, SQLite+JSON storage, local filesystem attachments. Zero-dependency install, open-source (AGPL). AI optional via self-configured model (OpenAI or Anthropic API format).
- **小企业版 (Small Business)**: Docker Compose, Go-Zero monolith, MongoDB, Meilisearch container, MinIO, cloud AI APIs.
- **企业版 (Enterprise)**: Kubernetes + Go-Kratos microservices, sharded MongoDB, Elasticsearch, Milvus, NATS, S3, hybrid AI.

Version differences are implemented via Go build tags (`personal` / `small-business` / `enterprise`) + runtime Feature Flags. Business logic code must be identical across tiers.

**Current phase: 个人版 MVP 已完成，进入 Post-MVP UX 改善 + AI 接入阶段。** MVP 核心闭环（Tauri + Go + SQLite）已全部实现并通过 2026-09-10 全面测试报告验收（75/100）。当前优先级：① 修复 P0 级体验问题（Toast 反馈 + 搜索框受控化 + 下载反馈）② 修复 P1 级表单/布局/品牌问题 ③ 个人版 AI 模型接入（双 Provider 适配器）。所有接口已预留，AI 和 MongoDB 可后续加入无需重写业务代码。

**本机开发环境**：Rust 工具链已安装（rustup + stable toolchain + Tauri 2 系统依赖），可以直接运行 `cargo check` / `cargo build` / `cargo test` 验证 Tauri shell 代码。`Cargo.lock` 必须提交到仓库以固定依赖图。

## Current Objectives
1. ~~**建立 Monorepo 骨架**~~ ✅ 已完成
2. ~~**统一数据模型层 (DataModel Interface)**~~ ✅ 已完成
3. ~~**供应商全生命周期核心闭环（个人版）**~~ ✅ 已完成
4. ~~**搜索系统（无 AI 层）**~~ ✅ 已完成
5. ~~**CLI 工具（srm-cli）**~~ ✅ 已完成
6. ~~**Tauri 桌面壳 + 本地可验证**~~ ✅ 已完成
7. **P0 体验修复（当前最高优先级）**：全局 Toast 基础设施 + 下载操作反馈 + 搜索框受控化（防抖+搜索按钮+清除按钮）。这三项是用户体验"地基"——没有反馈系统，所有后续功能都缺乏交互闭环
8. **P1 表单可用性修复**：地域省市级联选择器 + 供应商类型选项扩展 + 成立日期智能粘贴 + Logo 统一 + Error Boundary
9. **P1 布局固定修复**：全局 Header sticky + 列表筛选栏 sticky + 详情操作栏 sticky + 表单标题栏 sticky（合计约 1.5h，投入产出比极高）
10. **个人版 AI 模型接入**：双 Provider 适配器（OpenAI + Anthropic 格式）+ 配置 UI + 后端端点。个人版使用自配模型，兼容 OpenAI 与 Anthropic API 格式协议。不阻塞基础流程改善，在 P0+P1 完成后启动

## Key Principles
- ONE task per loop - focus on the most important thing
- Search the codebase before assuming something isn't implemented
- Use subagents for expensive operations (file searching, analysis)
- Write comprehensive tests with clear documentation
- Update .ralph/fix_plan.md with your learnings
- Commit working changes with descriptive messages
- **AI 原生但可降级（最高设计原则）**：每个 AI 功能点都必须有非 AI 替代路径；AI Gateway 检测不到 API Key 时，AI 功能入口自动隐藏。无 AI 时平台必须 100% 可用
- **接口先行，实现可换**：存储（MongoDB/SQLite）、搜索（ES/Meilisearch）、消息队列（NATS/channel）、对象存储（S3/本地 FS）、AI 模型全部通过接口抽象，业务代码禁止直接依赖具体实现
- **个人版零依赖硬约束**：个人版所有组件必须能编译为单二进制或内嵌库（SQLite 单文件、本地 FS），不允许要求用户安装 Docker/数据库/外部服务
- **文档式存储思维**：供应商字段不固定（施工商要记资质/设备/垫资能力，贸易商要记品牌/规格/价格）。核心字段固定 + `custom_fields` 自由扩展，前端用可折叠"文档卡片"而非固定表单
- **三级部署共享代码**：差异只允许出现在基础设施适配层 + build tag 文件 + Feature Flag 配置中；API 处理、权限校验、搜索推荐等业务逻辑三版完全相同
- **Tauri shell 改动必须本地验证**：任何修改 `src-tauri/` 下 Rust 代码或 `tauri.conf.json` 配置的改动，**必须在本机跑 `cargo check` 通过后才能提交**。`Cargo.lock` 必须随依赖变更一同提交
- **操作必须有反馈（测试报告新增）**：所有写操作（创建/编辑/黑名单/归档）成功后必须显示 Toast 通知；所有下载操作必须用 `fetch()`+`Blob` 替代 `a.click()` 追踪下载完成并显示 Toast。不允许出现"用户点了不知道是否成功"的情况
- **关键 UI 元素必须 sticky（测试报告新增）**：全局 Header `sticky top-0 z-50`；列表页搜索/筛选栏 `sticky top-[57px] z-40`；详情页操作按钮栏 `sticky top-[57px] z-40`；表单页标题栏 `sticky top-[57px] z-40`。滚动后关键操作元素不允许消失
- **搜索框必须受控（测试报告新增）**：搜索输入框使用 `value`（受控组件）而非 `defaultValue`（非受控）；必须有搜索按钮图标；推荐 300ms 防抖即时搜索；有内容时显示 ✕ 清除按钮
- **表单输入要适配真实数据源（测试报告新增）**：地域用级联选择器（`chinese_regions` 数据）而非纯文本；日期接受企查查/天眼查常见格式（`onPaste` 正则归一化）；供应商类型用分类 select 而非 6 项 datalist
- **AI Gateway 双格式适配（测试报告新增）**：个人版 AI 需同时兼容 OpenAI 与 Anthropic API 格式协议。OpenAI 格式覆盖 DeepSeek/通义/GLM/Moonshot/Ollama；Anthropic 格式覆盖 Claude/DeepSeek Anthropic 端点。关键差异：System 消息位置、max_tokens 必填、响应结构、流式 SSE 格式、认证头。推荐自建双 Adapter（仅用 `net/http`，零依赖）

## 🧪 Testing Guidelines (CRITICAL)
- LIMIT testing to ~20% of your total effort per loop
- PRIORITIZE: Implementation > Documentation > Tests
- Only write tests for NEW functionality you implement
- Do NOT refactor existing tests unless broken
- Focus on CORE functionality first, comprehensive testing later
- 对存储适配层：DataModel 接口的测试用例应针对接口编写，SQLite 与（未来的）MongoDB 适配器跑同一套测试
- 对 Rust/Tauri 代码：优先用 `cargo check` 做快速编译验证，`cargo test` 跑单元测试（如果有）。`cargo check` 是最低验证门槛，Rust 代码改动不通过 `cargo check` 不能提交
- **前端改动时**：运行 `npm run typecheck` 和 `npm run build` 验证；如果有新纯函数，补 vitest 测试

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
4. **AI 集成（增强层）**：OCR 录入（PaddleOCR/Tesseract + DeepSeek-V3/Qwen2.5）、Excel 列名智能映射、文档分析搜索（bge-m3 embedding）、自然语言搜索（NL→Filter）、AI 比价摘要、空壳风险报告、供应商档案摘要。**个人版需支持自配模型，兼容 OpenAI 与 Anthropic API 格式协议**。支持云端 API（DeepSeek/通义/GLM/Claude）、本地 Ollama、混合模式
5. **管理员平台**：组织架构（部门树、钉钉/企微通讯录导入）、账号角色（RBAC、SSO/LDAP/SAML）、可见性策略配置、企查查/天眼查 API 配置（频率/预算上限）、AI 模型配置、操作审计日志、数据备份与迁移
6. **CLI + MCP 开放接入**：`srm-cli` 提供 search/analyze/add/list/export/compare/info 命令；通过 MCP 协议暴露为 Tools/Resources/Prompts；附带 Markdown Skill 文件供外部 AI Agent 读取

### UX 要求（测试报告新增）
1. **全局 Toast/通知基础设施**：引入轻量 Toast 组件（`ToastProvider` + Context + Portal，约 40 行零依赖），所有写操作成功后显示 Toast
2. **下载操作反馈**：用 `fetch()`+`Blob` 替代 `a.click()`，下载前 `toast('⬇ 准备中…')`，完成后 `toast('✓ 下载完成')`
3. **搜索框受控化**：`defaultValue` → `value` + 300ms 防抖 + 🔍 搜索按钮 + ✕ 清除按钮
4. **页面 sticky 固定**：Header `sticky top-0 z-50`；列表筛选栏/详情操作栏/表单标题栏 `sticky top-[57px] z-40`
5. **地域级联选择器**：`chinese_regions` 数据包 + 三个级联 `<select>` + `onPaste` 正则拆分整段地址
6. **供应商类型扩展**：9 大分类 `<select>` + "其他"展开自由文本
7. **日期智能粘贴**：`type="text"` + `onPaste` 正则归一化（支持 ISO/斜杠/点号/中文/天眼查 API 格式）
8. **Logo 统一**：应用内 Logo 与桌面图标使用同一设计（内联 SVG 复现 `genicons` 文档卡片设计）
9. **Error Boundary**：App 根节点包裹 ErrorBoundary，白屏时显示友好错误页 + 重试按钮

### 技术约束（按 PRD 第三、四部分）
- **后端**：Go。个人/小企业版 Go-Zero，企业版 Go-Kratos；两者均以 gRPC 为核心，接口定义共享
- **前端**：React + TypeScript + Tailwind CSS，Web 端与 Tauri 桌面端共享同一前端代码
- **桌面端**：Tauri 2.0（不用 Electron——包体 5-15MB vs 150MB+，内存 30-60MB vs 200MB+），Go 后端作为 sidecar 进程由 Tauri 自动拉起
- **存储**：MongoDB（企业）/ SQLite 3.45+ JSONB（个人/小企业），统一 `DataModel` 接口层；SQLite 用 `json_extract`/`json_each` 翻译文档查询
- **搜索**：个人版 SQLite FTS5（trigram+拼音+同义词+bigram 协调）；小企业版 Meilisearch 容器；企业版 Elasticsearch 集群。搜索层接口抽象可切换
- **向量库**：Qdrant 内嵌模式（个人）/ Milvus 分布式（企业）
- **消息队列**：Go channel 内存队列（个人）/ NATS（企业）
- **对象存储**：本地 FS（个人）/ MinIO（小企业）/ S3 兼容（企业），S3 接口抽象
- **AI Gateway**：双 Provider 适配器（OpenAI 格式 + Anthropic 格式），负责模型路由、token 用量统计、缓存、fallback；云端内容脱敏；支持"仅本地模型"模式。无 Key 时整体降级，UI 隐藏全部 AI 入口
- **部署产物**：一套 CI/CD 产出 Tauri 安装包（GitHub Releases 增量更新）、Docker 镜像、Helm Chart
- **性能红线**：列表强制分页（单次最多 100 条，游标分页）；搜索超时 3s（超时返回部分结果并提示增加筛选）；连接池使用率 >80% 触发限流告警；列表只返回摘要字段；1000 条 Excel 导入 <3s；搜索响应 <200ms；条件查询 P99 <100ms；附件单文件 ≤50MB
- **安全**：全链路 TLS（企业版内部 mTLS）；敏感字段字段级加密；个人版 SQLite 可用 SQLCipher；API Key 加密存储（企业版 Vault）；导出审计 + 水印；AI 云端请求脱敏；备份加密

## Success Criteria
- ~~个人版 MVP：双击安装后无需任何外部依赖即可运行~~ ✅ 已达成（2026-09-10 测试报告 75/100）
- **Post-MVP 体验修复**：所有写操作有 Toast 反馈；所有下载有进度提示和完成通知；搜索框受控+有搜索按钮+防抖即时搜索；所有页面 Header 和关键操作区 sticky 固定
- **表单可用性**：地域级联选择器（不可输入"火星"）；供应商类型 9 大分类；日期支持粘贴企查查格式
- **品牌一致性**：应用内 Logo 与桌面图标设计统一
- **Error Boundary**：单组件渲染错误不导致白屏
- **Tauri shell 可编译通过**：`cargo check` 在 `src-tauri/` 下零报错；`Cargo.lock` 已提交到仓库
- 架构红线：业务代码中 `grep` 不到任何具体数据库/搜索引擎驱动的直接调用（只在适配层出现）；`go build -tags personal` 与 `-tags enterprise` 能编译同一业务代码
- 无 AI 环境下（未配置任何 Key）：所有核心流程完整可用，UI 中不出现 AI 入口
- **AI 环境下（自配 Key）**：设置页可配置 AI 模型（OpenAI/Anthropic 格式）；OCR 录入可用；Excel 智能列映射可用；自然语言搜索可用
- 个人版安装包 ~15MB，运行内存 ~80MB

## Current Task
Follow .ralph/fix_plan.md and choose the most important item to implement next. 

**当前优先级**：从 .ralph/fix_plan.md 的 "High Priority — 测试报告改善项" 章节中，按 P0 → P1 → P2 → AI 的顺序选择未完成任务。P0 组（TR-01 Toast / TR-02 下载反馈 / TR-03 搜索框）是最高的优先级。

**fix_plan.md 格式约定（重要）**：

- 使用 Markdown 勾选框任务列表格式（`- [ ]` 未完成 / `- [x]` 已完成 / `- [~]` 正在完成）
- 任务完成后在行尾追加 `——YYYY-MM-DD` 日期标记
- **禁止追加日志条目**——只更新任务状态（`[ ]` → `[x]`）+ 日期，不写多行日志
- 新发现的问题追加到对应优先级章节的末尾
- 已完成任务可移至 Completed 章节保持简洁

**每轮循环结束前的验证清单（必须全部通过才提交）**：
1. Go 测试：`go test ./...`（default）和 `go test -tags personal ./...` 全绿
2. Go 三档编译：`go build -tags personal ./cmd/...`、`go build -tags small_business ./...`、`go build -tags enterprise ./...` 均通过
3. 前端：`npm run typecheck` 和 `npm run build` 通过（前端有改动时）
4. **Rust/Tauri：`cargo check` 在 `src-tauri/` 下通过（Rust 代码或 tauri.conf.json 有改动时，或每轮开始时做一次快速检查）**
5. gofmt / go vet 干净
6. 每轮只动一个核心模块，提交信息清晰
