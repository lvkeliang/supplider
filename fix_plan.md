# fix_plan.md — Supplider 开发记录与待办

Ralph 每轮循环在此记录：已完成项、踩过的坑、下一步最重要的事。
新条目追加到对应章节顶部（最新在前）。

## 下一步优先级（个人版 MVP）

1. **Tauri 安装包真机验证**：shell 代码与 sidecar 已完成并通过 HTTP 层 E2E（见下），
   但本机无 Rust 工具链，未跑过 `tauri build`；需在有 Rust 的机器执行
   `scripts/build-sidecar.sh && cd src-tauri && tauri build`，验证安装包体积（目标 ~15MB）
   与双击拉起 sidecar。图标已由 `backend/cmd/genicons` 离线生成（PNG/ICO/ICNS）。
2. **可见性策略收紧流程**：个人版只需 0/1 两级的数据处置骨架（扫描不合规 → 通知录入者
   → 缓冲标记"待调整" → 超时降级）。当前可见性字段/校验已在，缺策略执行流程。
3. **Meilisearch 适配器（小企业版，非个人版）**：个人版**不做**内嵌 Meilisearch——
   Meilisearch 是 Rust 独立 server 二进制、无可内嵌 Go 库，塞进个人版会破坏"单二进制
   零外部依赖 / ~15MB"硬约束。个人版搜索继续用 SQLite FTS5（已在 `search.Index` 接口
   之后，行为被测试钉住）；Meilisearch 适配器应在小企业版（Docker Compose 独立容器）
   实现同一接口，届时照 FTS5 测试对照即可。
4. 空壳检测后续增强：外部工商/司法数据（被执行人/行政处罚）接入位已留（RiskFlags 字段
   保留不被引擎覆盖）。黑名单生命周期已落地（见下）；外部数据"被执行人→自动预警"可后挂。

5. srm-mcp 打包/分发：`go build -tags personal` 已出独立 stdio 二进制约 12MB，后续
   可纳入 scripts 构建/发布产物，随桌面版分发或单独提供（MCP 主机配置 command 即 srm-mcp）。

## 已完成

### 2026-09-08：黑名单生命周期（淘汰/禁用）——生命周期"淘汰"闭环落地

补齐生命周期最后一环：`domain.StatusBlacklisted` 常量本已存在但无入口，本环把"黑名单"
做成与归档并列的状态操作。黑名单是**可见的禁用警告**（确认造假/严重违约的供应商不应被
误选），与归档语义不同——归档是安静删除（默认列表隐藏），黑名单仍出现在列表/搜索但
醒目标记。纯本地，无 AI。

- **数据模型**（`domain`）：Supplier 加 `BlacklistReason`（列入原因，移出时清空；
  列入时间/操作人走 change_log）。黑名单原因外的审计信息全在变更记录。
- **服务层**（`internal/supplier/blacklist.go`）：
  - `Blacklist(id, reason)`：status=blacklisted，用 **Put**（不是 store.Delete——Delete
    强制归档），写 `status` 与 `blacklist_reason` 两条 change_log；已归档需先恢复
    （报错含 "must be restored"→HTTP 400）；已在黑名单→幂等 no-op。
  - `Unblacklist(id)`：status 回 active、清原因、写 change_log；非黑名单→no-op。
  - 适配器无需改：黑名单文档整体以 JSONB 持久化；默认列表只隐藏 archived，所以黑名单
    仍可见，`?status=blacklisted` 可单独筛出（memory/sqlite 的 Status 过滤本就支持）。
- **API**：`POST /suppliers/{id}/blacklist`（body `{reason}` 或 `?reason=`）、
  `POST /suppliers/{id}/unblacklist`，均返回更新后文档。
- **CLI**：`srm-cli blacklist <id> [--reason 原因]`、`srm-cli unblacklist <id>`；
  `srm-cli info` 头部 `[blacklisted]` + ⚠ 黑名单原因行。
- **MCP**：新增 `blacklist_supplier` 工具（`id`/`reason?`/`remove?`，remove=true 移出）；
  Skill 文档同步（并注明 Agent 不擅自拉黑，需用户明确指示）。
- **前端**：列表卡片黑名单供应商显示红底白字 `🚫 黑名单` 徽标（优先于风险标）；详情页
  顶部红色横幅"已列入黑名单（淘汰/禁用），请勿选用"+原因；头部按钮：在库显示
  `🚫 列入黑名单`（弹 prompt 填原因）+归档，黑名单显示`移出黑名单`，归档显示`恢复`。
- 测试：`supplier/blacklist_test.go` 3 例——列入(状态+原因+change_log)后**默认列表仍
  可见**且 `status=blacklisted` 可筛出、移出回 active 清原因；归档供应商列入被拒；
  重复列入/对非黑名单移出均幂等。全量 `go test` 默认+personal 全绿，enterprise/personal
  编译通过；前端 tsc+vite 通过。
- E2E：HTTP 列入（status/reason 正确）→列表仍可见→status 过滤命中→CLI info 显示黑名单
  →CLI 移出回 active；MCP stdio `blacklist_supplier` 写入**与运行中 sidecar 共享的
  SQLite**（WAL 多进程），HTTP 复核状态/原因已更新。

### 2026-09-08：MCP 开放接入（Tools / Resources / Prompts + Skill 文件）

把供应商平台通过 Model Context Protocol 暴露给外部 AI Agent（Claude Desktop /
Cursor 等）。新增 `internal/mcp` 包 + `cmd/srm-mcp` 独立 stdio 二进制；与 httpapi
同属**纯业务层**，只依赖 `*supplier.Service`，不碰具体存储驱动；stdlib 手写
JSON-RPC 2.0（无 SDK 依赖，保持个人版纯 Go 小二进制）。

- **传输**：stdio 换行分隔 JSON-RPC。`Server.Serve` 逐行读请求回响应；notification
  （无 id）不回包；日志走 stderr，stdout 只跑协议。实现 initialize/ping/
  tools(list,call)/resources(list,templates/list,read)/prompts(list,get)；未知方法
  回 -32601；工具内错误用 MCP `isError` 文本块（让模型自我纠正）而非协议错误。
- **直连存储（不依赖 sidecar）**：`cmd/srm-mcp` 经 storefactory 直接打开**与 Tauri
  桌面版同一个** SQLite 库——默认数据目录按 Tauri identifier `com.supplider.desktop`
  解析（Linux `$XDG_DATA_HOME`/`~/.local/share`；macOS `~/Library/Application Support`；
  Windows `%APPDATA%`），可用 `--data-dir`/`SRM_DATA_DIR` 覆盖。WAL 下可与运行中的
  桌面 App 并发读写；App 关闭时 MCP 仍独立工作。无网络/无 Key/无外部服务。
- **Tools（7 个）**：`search_suppliers`（中文 FTS/拼音/同义词 + 地域/品类/资质/评分
  筛选，q 空即列首页）、`get_supplier`、`add_supplier`（必填公司名/省/市，录入即跑
  空壳检测不阻断）、`shell_risk_queue`、`supplier_risk`、`expiring_qualifications`、
  `compare_suppliers`；均带中文 description + JSON-Schema 入参。
- **Resources**：静态 `supplider://suppliers`、`supplider://risk/shell`、
  `supplider://reminders/expiring`；模板 `supplider://supplier/{id}`。
- **Prompts**：`supplier-due-diligence`（query=id/公司名）——尽调流程提示。
- **Skill 文件**：`docs/mcp/supplider-skill.md`（frontmatter + 工具表/资源/工作流/
  输出约定），供外部 Agent 读取。
- 测试：`mcp/server_test.go` 用 memory store 跑完整 JSON-RPC 会话（能力宣告 / 7 工具
  / add 干净+风险两家 / search 命中 / shell 队列只含风险家 / resources / prompts /
  缺失 id 回 isError / 未知方法 -32601 / notification 不回包）。默认 + personal 全量
  `go test` 全绿，enterprise/personal 编译通过。
- E2E（真实 stdio 二进制 + fresh SQLite）：initialize/notification/add(干净→无风险，
  风险→R103+R202)/search 命中/shell 队列仅风险家/resources 读到 2 家；二次启动重开
  同库数据仍在；二进制约 12MB，stderr 仅日志。


### 2026-09-08：空壳检测人工审核闭环——生命周期"审核"人在回路

规则引擎只"标记"，本环补上"人来清"：审核人查验原件后把供应商清出队列，且资料再变动
会自动重新进入队列（陈旧的人工结论不能掩盖新信号）。纯本地、无 AI/网络。

- **数据模型**（`domain`）：`RiskFlags` 加 `Reviewed / ReviewedAt / ReviewedBy /
  ReviewOutcome(verified|dismissed) / ReviewNote`；新增常量 `RiskReviewVerified`
  （已核验）/`RiskReviewDismissed`（误报忽略）。`Summary` 加 `risk_reviewed` 反范式位，
  列表据此区分"待审核"红标与"已核验"绿标。
- **服务层**（`internal/supplier/risk.go`）：
  - `ReviewRisk(id, RiskReviewInput{Outcome,By,Note})`：校验 outcome（非法报错）、写
    RiskFlags 审核字段、追加 `risk_review` change_log（含上一次 outcome，可审计）。
    **不覆盖** shell_risk/notes——引擎结论客观保留，reviewed 只表示"人已受理"；外部信号
    （被执行人/行政处罚）同样不受影响。
  - `ShellRiskSuppliers` 队列**跳过 Reviewed**——审核后即出队。
  - **编辑自动重开**：Update 时若变更触及规则读取的字段（`basic_info.*` / qualifications
    / categories），清空审核状态；绩效/产品/custom_fields/可见性/附件等不影响空壳判定的
    编辑**不**重开（`reopenRiskReviewIfNeeded`，在 applyRisk 前调用）。
- **API**：`POST /api/v1/suppliers/{id}/risk-review`，body `{outcome, by, note}`（body
  可空，支持 `?outcome=`；by 默认 local）。非法 outcome → 400，不存在 → 404。
- **CLI**：`srm-cli review <id> [--dismiss] [--outcome verified|dismissed] [--by 用户]
  [--note ...]`——默认"已核验"，`--dismiss` 误报忽略；成功打印已清出队列。
- **前端**：详情"风险检测"卡片在未审核时显示「✓ 已核验（查验原件，正规）」「误报忽略」
  两个按钮（带确认），审核后转为绿色"已人工核验/误报忽略"条（审核人/时间/备注 + 资料
  变更会重进队列提示），卡片默认折叠；列表卡片红标改为"⚠ 待审核"，已审核显示绿标
  "✓ 已核验"。
- 测试：`supplier/risk_test.go` 加 3 例——审核后出队 + change_log + summary 投影
  (shell_risk=true & risk_reviewed=true)、非法 outcome 报错、**仅风险相关编辑重开**
  （绩效编辑保持已审核、basic_info 编辑重开并回队）。全量 `go test` 默认/personal 全绿，
  enterprise/personal 编译通过；前端 tsc+vite 通过。
- E2E（真实 HTTP，personal）：坏代码+资料简陋贸易商 → 队列 1 → dismiss(by alice) →
  队列 0 且 shell_risk 仍 true/risk_reviewed=true；非法 outcome 400；PATCH 加法人
  （basic_info）→ 自动重开 reviewed=false 队列回 1；`srm-cli review` 默认 verified → 队列
  0；PATCH 绩效 → 保持已审核。



### 2026-09-08：空壳特征检测（非 AI 规则引擎）全链路落地——生命周期"审核"无 AI 基线

录入/审核环节的自动校验，纯本地规则、无网络、无 Key；AI 空壳风险报告（LLM）以后作为
增强层叠加在**这些信号**之上。规则保守：只标记供人工复核，**绝不阻断录入**。

- **规则引擎**（`internal/risk/risk.go`，纯函数 `Evaluate(supplier, now) Report`）：
  - R1xx 身份：R101 未填信用代码(low) / R102 长度≠18 或含非法字符(high) /
    **R103 信用代码校验位不符 GB 32100-2015(high)**——`CheckCreditCode` 实现国标
    加权校验（C=(31−Σvᵢwᵢ mod31) mod31），真实代码必过、编造代码几乎必挂。
  - R2xx 档案：R201 成立未满 180 天(medium，临时注册/买壳) / R202 核心资料缺 ≥2 项
    (medium) / R203 施工类无任何资质(medium) / **R204 资质重复(medium)**——同证书号
    出现多次（确凿）或同类型+同等级重复（堆砌资质/录入重复）。
  - R3xx 财务：R301 注册资本 <100 万元(low)，解析"5000万人民币/1.2亿"等中文写法。
  - 判定：任一 high，或 ≥2 条 medium → `ShellRisk=true`；low 仅提示补全。信号带稳定
    code + 中文解释，`Notes()` 汇总 medium/high 写入 risk_flags.notes。
- **生命周期接线**（`internal/supplier/risk.go`）：`applyRisk(doc)` 在 Create/Update
  与 `recomputeRating` 同处调用——risk_flags 与 rating 一样是**派生数据**，随写入刷新、
  **不产生 change_log 噪音**。只覆盖引擎字段（ShellRisk/Notes），高版本外部信号
  （被执行人/行政处罚，企查查 API）原样保留。
  - `RiskReport(id)`：实时跑规则返回完整信号（不落库），详情页解释"为什么被标记"。
  - `CheckRisks(id)`：重跑并持久化（规则升级后的回填/按需复审），仅在结论变化时写库。
  - `ShellRiskSuppliers()`：keyset 分页遍历**在库**供应商实时评估，返回审核队列
    （高/中信号计数 + 信号明细），按 high→medium→名称排序；归档不产生审核噪音。
  - `domain.Summary` 加 `shell_risk` 反范式标志，列表页无需扫描即可打标。
- **API**：`GET /api/v1/suppliers/{id}/risk`（实时信号）、`POST .../risk-check`
  （重跑+持久化）、`GET /api/v1/risk/shell`（审核队列 {count,items[]}）。
- **CLI**：`srm-cli risk [id] [--json]`——无 id 列审核队列（CJK 等宽表格，等级/高中
  计数/供应商/地域/首要信号）；带 id 显示该供应商逐条信号（✗高/!中/·低）；适合 cron。
- **前端**：列表卡片有 `⚠ 空壳风险` 红标；列表上方红色可展开横幅（N 家待审核，点击
  直达详情）；详情页"风险检测"文档卡片逐条展示信号（严重度色标 + 规则 code + 中文解释），
  无信号不显示；外部信号（被执行人/行政处罚）以 EXT 行并入同卡。
- 测试：`risk/risk_test.go`（R103 校验位正反例、非法字符、长度、R201/202/203、
  **R204 类型+等级/证书号重复与不同等级不误报**、低资本不单独触发、贸易商不触发施工
  规则）；`supplier/risk_test.go`（干净供应商不标记且进 summary、坏校验码标记+实时报告
  含 R103、Update 后风险翻转/修复后清除、审核队列只含在库且归档后移出、CheckRisks
  保留外部 ExecutedPerson 标志）。全量 `go test` 在默认与 `-tags personal` 下全绿，
  `-tags enterprise`/`personal` 编译通过；前端 `tsc + vite build` 通过。
- E2E（真实 HTTP，personal 档 ai_enabled=false）：坏代码+资料简陋施工商 → shell_risk
  并列出 R103/R202/R203；正规贸易商 → 不标记；`/risk/shell` 仅 1 家且 high=1/med=2；
  list summary 带 shell_risk；CLI 队列/详情/干净三种输出正确；R204 同证书号被捕获；
  POST risk-check 返回 200。



### 2026-09-08：资质到期提醒（90/30/7 天 + 已过期）——生命周期"维护"非 AI 基线

- **业务逻辑**（`internal/supplier/expiry.go`）：`ExpiringQualifications(ctx, withinDays)`
  按 Export 同款 keyset 分页遍历**在库**供应商（归档不产生维护噪音），解析每份资质的
  expiry 日期；按日差归入 expired（<0)/7d(≤7)/30d(≤30)/90d(≤90) 桶，按紧迫度排序
  （最过期的在前）。日期解析接受 ISO 日期与 RFC3339 时间戳（防御性），空/乱码日期
  静默跳过不炸整批；日差按 UTC 日历天（截断到午夜），不受时分/时区影响。
- **可测时钟**：`Service.WithClock(func() time.Time)` 导出——生产走 UTC 墙钟，测试冻时间。
- **API**：`GET /api/v1/reminders/expiring?within=90` → `{count, expired, items[]}`，
  items 含 supplier_id/name、地域、资质类型/等级/证书号、到期日、days_left、bucket。
- **CLI**：`srm-cli expiring [--within N] [--json]`——CJK 等宽表格，状态列
  ✗已过期/!!7天内/!30天内/90天内，剩余列"已过期 18 天 / 6 天后 / 今天到期"；适合挂
  cron/计划任务做每日提醒。
- **前端**：SupplierList 挂载时拉取一次（best-effort，失败不影响列表）；有提醒时列表上方
  出现可展开横幅——有已过期为红色（"N 项资质已过期"），否则琥珀色（"N 项资质即将到期"）；
  展开后每条点击直达供应商详情。
- 测试：`expiry_test.go` 3 例（冻结时钟）——窗口分桶与排序、今天到期(0天)/RFC3339/
  乱码日期跳过、归档排除；全量 `go test` 在默认与 -tags personal 下全绿，
  -tags enterprise 编译通过。E2E（真实 HTTP）验证 API 与 CLI 输出一致。
- 工商变更监控仍是 [AI]/API 层占位；此扫描即 PRD"变更推送/到期提醒"在无 Key 环境的
  100% 可用降级路径。

### 2026-09-08：Tauri 2.0 桌面 shell + 单二进制 Web 模式（sidecar 打包落地）

- **Tauri shell（`src-tauri/`）**：纯基础设施层，零业务逻辑。Rust 侧启动时
  `tauri_plugin_shell` 拉起 Go sidecar（`--addr 127.0.0.1:7612 --data-dir <app_data_dir>`），
  轮询 `/readyz` 最多 20s，退出时 kill 子进程（无孤儿守护）；sidecar 日志转发 Tauri 控制台。
  capabilities 只开 sidecar spawn/execute/kill，数据访问全走 HTTP。
- **单二进制 Web 模式**：`internal/webui` 用 `//go:embed all:dist` 把前端构建产物嵌进
  suppliderd，`httpapi.MountWebUI` 挂在 `/`（/api、/readyz 更具体的 pattern 优先）；
  非资源路径回退 index.html（SPA），缺失的 /assets/* 仍 404。双击/命令行直接跑 sidecar，
  浏览器开 127.0.0.1:7612 即得完整平台。
- **CORS**：`httpapi.CORS` 仅放行 Tauri 固定 origin（tauri://localhost、tauri.localhost）
  与 loopback 开发服务器；预检 OPTIONS 直接 204，无 Origin 头（同源/反代）零影响。
- **打包脚本**：`scripts/build-frontend.sh`（npm build 并同步 dist 到 frontend/dist 与
  webui/dist 两处）、`scripts/build-sidecar.sh`（CGO_ENABLED=0 纯 Go 交叉编译 5 个目标
  triple，-ldflags="-s -w" 剥符号，linux 二进制 ~16MB）。
- **fresh-clone 安全**：dist/ 只跟踪 `.gitkeep`（go:embed 要求目录存在）；无构建产物时
  `webui.Dist()` 返回内置 placeholder.html（"前端未构建"引导页，fstest.MapFS），API 不受影响。
  已用 /tmp 最小模块模拟验证：仅含 .gitkeep 的 dist 可编译并返回占位页 200。
- **E2E 验证（-tags personal，真实 HTTP）**：/readyz、/features（tier=personal/sqlite/fts5/
  localfs/ai=false）；创建 3 家供应商后 FTS 全通过——trigram 子串（"州一建有"）、全拼
  （hangzhouyijian）、首字母（hzyj）、同义词（搜"商砼"命中写"商品混凝土"的文档）、短词
  LIKE（"水泥"）、城市+关键词 AND、评分筛选（rating 由 performance_history 聚合，
  不可直接写入——4.5/5.0 两条 → 4.75）；嵌入式 UI 200 + assets MIME 正确 + SPA 回退 +
  非法 origin 无 CORS 头；JSON 导出正常。
- **前端搜索接线确认**：`SupplierList.tsx` 早已走 `GET /api/v1/suppliers?q=...` 并展示摘要
  卡片，筛选栏（省/市/品类/资质等级/最低评分/含归档）与游标分页齐备——上轮计划项 #1 关闭。

### 2026-09-07：SQLite FTS5 搜索引擎（个人版搜索层落地）

个人版无外部搜索引擎依赖，搜索直接在 SQLite 单文件内实现：

- **FTS5 trigram 分词器**：中文无需词典，`杭州一建有限公司` 可被任意 ≥3 字子串命中
  （杭州一建 / 州一建有 / 一建有限…）；Latin/数字（信用代码、拼音）容错子串匹配。
- **短词 LIKE 回退**：trigram 无法索引 <3 字的词（水泥、建材、杭州…），这些词走
  `search_text LIKE`；两条路径与结构化筛选 AND 合并，语义是原子串搜索的严格超集。
- **拼音**：`internal/search/analyze.go` 用 go-pinyin 生成两种形式——全拼连写
  （`hangzhouyijian`，按字段一个 token，trigram 可匹配任意子串）和首字母缩写（`hzyj`），
  存入 FTS 的 `py` 列；不依赖中文输入法即可搜索。
- **建筑行业同义词（索引期扩展）**：商砼 ↔ 商品混凝土 ↔ 混凝土 ↔ 砼；挖机/挖掘机/钩机；
  吊机/吊车/起重机。文档含任一词，其余词写入索引，双向可搜。
- **事务一致性**：FTS 行与供应商行在**同一事务**内更新（Put 时 DELETE+INSERT），
  无双写窗口；旧库重开时 `reindexFTS` 自动回填。
- 引擎无关的中文分析逻辑（SplitTerms / Pinyin / SynonymExpansions）放在
  `internal/search`，未来 Meilisearch/ES 适配器复用同一套——三级部署中文行为不得分叉。

### 此前各轮（摘要）

- Monorepo 骨架：Go backend（cmd/suppliderd、cmd/srm-cli）+ React/TS/Tailwind 前端；
  build tag `personal`/`small_business`/`enterprise` + tier 包 + featureflag。
- DataModel 接口层：`datamodel.SupplierStore`；memory 参考适配器 + SQLite JSONB 适配器
  （modernc 纯 Go 驱动，无 cgo，可交叉编译）；共享 contract 测试套件。
- 供应商闭环：手动录入、Excel 导入（模板/列映射/1000 行 <3s）、JSON/XLSX 导出、
  本地 FS 对象存储 + multipart 附件上传下载、文档卡片详情所需的完整 domain 模型。
- CLI：search/add/list/info/export/compare。
- AI Gateway 接口骨架（无 Key 时 AI 入口隐藏的可降级设计）、taskqueue（channel 内存队列）。

## 踩坑记录（重要！）

1. ****E2E 测试必须带 `-tags personal`**：无 tag 的开发构建默认接线 **memory 存储**
   （`storefactory/tier_default.go`），不是 SQLite！用 `go build ./cmd/suppliderd`
   起的守护进程行为（无 FTS、无拼音、无同义词）与个人版发行物完全不同。
   验证个人版功能一律 `go build -tags personal`。
2. **同义词嵌套陷阱**：同义词组成员互相包含——`砼` 是 `商砼` 的子串、`混凝土` 是
   `商品混凝土` 的子串。朴素 `strings.Contains(text, "砼")` 会把只写了"商砼"的文档
   误判为已含"砼"而跳过扩展。`SynonymExpansions` 现在先把**其他**较长成员从文本中
   擦除（最长优先；候选词自身及其子串保留），再判断独立出现。
3. **同义词必须同时进两条索引路径**：扩展词只放 FTS content 列不够——短词（如
   `商砼` 2 字）查询走 LIKE on `search_text`，若 `search_text` 不含扩展词，
   "搜商砼找写混凝土的文档"方向失效。现 `buildSearchText` 统一追加扩展词，
   FTS content 直接复用，两路径行为一致。
4. **测试夹具的地域要与名字一致**：fixture 默认地域是杭州，命名为"宁波建材贸易"的
   供应商若不显式设置 Region.City=宁波，"关键词 + 城市筛选 AND"的测试会假阳性通过/失败。

## 架构红线自查

- 业务代码（supplier service / httpapi / exporter / importer）只依赖 `datamodel`、
  `search`、`objectstore` 等接口；具体驱动（modernc sqlite、go-pinyin）仅出现在
  adapter 包（sqlite/、localfs/）与 storefactory/objectfactory 接线文件中。
- `go build -tags personal` 与 `-tags enterprise` 均编译通过（每轮验证）。
