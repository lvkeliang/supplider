# Ralph Fix Plan

> 版本节奏依据 PRD 落地路径：**种子期（0-3 月）= 个人版 MVP**（Tauri + Go + SQLite，开源）→ 验证期（3-6 月）= 小企业版（权限/部门/API 对接/AI）→ 增长期（6-12 月）= 企业版（微服务/K8s）。
> 原则：AI 与 MongoDB 全部后置，但接口必须先行预留。

## High Priority — 个人版 MVP（种子期核心闭环）

- [x] **Monorepo 项目骨架**：目录结构（`backend/` Go、`frontend/` React+TS+Tailwind、`desktop/` Tauri、`cli/`）；Go module 初始化；build tag 约定（`personal` / `small-business` / `enterprise`）；`make build-personal` 可产出二进制——2026-09-07
- [x] **统一数据模型层 DataModel Interface**：`datamodel.SupplierStore`（Put/Get/Delete/List/Ping/Close）+ `SupplierFilter`/`Sort`/游标分页；业务代码只依赖接口；`datamodel/contract` 契约测试套件约束所有适配器行为（memory 与 sqlite 跑同一套 15 例）——2026-09-07
- [x] **SQLite + JSON1/JSONB 存储适配器**（`internal/datamodel/sqlite`，personal + small-business 共用）：文档经 `jsonb(?)` 存 JSONB BLOB 列、`json(doc)` 读回；过滤/排序/游标字段反范式为类型化列（province/city/district/rating/qual_rank/owner/status/visibility/时间戳）+ `supplier_categories` 品类边表（EXISTS OR 语义）；WAL 模式、busy_timeout、外键级联；关键字走 `search_text` LIKE（ESCAPE 处理 %/_）与 memory 语义对齐。无 build tag（适配器属基础设施层，允许全 tier 编译），由 storefactory 按 tag 接线——2026-09-07
- [x] **供应商文档模型**：按 PRD 样例实现 `Supplier` 结构 —— `basic_info`（公司名/信用代码/法人/注册资本/成立日期/经营范围/省市区地域/联系人/电话/邮箱/网址/地址/供应商类型）、`qualifications[]`、`categories[]`、`products_services[]`、`performance_history[]`（三维评分：交付/质量/配合度+综合评分）、`risk_flags`（空壳检测+人工审核）、`visibility`、`owner`、`shared_with`、`custom_fields`（自由扩展）、`change_log[]`、`attachments[]`、`watched`（关注态）、`vis_enforcement`（可见性处置子文档）、`vis_exception`（申诉例外）、`blacklist_reason`；ID 规则 `sup_2026_000xxx`（6 位 crypto/rand，服务层撞号探测重试）——2026-09-08
- [x] **供应商 CRUD API**（Go 后端 HTTP）：创建（含 ID 撞号探测+录入去重+空壳检测自动标记）、读取、更新（自动写 change_log：字段/旧值/新值/时间/来源；编辑触发风险重开/可见性例外清除）、删除（归档语义）、列表——2026-09-08
- [x] **手动录入表单（前端）**：`frontend/`（Vite + React 18 + TS + Tailwind）；核心字段表单 + 动态资质/产品/绩效行 + 动态自定义字段 + 联系方式/类型/地址/官网 + 三维绩效评分输入 + 可见性选择按 `features.visibility_levels` 门控；create POST / edit PATCH（服务端 diff 自动写 change_log）；实时查重（400ms 防抖，三档：确凿/疑似/近似）——2026-09-08
- [x] **供应商详情页"文档卡片"UI**：基本信息/资质/品类/产品服务/绩效/自定义字段/附件/变更记录各为独立可折叠 `DocumentCard`；自定义字段泛化渲染；风险标记横幅 + 空壳检测逐条信号；归档/恢复/编辑/黑名单/合并/关注操作；列表摘要卡 + 关键词搜索 + 地域/品类/资质/评分筛选 + 游标"加载更多"（列表只取 summary 字段）；本地优先排序（📍 徽标）；关注筛选；到期提醒横幅——2026-09-08
- [x] **Monorepo 前端骨架**：`frontend/` 已就位（dev 走 Vite 代理 /api→127.0.0.1:7612；产物 dist/ ~55KB gzip）；Tauri 2.0 `desktop/` Rust 壳已完成——sidecar 自动拉起 + 内嵌 dist/ + 打包 Win/macOS/Linux；Go HTTP 层已加 localhost CORS（Tauri origin `tauri://localhost` 直连 sidecar）；API base 运行时检测（`resolveBase()` 区分 Tauri 生产 / dev / 浏览器访问）——2026-09-08
- [x] **附件管理**：`objectstore` 端口 + `localfs` 适配器；`objectfactory` 按 tier 接线；HTTP `POST /suppliers/{id}/attachments`（multipart，双重 50MB 限制）与 `GET /attachments/{key...}` 流式下载（RFC 5987 中文文件名）；`DELETE /suppliers/{id}/attachments?url=`（按引用计数删除，合并共享引用后不悬空/孤儿）；服务层自动写 change_log；前端附件卡支持上传/下载/删除——2026-09-08
- [x] **Excel 批量导入**：`internal/importer`（纯 Go `xuri/excelize`）——模板下载 + 列别名自动建议映射 + 列映射确定性修复（重复表头按索引稳定取值、未知键报错）；`POST /import/commit` 批量写入 + 逐行查重（确凿/疑似/近似三档，skip 模式可跳过确凿/疑似）；品类分隔符切分、资质类型/等级成单条资质、绩效评分列映射；空行跳过；坏行报错不阻断整批。实测 **1000 条 ~1.85s（富数据）<3s 红线**——2026-09-08
- [x] **基础全文搜索**：SQLite FTS5 trigram 分词器（中文无需词典，≥3 字子串命中）+ 短词 LIKE 回退（<3 字走参数化 LIKE）+ 拼音（全拼+首字母，go-pinyin）+ 建筑行业同义词（商砼↔商品混凝土等，索引期扩展）；bigram 协调启发式（跨字段"杭州混凝土"命中，front/back 锚点防误配）；FTS5 病态关键词（引号/OR/NEAR/反斜杠等）不报错；搜索接口 `search.Index` 已抽象，引擎无关中文分析逻辑放 `internal/search`，未来 Meilisearch/ES 复用同一套——2026-09-07（FTS5），2026-09-09（bigram+健壮性）
- [x] **条件筛选**：地域（省/市/区）、品类、资质等级（未知等级 400 而非静默关闭硬过滤）、评分范围组合过滤 + 排序；结构化查询走存储层；本地优先排序（prefer_province/prefer_city 三元组 keyset）——2026-09-09
- [x] **列表页性能防护**：游标分页（禁 OFFSET 深翻页）、单页最多 100 条、列表只返回摘要字段、搜索 3s 超时；5000 家库 p99：FTS 关键词搜索 ≈42-47ms（红线 200ms）、地域+品类筛选 ≈13ms（红线 100ms）——2026-09-09
- [x] **srm-cli 命令行工具**：`search`/`add`/`list`/`info`/`export`/`compare`/`backup`/`restore`/`duplicates`/`merge`/`blacklist`/`unblacklist`/`risk`/`review`/`expiring`/`watch`/`unwatch`/`notifications`/`visibility`/`appeal`/`resolve-appeal`/`preference` 等命令齐备；CJK 宽度对齐输出；`reorderFlags` 让 flag 可写在位置参数后；空白 id 就地拒绝——2026-09-09
- [x] **数据导出与备份**：`internal/exporter` 出站适配器——JSON bundle（全字段保真）+ XLSX（固定列中文表头，含网址/主营产品列，自定义字段动态加列）；`GET /api/v1/export`（共享筛选、游标翻页全量、50k 上限）；数据备份导出（VACUUM INTO 一致性快照 + 附件 zip + manifest）；应用内一键恢复（暂存-重启-原子换入 + 回退副本 + 布局类型冲突拒绝）；前端设置页备份/恢复卡片——2026-09-08（备份导出），2026-09-09（恢复+审计）
- [x] **Tauri 2.0 桌面打包**：React 前端嵌入；Go 后端作为 sidecar 随应用启动自动拉起（监督进程：崩溃自动 respawn，第 5 次短命代 give-up；SIGTERM/SIGINT 干净收子进程）；单实例交接（端口占用时探测健康实例并 exit 0）；运行期断线自愈（5s 心跳+被动确认探针→down→无限重连+gateKey 重挂载）；启动连接闸门（500ms 轮询 features，最多 20s）；beforeBuildCommand 路径修复（裸 `npm run build`，cwd 固定为 frontendDist 父目录）；Linux deb 12MB / rpm 12MB / AppImage 86MB 真机验证通过；单二进制 Web 模式（`go:embed` 前端嵌入 suppliderd）；CI/CD 流水线（ci.yml + release.yml，`v*` tag 产出四平台桌面安装包 + 五平台 CLI/MCP 二进制 + SHA256SUMS）——2026-09-08（shell），2026-09-10（打包验证+监督进程+CI）
- [x] **Feature Flag 基础框架**：运行时配置控制功能开关（为三版功能矩阵打底），`featureflag` 包 + tier 接线；无配置 AI Key 时 AI 入口隐藏的前端/后端机制——2026-09-08
- [x] **MVP 验收走查**：全新机器双击安装 → 零依赖运行 → 录入 → Excel 导入 → 搜索筛选 → CLI 查询 → 导出 → 备份/恢复，全流程无外部服务；真机 E2E 覆盖：deb 解包安装 + AppImage 直跑，readyz/per-user 数据目录/CRUD+跨字段搜索/重启持久化/kill -9 respawn/SIGTERM 零孤儿——2026-09-10
- [x] **空壳特征检测（非 AI 规则引擎）**：R1xx 身份（R103 信用代码校验位 GB 32100-2015）/R2xx 档案（R201 成立天数/R202 资料缺失/R203 施工无资质/R204 资质重复）/R3xx 财务（R301 低资本，解析中文写法）；15 位旧工商注册号不误判；千分位逗号/中文日期写法生效；任一 high 或 ≥2 medium → shell_risk；列表 summary 反范式标志；人工审核闭环（verified/dismissed，编辑风险字段自动重开）；审核队列（keyset 分页）；HTTP/CLI/MCP/前端全链路——2026-09-08
- [x] **黑名单生命周期**：`Blacklist`/`Unblacklist`（可见的禁用警告，默认列表仍可见且醒目标记）；归档供应商拒绝列入；幂等；HTTP/CLI/MCP/前端全链路——2026-09-08
- [x] **录入去重（非 AI）**：三档匹配——确凿（18 位信用代码一致）/疑似（规范化名称一致）/近似（简称包含/拼音全等/一字之差，同省闸门）；全库扫描含黑名单/归档；Excel 批量导入逐行查重（整库索引一次，skip 模式跳过确凿/疑似，近似只警告）；"flag, don't block" 哲学；前端实时查重+合并选择器——2026-09-08（去重），2026-09-09（近似档+批量查重）
- [x] **人工合并重复供应商**：主档案定向合并（吸收品类/资质/产品线/绩效/附件/自定义字段/shared_with）；无项目名绩效用内容指纹去重；同资质补全空白字段（证书号/到期日）；附件按 URL 引用合并（字节不移动）；双方 change_log；重复方归档；守卫（自合并/归档/黑名单拒绝）；前端查重候选选择器——2026-09-08
- [x] **可见性策略收紧数据处置流程**：扫描不合规 → 7 天缓冲标记"待调整" → 超时自动降级 → 申诉暂停/裁决（grant 例外/deny 降级）；策略持久化（settings 键，随库备份）+ sidecar 开机+每 24h 自动处置 ticker；HTTP/CLI/MCP（只读 `visibility_violations`）/前端（设置页+列表徽标+详情横幅）；SQLite 生命周期测试覆盖——2026-09-08
- [x] **资质到期提醒（90/30/7 天 + 已过期）**：keyset 分页遍历在库供应商；日期解析接受 ISO/RFC3339/斜杠/点分/年月日四种布局；窗口分桶（expired/7d/30d/90d）；写入校验（含数字但无法解析的日期报 400）；通知去重（按证书身份 dedup key）；HTTP/CLI/前端横幅；可挂 cron——2026-09-08（提醒），2026-09-09（写入校验+日期解析修复）
- [x] **MCP 开放接入**：`internal/mcp` + `cmd/srm-mcp` 独立 stdio 二进制；JSON-RPC 2.0（stdlib 手写，无 SDK）；直连存储（与桌面 App 同一 SQLite 库，WAL 多进程并发）；10+ Tools（search/get/add/risk/compare/duplicates/expiring/visibility/list_notifications 等）+ Resources + Prompts + Skill 文件；per-message panic 隔离+超长行不杀死连接；`scripts/build-tools.sh` 五平台交叉编译 + `docs/mcp/SETUP.md` 接入指南；MCP 默认数据目录与桌面 App 一致（测试钉死）——2026-09-08（MCP），2026-09-09（健壮性+打包分发）
- [x] **本地供应商偏好（本地优先排序）**：`SupplierFilter.PreferProvince/PreferCity`（排序提示，不缩小结果集）；三元组 keyset（prio, 排序键, id）；settings 键持久化；业务层自动应用偏好（HTTP/CLI/MCP 同源）；前端设置页+列表 📍 徽标+勾选单次关闭——2026-09-09
- [x] **三维绩效评价（交付/质量/配合度）端到端**：`EffectiveScore()`（总评优先，否则三维均值）；Create/Update 校验 0-5；前端表单卡片式输入；详情页维度渲染；CLI info 维度显示；Excel 导入支持绩效评分列——2026-09-08
- [x] **前端供应商对比/比价**：列表勾选+浮动条+CompareView 横向表格（地域/品类/资质/评分/维度均分/合作项目数/价格区间/风险状态）；并发拉取完整档案——2026-09-08
- [x] **关注供应商 + 应用内变更通知铃铛**：`NotificationStore`（独立表，归档/合并/删除不丢历史）；dedup key 折叠+空键离散；best-effort 副作用钩子（黑名单/归档/合并/风险/到期）；60s 轮询铃铛；前端关注/筛选/通知；SQLite+memory 契约测试；多次重启幂等 E2E——2026-09-09
- [x] **无空格中文检索（bigram 协调）**：`search.DocumentText`（统一可检索文本，含省/市/区）；`CJKCoordination`（≥4 rune 汉字 term 切重叠 bigram，半数覆盖+front/back 锚点+ASCII 片段连续出现）；SQLite 与内存适配器共用同一参考匹配器（parity 测试）——2026-09-09
- [x] **前端引入单测（vitest）**：`filterQuery`/`listQuery` 纯函数+7 例测试；CI 加 `npm test` + `npm run typecheck`——2026-09-09
- [x] **API 健壮性审计**：畸形分页游标→400（ErrInvalidPagination 哨兵）；附件 key 穿越/控制字符→400（ErrInvalidKey 哨兵）；畸形策略体→400（decodeOptionalJSONBody 统一 helper）；可选 JSON 体被吞解码错误收敛；黑盒扫描约 50+ 畸形用例确认无 500——2026-09-09
- [ ] **Windows NSIS + macOS dmg 真机安装验证**（需人工/远端，CI 无法替代）：`v*` tag 后下载四平台 Release 安装包真机安装确认（macOS 未签名需解除隔离；Windows NSIS 与两 mac dmg 本机无法验证）；Linux 已由 2026-09-10 本机全量打包+真机验证覆盖
- [ ] **代码签名/公证**：TAURI_SIGNING_PRIVATE_KEY secrets 配置以启用 Tauri 自动更新；macOS 公证（Apple Developer ID）；Windows 代码签名证书

## High Priority — 测试报告改善项（Post-MVP UX & AI）

> 来源：2026-09-10 桌面应用全面测试报告（92 项测试，19 个可执行改善项）
> 格式：`[测试报告编号] 问题描述 → 修复方向`

### P0 — 立即修复（用户体验地基）

- [x] **TR-01 全局 Toast/通知基础设施**：应用不存在 Toast 组件，所有写操作（创建/编辑/黑名单/归档）成功后无反馈。引入轻量 Toast（`ToastProvider` + Context + Portal，约 40 行零依赖），所有 `submit()` 成功后 `toast('✓ 操作成功')`。涉及 `App.tsx` 全局——2026-09-10
- [x] **TR-02 所有下载操作添加反馈**：4 个下载入口（Excel 模板/导出 Excel/JSON 备份/zip 备份）全部零反馈。用 `fetch()` + `Blob` 替代 `a.click()` 可追踪下载完成，下载前 `toast('⬇ 准备中…')`，完成后 `toast('✓ 下载完成')`。涉及 `SupplierList.tsx`/`ImportView.tsx`/`SettingsView.tsx`——2026-09-10
- [x] **TR-03 搜索框改为受控组件**：`defaultValue` → `value`（受控）+ 300ms 防抖即时搜索 + 🔍 搜索按钮 + ✕ 清除按钮 + placeholder 改为"搜索公司名/信用代码/法人/品类…（输入即搜索）"。涉及 `SupplierList.tsx`——2026-09-10

- [ ] **TR-02b 附件下载链接也需 fetch+Blob 化（TR-02 新发现）**：`SupplierDetail.tsx` 附件列表仍用 `<a href={apiUrl(url)} target="_blank">`，在 Tauri webview 内 Content-Disposition: attachment 的保存行为未经真机验证（可能不弹保存框/无反馈）；应复用 `api.download()` + toast。未纳入 TR-02（范围限定 4 个导出/备份/模板入口）。

### P1 — 下一迭代（表单可用性 + 品牌一致性 + 布局固定）

- [x] **TR-04 地域填写省市级联选择器**：当前 3 个纯文本 `<input>` 无级联无校验，可输入"火星"。改用 `chinese_regions` 数据包 + 三个级联 `<select>`；可选 `onPaste` 正则拆分整段地址。涉及 `SupplierForm.tsx`——2026-09-10
- [x] **TR-05 供应商类型选项扩展**：当前仅 6 项 `<datalist>`，改为 `<select>` 9 大分类（施工承包/设计咨询/物资供应/设备租赁/物流运输/检测认证/技术服务/劳务服务/其他）+ 选"其他"展开自由文本。涉及 `SupplierForm.tsx`——2026-09-10
- [x] **TR-06 成立日期智能粘贴**：当前 `<input type="date">` 仅接受 ISO 格式，粘贴企查查格式"2005年3月15日"被拒绝。改为 `<input type="text">` + `onPaste` 正则归一化（支持 ISO/斜杠/点号/中文/天眼查 API 格式）。涉及 `SupplierForm.tsx`——2026-09-10
- [x] **TR-07 统一应用内 Logo 与桌面图标**：应用内是纯色方块+"供"字（CSS 文本），桌面图标是渐变层叠文档卡片（`genicons` Go 程序生成），品牌断裂。新建 `Logo.tsx` 组件用内联 SVG 复现 `genicons` 设计，替换 `App.tsx` 中的 `<span>供</span>`，同时作 Favicon——2026-09-10
- [x] **TR-08 全局 Header 粘性固定**：所有 5 个页面滚动后顶部 Header 消失。`<header>` 添加 `sticky top-0 z-50`。涉及 `App.tsx`——2026-09-10
- [x] **TR-09 列表页搜索/筛选栏粘性固定**：滚动后搜索框和筛选条件消失。标题行+筛选栏包裹在 `sticky top-[57px] z-40 bg-slate-50` 容器中。涉及 `SupplierList.tsx`——2026-09-10
- [x] **TR-10 详情页操作按钮栏粘性固定**：滚动后编辑/归档/黑名单等按钮消失。标题+操作按钮容器添加 `sticky top-[57px] z-40 bg-white`。涉及 `SupplierDetail.tsx`——2026-09-10
- [x] **TR-11 表单页标题栏粘性固定**：滚动后标题和取消按钮消失。标题栏添加 `sticky top-[57px] z-40 bg-slate-50 py-2`。涉及 `SupplierForm.tsx`——2026-09-10
- [x] **TR-12 React Error Boundary**：应用无 Error Boundary，单个组件渲染错误导致白屏。在 App 根节点包裹 `ErrorBoundary`，捕获后显示友好错误页 + 重试按钮。涉及 `App.tsx`——2026-09-10
- [x] **TR-13 长表单锚点导航**：新建/编辑表单 9 个分区内容长，无快速跳转。左侧 sticky 锚点导航或顶部进度条，点击跳转到对应分区。涉及 `SupplierForm.tsx`——2026-09-10
- [x] **TR-14 DocumentCard 折叠/展开动画**：当前直接切换 `display` 无过渡。用 `max-height` CSS 过渡或 `react-transition-group` 实现平滑展开。涉及 `Card.tsx`——2026-09-10

### P2 — 后续打磨

- [x] **缺陷修复：`compact()` 把数字 0 当未设置，`min_rating=0`/`max_rating=0` 被丢弃**：前端序列化保留 0；后端新增 `SupplierFilter.MaxRatingSet` 存在性标志，显式 `max_rating=0` = 仅无评分（与缺省返回全部区分）；min=0 自然匹配全部。httpapi 测试钉死。——2026-09-10
- [ ] **TR-15 品牌色板补全**：`tailwind.config.js` 中 brand 色仅 50/100/500/600/700 五阶，缺 200/300/400/800/900 中间色阶
- [ ] **TR-16 Emoji 替换为 SVG 图标**：用 Heroicons 或 Lucide 替换 ⚠ 🚫 ✓ ⚖ 🔔 ⏰ 📍 等状态 emoji，保证跨平台一致性
- [ ] **TR-17 暗色模式支持**：利用 Tailwind `dark:` 前缀添加暗色模式，跟随系统或手动切换
- [ ] **TR-18 键盘快捷键**：添加 Ctrl+N（新建）、/或 Ctrl+K（搜索）、Esc（返回）等快捷键，设置中提供说明

### AI — 个人版 AI 模型接入（独立长线）

- [ ] **TR-19-A AI Gateway 双 Provider 适配器**：实现 `OpenAIAdapter`（覆盖 DeepSeek/通义/GLM/Moonshot/Ollama）+ `AnthropicAdapter`（Claude/DeepSeek Anthropic 端点）；配置存储（SQLite settings 表）；`POST /api/v1/ai/config` + `GET /api/v1/ai/test` 端点；启动时探测 provider → `WithAIState(true/false)`；设置页 AI 配置卡片（预设提供商快捷填充 + 测试连接）。自建双 Adapter 仅用 `net/http`（零依赖、与现有 Gateway 接口无缝衔接）。**阶段 1，2-3 天**
- [ ] **TR-19-B OCR 辅助录入**：前端"图片录入"按钮；后端 `POST /api/v1/ai/ocr` 上传图片 → 调用 `Gateway.Complete()` 带视觉提示词 → 解析 JSON 结果预填表单。**阶段 2，2-3 天**
- [ ] **TR-19-C Excel 智能列映射**：`ImportView.tsx` 预览阶段增加"AI 建议映射"按钮；后端 `POST /api/v1/ai/excel-map` 发送表头+示例值 → AI 返回映射建议 JSON。**阶段 3，1-2 天**
- [ ] **TR-19-D 自然语言搜索**：搜索框旁增加"AI 搜索"模式切换；后端 `POST /api/v1/ai/nl-search` 转自然语言为结构化筛选参数。**阶段 4，2-3 天**
- [ ] **TR-19-E 文档分析搜索 + 语义搜索**：文档上传+向量化存储；`Gateway.Embed()` 实现（bge-m3 模型）；文档分析推荐供应商。**阶段 5，5-7 天**
- [ ] **TR-19 备注（小打磨，随相关改动顺手做）**：~~`compact()` 0 值问题（已修，见 P2 顶部 2026-09-10）~~；设置页本地偏好保存成功消息应 3-5 秒自动消失；合并重复按钮可改为"合并重复档案…"带省略号风格

## Medium Priority — 小企业版（验证期）

- [ ] **五级可见性权限体系**（基础已落地）：等级 0-4（仅自己/指定人/本部门/指定部门/全公司）；查询时按 owner + shared_with + 部门归属过滤；个人版已开放 0/1 + 完整处置状态机；**待办**：部门归属过滤（需用户与部门管理先落地）、指定人/指定部门过滤
- [ ] **用户与部门管理**：用户账号、部门层级树、角色（录入者/部门管理者/管理员）；RBAC + 可见性双轨；组织架构 Excel 导入
- [x] **管理员可见性策略配置**：各等级启用/禁用、默认等级、缓冲天数；策略持久化（settings 键）；HTTP GET/PUT /api/v1/visibility/policy；前端设置页——2026-09-08
- [x] **策略收紧数据处置流程**：扫描→7 天缓冲→自动降级→申诉流程（完整状态机 + sidecar 自动 ticker）——2026-09-08
- [ ] **供应商审核入库流程**：录入后管理者审批 → 入库；审批状态机。**已落地部分**：空壳检测人工审核闭环（verified/dismissed）、黑名单生命周期
- [ ] **企查查/天眼查 API 对接**：入库时按企业名称+统一社会信用代码自动填充工商信息；API Key 加密存储；费用预算上限与预警。**已预留**：`risk_flags` 字段保留外部信号（被执行人/行政处罚）不被引擎覆盖
- [ ] **工商变更自动监控**：定时任务（每日/每周轮询）检测法人/经营范围/注册资本变更 → 自动更新档案 + 写 change_log（source=api_sync）→ 推送通知关注者。**已落地降级路径**：本地资质到期扫描（无 Key 100% 可用）
- [x] **资质到期提醒**：提前 90/30/7 天通知 owner/关注者（本地扫描+应用内通知，已完整落地）——2026-09-08
- [x] **风险标记与空壳检测**：rule engine 扫描（R1xx/R2xx/R3xx 规则）→ `risk_flags` 标记 + 风险预警；黑名单/归档生命周期；人工审核闭环——2026-09-08
- [x] **合作记录与绩效评价**：项目合作归档；交付/质量/配合度评分（三维）；绩效历史进入档案并影响搜索排序（`recomputeRating`）——2026-09-08
- [x] **基础项目比价（非 AI）**：多供应商横向对比表（价格/交付/资质），手工选择供应商生成对比；CLI compare + 前端 CompareView——2026-09-08
- [ ] **AI Gateway 统一适配层**（个人版也需实现，见 TR-19-A）：双 Provider 适配器（OpenAI 格式覆盖 DeepSeek/通义/GLM/Moonshot/Ollama + Anthropic 格式覆盖 Claude/DeepSeek Anthropic 端点）；模型路由（按任务类型）、token 用量统计与月度报告、相同请求缓存、主模型失败 fallback；无 Key 时整体降级。**已预留**：`ai_gateway` 接口骨架 + taskqueue（channel 内存队列）。个人版自配模型需兼容 OpenAI 与 Anthropic API 格式协议
- [ ] **AI 辅助录入**：OCR（PaddleOCR/Tesseract）识别营业执照/资质证书 → LLM 抽取结构化字段自动填表；Excel 列名 AI 智能映射（feature flag 门控位已留）
- [ ] **文档分析搜索**：上传 PDF/Word/Excel/图片 → OCR → LLM 提取需求要素 → bge-m3 embedding → Qdrant 内嵌向量相似搜索 + 关键词 + 地域/资质硬过滤 → 推荐列表 + 匹配理由 + 自动比价表
- [ ] **自然语言搜索**："杭州本地能做市政工程的二级资质以上供应商" → LLM 转结构化 Filter
- [ ] **AI 比价与风险报告**：多报价方案对比摘要推荐；工商变更记录 → 空壳风险评估报告；供应商档案一段话摘要
- [x] **MCP Server**：CLI 命令映射为 MCP Tools；供应商数据映射为 Resources；预置 Prompt 模板；随包发布 Markdown Skill 文件（命令语法 + 典型用法）——2026-09-08
- [ ] **Meilisearch 适配器（小企业版）**：个人版**不做**内嵌 Meilisearch（Rust 独立 server 二进制，破坏"单二进制零外部依赖"硬约束）；个人版搜索继续用 SQLite FTS5；Meilisearch 适配器应在小企业版（Docker Compose 独立容器）实现同一 `search.Index` 接口，照 FTS5 测试对照
- [ ] **MongoDB 存储适配器**（`//go:build enterprise` / small-business）：BSON 文档操作、复合索引（可见性+品类+地域）；与 SQLite 适配器跑同一 contract test 套件
- [ ] **Docker Compose 一键部署**：Go-Zero 单体 + Nginx + MongoDB + Meilisearch + MinIO；安装脚本
- [ ] **MinIO/对象存储抽象**：S3 兼容接口，本地 FS / MinIO / S3 可切换。**已落地**：`objectstore.Store` 端口 + `localfs` 适配器 + `objectfactory` 接线
- [ ] **操作审计日志**：录入/修改/删除/可见性变更/导出全记录，按时间/用户/类型查询（小企业版基础级）
- [ ] **异步任务队列**：内存 channel 实现抽象接口（已落地），为 NATS 替换做准备（变更监控、AI 推理任务）
- [ ] **通知系统**：变更推送、到期提醒、审批通知。**已落地**：应用内通知（铃铛+dedup）；**待办**：企业版通知服务把 `VisibilityViolation`/`Notification` payload 扇出到钉钉/企微（接口形状已预留）
- [ ] **共享贡献度激励**：共享越多搜索优先级越高、贡献度指标（对抗"平台退化为个人工具"风险）

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
- [ ] **Tauri 自动更新**：GitHub Releases / 自建更新源增量更新。**已预留**：TAURI_SIGNING_PRIVATE_KEY 后挂位
- [ ] **钉钉/企业微信通讯录同步**
- [ ] **行业模板扩展**：装饰/园林/市政/弱电行业预置供应商分类与字段模板
- [ ] **CI/CD 三产物流水线**：单仓提交 → `-tags personal/small-business/enterprise` 三目标编译 → Tauri 包 / Docker 镜像 / Helm Chart → GitHub Releases / Registry / Helm Repo。**已落地**：personal tag 的 CI/CD（ci.yml + release.yml）

## Completed

- [x] Project initialization（Ralph 脚手架、PRD 转换完成）——2026-09-07
- [x] Backend monorepo 骨架：Go module、build tag 三 tier 接线（storefactory/tier/featureflag）、domain 文档模型、supplier service（change_log diff、评分聚合、归档/恢复）、HTTP API、srm-cli 雏形、memory 参考适配器——2026-09-07
- [x] DataModel 接口 + contract 契约套件 + SQLite JSONB 适配器（personal/small-business 已接线；enterprise 占位待 Mongo）——2026-09-07
- [x] 前端（React+TS+Tailwind/Vite）：列表搜索筛选 + 游标分页、手动录入/编辑表单（动态自定义字段+联系方式+三维绩效）、文档卡片详情页（风险/审核/黑名单/合并/关注）、CompareView 对比页、SettingsView 设置页（可见性策略+本地偏好+备份恢复）、NotificationBell 通知铃铛、启动连接闸门+断线自愈；AI 入口按 /features 隐藏——2026-09-08
- [x] 附件管理（本地 FS）：objectstore 端口 + localfs 适配器 + objectfactory 接线 + multipart 上传/流式下载/引用计数删除 + 前端附件卡——2026-09-07
- [x] Excel 批量导入：importer 适配器（模板/预览/列映射/提交+确定性修复+逐行查重+绩效评分列+官网/产品列）+ service.Import 批量 + HTTP 三端点 + 前端手动列映射 UI；1000 行 1.85s<3s——2026-09-08
- [x] 数据导出与备份 + srm-cli export/compare：exporter 适配器（JSON 全量 bundle / XLSX 模板兼容往返+网址/产品列）+ `GET /api/v1/export` + CLI 两命令 + 前端导出按钮；数据备份导出（VACUUM INTO 快照 + 附件 zip + manifest）+ 应用内一键恢复（暂存-重启-原子换入+回退+布局冲突拒绝+全链路往返测试）——2026-09-08
- [x] SQLite FTS5 搜索引擎：trigram 分词 + 短词 LIKE + 拼音（全拼+首字母）+ 同义词（索引期扩展）+ bigram 协调（跨字段命中）+ FTS5 病态关键词健壮性 + 5000 家库延迟红线复测（p99 ≈42-47ms / 13ms）——2026-09-07/09
- [x] 空壳特征检测（非 AI 规则引擎）：R1xx/R2xx/R3xx 规则 + R103 信用代码校验位（GB 32100-2015）+ 15 位旧注册号不误判 + 千分位/中文日期修复 + 人工审核闭环（verified/dismissed + 编辑重开）+ 审核队列 + 前端列表/详情——2026-09-08
- [x] 黑名单生命周期：Blacklist/Unblacklist（可见禁用警告+幂等+归档拒绝）+ HTTP/CLI/MCP/前端——2026-09-08
- [x] 录入去重（非 AI）：三档匹配（确凿/疑似/近似）+ 全库扫描含黑名单/归档 + Excel 批量逐行查重 + 前端实时查重+合并选择器——2026-09-08/09
- [x] 人工合并重复供应商：主档案定向合并 + 内容指纹去重 + 资质补全 + 附件引用合并 + 守卫 + 前端查重候选选择器——2026-09-08
- [x] 可见性策略收紧数据处置流程：完整状态机（扫描→缓冲→降级→申诉）+ 策略持久化 + sidecar 自动 ticker + SQLite 生命周期测试 + HTTP/CLI/MCP/前端——2026-09-08
- [x] 资质到期提醒（90/30/7 天 + 已过期）：keyset 分页 + 四种日期布局解析 + 写入校验 + 通知去重 + HTTP/CLI/前端——2026-09-08/09
- [x] MCP 开放接入：internal/mcp + cmd/srm-mcp + JSON-RPC 2.0 + 10+ Tools/Resources/Prompts + Skill 文件 + 直连存储 + per-message panic 隔离 + build-tools.sh 五平台交叉编译 + SETUP.md——2026-09-08/09
- [x] 本地供应商偏好：三元组 keyset + settings 持久化 + HTTP/CLI/MCP 同源 + 前端设置页+📍 徽标——2026-09-09
- [x] 三维绩效评价端到端：EffectiveScore（总评优先/三维均值）+ 校验 + 前端表单/详情/CLI/Excel 导入——2026-09-08
- [x] 前端供应商对比/比价：列表勾选 + CompareView 横向表格——2026-09-08
- [x] 关注供应商 + 通知铃铛：NotificationStore（独立表+dedup）+ best-effort 钩子 + 60s 轮询 + 前端关注/筛选/铃铛 + 契约测试 + E2E——2026-09-09
- [x] CI/CD 流水线：ci.yml（backend/frontend/cross-compile/shell jobs）+ release.yml（四平台桌面安装包+五平台二进制+SHA256SUMS）——2026-09-09
- [x] Tauri 2.0 桌面 shell + 单二进制 Web 模式：sidecar 拉起 + CORS + API base 运行时检测 + beforeBuildCommand 路径修复 + Linux deb/rpm/AppImage 真机验证——2026-09-08/10
- [x] sidecar 单实例交接：端口占用探测健康实例并 exit 0 + 启动闸门 + 断线自愈 + 监督进程（respawn+SIGTERM+崩溃环闸门）+ E2E——2026-09-09/10
- [x] 前端启动连接闸门：500ms 轮询 features 最多 20s + 启动屏 + offline 重连按钮 + gateKey 重挂载——2026-09-09
- [x] 无空格中文检索（bigram 协调）：search.DocumentText + CJKCoordination + front/back 锚点 + ASCII 片段连续 + parity 测试——2026-09-09
- [x] 前端单测（vitest）：filterQuery/listQuery 纯函数 + 7 例 + CI npm test——2026-09-09
- [x] API 健壮性审计：畸形游标/附件 key 穿越/控制字符/畸形策略体 → 4xx（哨兵错误映射）；可选 JSON 体解码错误统一 helper；黑盒 50+ 用例确认无 500——2026-09-09
- [x] SQLite 多进程并发写测试：两句柄同文件 40 goroutine 交替 Put + WAL+busy_timeout 零 SQLITE_BUSY——2026-09-09
- [x] srm-mcp 默认数据目录测试：三平台与 Tauri identifier 一致性钉死——2026-09-09
- [x] 供应商 ID 撞号修复：Create 前探测 Get→regenerate（upsert 接口下适配器不报 ErrConflict）——2026-09-09
- [x] 本机 Rust 工具链验证：cargo check 抓到并修复 blocking_recv API 漂移；Cargo.lock 提交钉版；ci.yml shell job 修复 sidecar 依赖——2026-09-10
- [x] 本机 tauri build 全链路：Linux deb 12MB/rpm 12MB/AppImage 86MB 真机验证（readyz/per-user 数据/CRUD+跨字段搜索/重启持久化/kill -9 respawn/SIGTERM 零孤儿）——2026-09-10
- [x] 三构建标签运行时验证 + go vet 全量复检：personal/small_business/enterprise 三 tag 均编译运行通过——2026-09-08
- [x] 单二进制 Web 发行链路验证：build-frontend → embed → suppliderd（UI+API 共存 ~24MB 单文件）——2026-09-08
- [x] README 补齐：三种使用方式 / CLI / 数据备份 / 架构红线——2026-09-08
- [x] 录入表单补齐联系方式/类型/地址/官网——2026-09-08
- [x] Excel 导入补齐官网/主营产品列 + 绩效评分列——2026-09-08
- [x] 附件删除（引用计数，合并共享引用后不悬空/孤儿）——2026-09-08
- [x] CLI 端到端冒烟 + xlsx 导出成功提示修复——2026-09-09
- [x] 备份/恢复链路审计（布局类型冲突拒绝 + 全链路 SQLite 往返测试）——2026-09-09
- [x] Excel 导入列映射确定性修复（重复表头稳定取值 + 未知键报错）——2026-09-09
- [x] 空壳检测规则修复（15 位旧注册号/千分位/中文日期）——2026-09-09
- [x] Excel 导出补齐网址/主营产品列——2026-09-09
- [x] 列表/分页层审计（未知资质等级 400 + 本地优先分页属性测试）——2026-09-09
- [x] 可见性处置状态机审计（申诉审计轨迹修复 + SQLite 持久化生命周期测试）——2026-09-09
- [x] 合并重复供应商数据丢失修复（无项目名绩效折叠 + 同资质证书号/到期日被丢）——2026-09-09
- [x] 资质到期日解析接受中文常见写法（斜杠/点分/年月日）——2026-09-09
- [x] sidecar HTTP 服务失败改优雅退出（不再跳过 store.Close）——2026-09-09
- [x] FTS5 关键词健壮性审计（病态输入不报错，回归测试）——2026-09-09

## Notes

### 设计要点

- **附件/对象存储设计要点**：① `objectstore.Store` 是 S3 形状端口（Put/Get/Remove/URL），localfs 是纯标准库实现，MinIO/S3 后补同接口；具体实现只允许出现在适配器包 + `objectfactory` 接线文件。② 50MB 限制做两层：HTTP `MaxBytesReader` 兜整个请求体（413），适配器内 `io.LimitReader(Max+1)` 兜流式字节。③ 对象 key 服务端生成 `<供应商id>/<unixnano>_<safeBase>`，`resolve()` 用 filepath.Rel 防 `..` 穿越，拒绝 <0x20/0x7f 控制字符。④ 下载只从对象库取字节，`Content-Disposition` 用 RFC 5987 `filename*=UTF-8''`。⑤ 删除按引用计数（`AttachmentReferenced` 全库扫描），最后一个引用消失才删字节。⑥ 空 DataDir 对象库为 nil，上传端点返 501。
- **Excel 导入设计要点**：① `internal/importer` 是入站适配器，依赖 excelize + supplier.CreateInput；业务层不碰 excelize（红线已验证）。② 列映射三步：Template（独立「填写说明」sheet）→ Inspect（表头别名自动建议，未识别列默认归 custom）→ Build（列按索引升序处理，标量字段首个非空列优先，列表字段多列并集去重）。③ 批量逐行 Create，坏行进 ImportReport.Errors 带行号不阻断整批。④ 逐行查重：整库索引一次，每行 O(索引) 匹配；skip 模式跳过确凿/疑似，近似只警告。
- **导出/CLI 设计要点**：① `internal/exporter` 与 importer 同为适配器，是 excelize 仅有的两个出站/入站包。② JSON bundle 是全量保真格式（change_log/attachments/custom_fields 全在），定位个人→小企业互导；XLSX 是人工交换格式，固定列中文表头与导入模板一致。③ 导出走服务层游标翻页收集 FULL 文档（50k 安全上限）。④ CLI `reorderFlags` 把 flag 移到位置参数前解析。⑤ compare 表格按终端宽度填充，CJK 字符算 2 列。
- **前端架构要点**：前端不引入 react-router 等重依赖，用 App 内 `View` 联合状态做路由；`src/api.ts` 是唯一发 fetch 的地方（`guardedFetch` 统一收口）；`types.ts` 是 Go domain 的 TS 投影。dev 用 Vite proxy 同源转发 sidecar；Tauri 生产构建用 `resolveBase()` 运行时检测（`__TAURI_INTERNALS__` + origin 判定）直连 `http://127.0.0.1:7612`。AI 入口靠 `features.ai_*` 为 false 自动隐藏。连接状态机 `connecting → online → down → offline`，5s 心跳+被动确认探针，down 时无限重连+gateKey 重挂载业务树。
- **SQLite 适配器设计要点**：① `doc BLOB` 列用 `jsonb(?)` 写入、`json(doc)` 读回。② 过滤/排序字段反范式成类型化列 + 索引，json_extract 留给 ad-hoc custom_fields 查询。③ 品类用 `supplier_categories` WITHOUT ROWID 边表 + EXISTS 实现 OR 语义。④ 游标分页是 `(prio, sort_col, id)` 三元组比较。⑤ `:memory:` DSN 每连接独立库，必须 `SetMaxOpenConns(1)`。⑥ 文件库用 WAL + busy_timeout(5000) + foreign_keys。⑦ VACUUM INTO 产一致性快照用于备份。
- **适配器不打 build tag**：`datamodel/sqlite` 包无 tag（基础设施层允许任意 tier 编译），差异只在 storefactory 的 `tier_*.go` 接线文件。
- **SQLite 驱动选型（已定）**：用 `modernc.org/sqlite`（纯 Go、无 cgo）而非 `mattn/go-sqlite3`（cgo）——Tauri sidecar 需交叉编译单二进制，cgo 要求目标平台 C 工具链，与"零依赖双击安装"冲突。驱动只允许出现在 `datamodel/sqlite` 适配器包内。
- **MVP 刻意精简**：PRD 风险提示明确建议 MVP 阶段只做 Tauri + Go + SQLite，MongoDB 与 AI 后续引入——但所有接口（存储/搜索/队列/对象存储/AI）必须从第一天就抽象。
- **目标行业切口**：建筑/工程行业（皮包公司、垫资、资质造假、本地化偏好痛点最集中）。
- **开源策略**：个人版 AGPL 协议开源，防竞品直接商用。
- **API 成本风险**：企查查/天眼查 1-5 元/次，个人版限制为手动查询，小企业版设费用上限，企业版谈批量折扣。
- **中文处理是硬需求**：全文搜索必须有好的中文分词 + 拼音 + 同义词；LLM 场景优先国产模型（DeepSeek-V3 约 2 元/百万 token）。
- **性能红线勿忘**：分页上限 100 条、搜索 3s 超时、连接池 80% 限流。
- **数据源参考**：企查查开放平台（变更后 24h 内同步）、天眼查 API（200+ 接口实时）、国家企业信用信息公示系统（免费无 API）、全国建筑市场监管平台（资质/注册人员/处罚，每日更新）。
- **Meilisearch 不进个人版**：Meilisearch 是 Rust 独立 server 二进制、无可内嵌 Go 库，塞进个人版会破坏"单二进制零外部依赖 / ~15MB"硬约束。个人版搜索继续用 SQLite FTS5；Meilisearch 适配器应在小企业版（Docker Compose 独立容器）实现同一接口。
- **MCP 边界**：MCP 只暴露只读/录入类工具；归档/合并/黑名单/可见性处置不向 Agent 开放（破坏性动作由人在 UI/CLI 执行）。MCP 直连存储（与桌面 App 同一 SQLite 库，WAL 多进程并发安全），不依赖 sidecar。
- **sidecar 监督设计**：监督只管进程存活（CommandEvent::Terminated 时 respawn，第 5 次短命代 give-up，存活 ≥10s 清零）；readyz/前端数据视图重挂载仍由前端 down→online 闸门负责，两层职责不重叠。SIGTERM/SIGINT 复用 `SidecarInner::shutdown()`，150ms 等回收。
- **Tauri 2 beforeBuildCommand cwd 陷阱**：Tauri 2 CLI 执行 hook 的工作目录**固定为 frontendDist 配置项的父目录**，`--prefix ../frontend` 会解析成仓库外路径。必须用裸 `npm run build`。
- **全局 Toast 缺失（测试报告发现）**：整个应用不存在 Toast/通知容器、不存在 loading/spinner 元素、无进度条组件。这不是个别操作遗漏，而是全局缺失反馈基础设施。所有下载通过原生 `<a>` 或动态 `<a>`+`click()` 实现，应用无法感知下载是否开始/完成/失败。必须引入 Toast 组件 + 用 `fetch()`+`Blob` 替代 `a.click()` 追踪下载完成。
- **页面滚动固定缺失（测试报告发现）**：所有 5 个页面（列表/详情/表单/设置/导入）的顶部 Header 均未实现固定定位，关键操作区（搜索栏/操作按钮/标题栏）也跟随滚动消失。根因是 `<header>` 缺少 `sticky top-0 z-50`，`<main>` 未设独立滚动区域。分层固定方案：Header `sticky top-0 z-50` + 列表筛选栏 `sticky top-[57px] z-40` + 详情操作栏 `sticky top-[57px] z-40` + 表单标题栏 `sticky top-[57px] z-40`。57px ≈ Header 高度。
- **AI Gateway 双格式适配（测试报告发现）**：OpenAI 与 Anthropic API 格式有 5 个关键差异：① System 消息位置（messages[0] vs 顶层字段）② Anthropic 的 max_tokens 必填 ③ 响应结构（字符串 vs 内容块数组）④ 流式 SSE 事件格式完全不同 ⑤ 认证头不同（Bearer vs x-api-key + anthropic-version）。DeepSeek 同时提供 Anthropic 兼容端点。推荐自建双 Adapter（仅用 `net/http`，零依赖），不引入第三方 SDK。
- **地域填写无校验（测试报告发现）**：3 个纯文本 `<input>` 输入省/市/区，无级联过滤无校验。本地优先排序依赖 province/city 精确匹配，用户手动输入容易打错字。从企查查复制"浙江省杭州市西湖区"整段文本需手动拆分到三个框。需引入 `chinese_regions` 数据包做级联 + `onPaste` 正则拆分。
- **日期格式不兼容（测试报告发现）**：`<input type="date">` 仅接受 ISO 格式，企查查/天眼查/工商系统的常见格式（`2005年3月15日`/`2005/3/15`/`2005.3.15`/`2005-03-15 00:00:00.0`）全部被拒绝。改为 `type="text"` + `onPaste` 正则归一化，约 30 行零依赖。

### 踩坑记录（重要！）

1. **E2E 测试必须带 `-tags personal`**：无 tag 的开发构建默认接线 **memory 存储**，不是 SQLite！用 `go build ./cmd/suppliderd` 起的守护进程行为（无 FTS、无拼音、无同义词）与个人版发行物完全不同。
2. **同义词嵌套陷阱**：同义词组成员互相包含——`砼` 是 `商砼` 的子串、`混凝土` 是 `商品混凝土` 的子串。朴素 `strings.Contains` 会误判。`SynonymExpansions` 先把其他较长成员从文本中擦除（最长优先），再判断独立出现。
3. **同义词必须同时进两条索引路径**：扩展词只放 FTS content 列不够——短词查询走 LIKE on `search_text`，若 `search_text` 不含扩展词，"搜商砼找写混凝土的文档"方向失效。`buildSearchText` 统一追加扩展词。
4. **测试夹具的地域要与名字一致**：fixture 默认地域是杭州，命名为"宁波建材贸易"的供应商若不显式设置 Region.City=宁波，"关键词 + 城市筛选 AND"的测试会假阳性通过/失败。
5. **upsert 接口下随机 ID 的唯一性不能靠适配器**：`SupplierStore.Put` 契约是 upsert（Update 需要），适配器永远不返回 ErrConflict；6 位随机后缀撞号时 Put 会静默覆盖旧供应商。唯一性探测（Get→regenerate）必须放在知道 create/update 之别的服务层。
6. **本地优先是"排序提示"不是"过滤条件"**：放 `SupplierFilter.PreferProvince` 但绝不进 WHERE——进了 WHERE 就变成只看本地供应商；keyset 必须带上 prio 成为三元组，否则跨页边界会重行/漏行。
7. **map 迭代序依赖**：`for col, key := range mapping`（Go map 迭代随机序），两列值冲突时每次导入结果可能不同。今后任何"多来源写入同一字段"的处理必须先把 key 排序，禁止裸 `range map` 决定覆盖。
8. **哨兵错误必须在 HTTP 边界正确分类**：适配器早已返回语义化哨兵（ErrInvalidPagination/ErrInvalidKey），但 `writeServiceError` 只特判了 ErrNotFound→404 和子串匹配→400，哨兵落入默认分支→500。后续新增错误类型应在 `writeServiceError` 登记 `errors.Is` 映射，而非依赖错误信息子串。
9. **可选 JSON 体被吞解码错误**：`_ = Decode(...)` 吞掉解码错误，Go 在类型报错前已为零值分配指针→畸形请求静默落库最严等级。统一用 `decodeOptionalJSONBody`（空体容错，有体但畸形一律 400）。
10. **tauri-build 2 的 build.rs 无条件拷贝 externalBin**：`copy_binaries` 对 `bundle.externalBin` 无条件执行，按当前 TARGET 拼 `binaries/suppliderd-<triple>`，缺失即 exit(1)。cargo check 同样跑 build.rs。check 前必须先 build-frontend（generate_context 嵌入 dist）+ build-sidecar。
11. **tauri 2 blocking_recv API 漂移**：tauri 2.11.5 的 `async_runtime::Receiver` 重导出 `tokio::sync::mpsc::Receiver`，`blocking_recv(&mut self) -> Option<T>`（不再是 `Result<Option<T>>`）。必须 `while let Some(event) = rx.blocking_recv()`。

### 架构红线自查

- 业务代码（supplier service / httpapi / exporter / importer / mcp）只依赖 `datamodel`、`search`、`objectstore` 等接口；具体驱动（modernc sqlite、go-pinyin）仅出现在 adapter 包（sqlite/、localfs/）与 storefactory/objectfactory 接线文件中。
- `go build -tags personal` 与 `-tags enterprise` 均编译通过（每轮验证）。
- excelize 仅出现在 importer/exporter 适配器包（grep 红线已验证）。
- 三 tier（personal/small_business/enterprise）编译+vet 干净。
- 发布五目标交叉编译成功（linux-amd64 23M / linux-arm64 22M / windows-amd64 24M / darwin-amd64 24M / darwin-arm64 23M）。
- 前端 vitest 7/7、tsc 干净、vite build 干净（测试代码不进 bundle）。
- `go test ./...` 与 `-tags personal` 全绿（13 包）。
- Cargo.lock 已提交钉版（tauri 2.11.5 / plugin-shell 2.3.6）。
