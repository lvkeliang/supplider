# fix_plan.md — Supplider 开发记录与待办

Ralph 每轮循环在此记录：已完成项、踩过的坑、下一步最重要的事。
新条目追加到对应章节顶部（最新在前）。

## 下一步优先级（个人版 MVP）

1. **Tauri 安装包真机验证**：shell 代码与 sidecar 已完成并通过 HTTP 层 E2E（见下），
   本机无 Rust 工具链未跑过 `tauri build`；**现已由 release.yml 在 GitHub runner
   （含 Rust）上构建**——打 `v*` tag 即产出四平台安装包。剩余验证只需：tag 后下载
   Release 里的安装包，确认体积（目标 ~15MB 安装器；实测 stripped sidecar ~16MB，
   嵌入后安装器预计 20-25MB 量级）与双击拉起 sidecar；未签名，macOS 需解除隔离属性。
   图标已由 `backend/cmd/genicons` 离线生成（PNG/ICO/ICNS）。后挂：代码签名/公证
   （TAURI_SIGNING_PRIVATE_KEY secrets）以启用 Tauri 自动更新。
2. **Meilisearch 适配器（小企业版，非个人版）**：个人版**不做**内嵌 Meilisearch——
   Meilisearch 是 Rust 独立 server 二进制、无可内嵌 Go 库，塞进个人版会破坏"单二进制
   零外部依赖 / ~15MB"硬约束。个人版搜索继续用 SQLite FTS5（已在 `search.Index` 接口
   之后，行为被测试钉住）；Meilisearch 适配器应在小企业版（Docker Compose 独立容器）
   实现同一接口，届时照 FTS5 测试对照即可。
3. ~~**合并重复供应商（人工）**~~ 已落地（见下），前端重复档案选择器亦已落地（详情页
   🔀 合并重复直接列出查重候选，黑名单/归档候选禁用并说明；手输 id 保留为兜底）。
   后挂可选项：MCP 暴露 merge 工具（当前刻意不暴露给 Agent——合并会归档数据，属于需
   用户明确指示的破坏性操作，与"Agent 不擅自拉黑/处置"同一边界）。
4. 空壳检测后续增强：外部工商/司法数据（被执行人/行政处罚）接入位已留（RiskFlags 字段
   保留不被引擎覆盖）。黑名单生命周期已落地（见下）；外部数据"被执行人→自动预警"可后挂。
5. ~~**可见性策略配置 UI + 定时处置**~~ 已落地（见下：策略落库 GET/PUT /api/v1/visibility/policy、
   设置页 SettingsView、sidecar 开机+每 24h 自动处置 ticker、MCP/CLI/HTTP 统一读持久化策略）。
   后挂：企业版通知服务把扫描报告推钉钉/企微的接口已用同一 `VisibilityViolation` 形状预留
   （小企业版/企业版接线即可）；策略变更审计可随操作审计日志（管理员平台）一并做。
6. ~~srm-mcp 打包/分发~~ 已落地（见下：`scripts/build-tools.sh` 交叉编译 srm-mcp +
   srm-cli 五平台产物到 dist/tools/ + sha256sums；`docs/mcp/SETUP.md` 安装配置指南）。
   ~~GitHub Releases 工作流~~ **已落地**（见下：ci.yml + release.yml；`v*` tag 自动
   产出四平台桌面安装包与五平台 CLI/MCP/daemon 二进制并附 SHA256SUMS）。

## 已完成

### 2026-09-09：黑盒审计修复——畸形策略体不再落库最严等级（0），附件 key 含控制字符返回 400 而非 500

延续"哨兵错误是否在 HTTP 边界被正确分类"的方法论，对在跑的 personal/SQLite 真机做
第二轮畸形输入黑盒扫描（本轮重点此前少测的 watch/通知/merge/申诉/策略/偏好/附件/
导入/恢复约 50 个用例）。绝大多数端点分类正确（merge/appeal/risk-review 空体 400、
附件 50MB 边界 201 / 50MB+1 413、假 xlsx/坏 multipart 400、cursor/穿越 key 4xx），
但发现两处真问题，均已修复并真机复验：

- **可见性策略畸形体静默落库最严等级（最危险）**：`PUT /visibility/policy` 的 body
  `{"max_level":"x"}` 返回 **200** 并把 `max_level=0`（0 仅自己，五级中最严）持久化、
  **立即触发一次 enforce 处置扫描**。根因：handler `_ = Decode(...)` 吞掉解码错误，
  而 Go 在类型报错前**已为 `*int` 分配零值**——指针非 nil，"必填"校验通过，0 又恰是
  合法的最严等级。畸形请求因此从"无操作"变成"把全公司可见性收紧到仅自己并启动数据
  处置"。修复：新增统一的 `decodeOptionalJSONBody`（空体容错以保留 CLI query 参数
  路径，**有体但畸形一律 400**，超 MaxBytesReader 同样 400），替换全部 8 处
  `_ = Decode`（policy/watch/blacklist/merge/appeal/resolve/risk-review/local-pref）；
  `?buffer_days=非数字` 也从静默按 0 改为 400。
- **附件 key 含控制字符 500**：`GET /attachments/a%00b` 在 localfs 路径检查全部通过
  （NUL 不是 `..`、不越界），最终 `os.Open` 在 syscall 层 EINVAL → 500。修复：
  `localfs.resolve` 拒绝任何 <0x20 / 0x7f 字符（NUL/CR/tab…），包装既有
  `objectstore.ErrInvalidKey` → HTTP 400；S3 兼容存储同样禁止这些字符，未来 S3
  适配器保留同一检查。
- 测试：`httpapi/visibility_policy_test.go`——4 种畸形策略体均 400 且 GET 确认
  **configured 仍为 false**（没落库）、空体+query 的 CLI 路径仍 200、6 个可选体
  端点对坏 JSON 统一 400 而空体仍 200；`attachment_test.go` 增 NUL/CR 编码 key →400；
  localfs 穿越测试增 `a\x00b`/`cr\r\n.txt`（Put+Get 双侧 ErrInvalidKey）。
- 验证：default+personal 全量测试绿；三 tag 编译；gofmt/vet 净；真机复验
  畸形策略 400 且不落库、空体 query 200、NUL key 400、格式正确无对象仍 404、
  watch 空体 200 / 坏体 400。
- 方法论：本轮把上轮"哨兵映射"之外的另一类输入错误——**可选 JSON 体被吞解码错误**
  ——收敛为单一 helper；今后新增"body 或 query 二选一"端点必须复用
  `decodeOptionalJSONBody`，不要再写 `_ = Decode`。

### 2026-09-09：运行期断线自愈——sidecar 中途崩溃/被杀后窗口不再永久报错，自动重连并重挂载业务视图

启动连接闸门（见上一轮）只解决了冷启动；**online 之后 sidecar 进程消失**
（崩溃、被任务管理器结束）时，Tauri shell 不会重启 sidecar（main.rs 仅在退出
时 kill），各视图各自停在错误态、铃铛 60s 轮询静默失败，窗口再也救不回来。
注意 ca07fa3 解决的是反向情形（shell 死、孤儿 sidecar 活）；本轮补齐对称面。
用户的自救路径"再双击一次"会启动第二个 shell，新 sidecar 在已释放的 7612
端口与 SQLite 文件上正常起服（实例交接逻辑保证不与活实例冲突），于是旧窗口
只差一个能感知恢复的前端闸门——本轮落地，纯前端、零新依赖。

- **api 层健康事件钩子**（`api.ts`）：全部 5 处 `fetch`（JSON 请求、multipart
  附件/导入、zip 恢复）收口到 `guardedFetch`；fetch reject（端口无响应）报
  `'failure'`，**任何收到响应的请求（含 4xx/5xx）报 `'success'`**——后端报错
  不等于后端死，绝不误翻闸门。新增 `api.readyz()` 与 shell/实例交接用同一端点。
- **App 连接状态机扩为 `connecting | online | down | offline`**（`App.tsx`）：
  - online 看门狗：5s `/readyz` 心跳，连续 2 次失败才翻 down（避免单次抖动/
    休眠恢复误报）；任何业务请求失败立即用 readyz **确认探针**再翻（上传取消
    等一次性 abort 探针会拿到响应，不翻）。
  - down：卸载整个业务树（停掉各视图轮询与铃铛），全屏"本地服务连接中断/正在
    自动尝试重新连接/数据不会丢失/请退出后重新双击启动"，1s 间隔**无限重连**
    （恢复可能随时由第二次双击带来，不能像冷启动 20s 后放弃），另附"立即重试"。
  - 恢复时重新拉 features 并 `gateKey++`：以 React key 重挂载业务树，所有视图
    在已验证连通之后才发首请求，旧错误态不会残留。
- 验证：tsc 通过，`build-frontend.sh` 内嵌成功（bundle 215.4KB / gzip 66.8KB，
  仅 +0.8KB），产物断言含三条断线文案；Node fetch 真机时序建模：服务存活
  readyz 200 → 关闭端口 fetch 立即 reject（翻闸门信号）→ 同端口重新起服务
  readyz 200（无限重连轮询自愈）。后端零改动，`go build` 与全量测试不受影响。
- 仍后挂（非本轮）：shell 侧 Rust 自动重启 sidecar（`CommandEvent::Terminated`
  时 respawn）可消除"必须再双击一次"，但本机无 Rust 工具链、CI 仅在 release
  tag 编译 Rust，改动需专门一轮连同 ci.yml 增加 `cargo check` 一起做。

### 2026-09-09：无空格中文拼接检索——"杭州混凝土"跨字段命中（bigram 协调），SQLite 与内存适配器共用同一参考匹配器

中文用户检索天然不带空格：地域+品类"杭州混凝土"是一个连续 run，但文档里
"杭州"在地域字段、"混凝土"在品类/产品字段，FTS5 trigram 短语要求整串连续，
于是**零命中**。个人版零依赖硬约束不允许引入分词词典，本轮落地词典无关的
bigram 协调启发式，并把它放进引擎无关的 `internal/search` 包，供 FTS5（今天）
与 Meilisearch/ES（以后）共用同一行为。上一轮循环在该特性上超时留下未提交
半成品；本轮完成它、修掉一个精度回归并钉死 SQL↔Go 行为对等。

- **统一可检索文本 `search.DocumentText`**（新 `search/document.go`）：公司名、
  **省/市/区**（旧 blob 不含地域，跨字段查询的根源）、信用代码、法人、经营范围、
  品类、产品名、自定义字段键值 + 索引期同义词扩展。SQLite 的 FTS content 列与
  `search_text` 列、内存适配器的匹配文本全部由它生成（`buildSearchText` 变为
  薄别名），三处分叉的 blob 拼接收敛为一处。
- **协调计划 `search.CJKCoordination` → `CJKPlan`**（`search/cjk.go`）：对
  ≥4 rune、汉字严格占多数的连续 term 切重叠 bigram 去重，命中文本中
  `ceil(半数)` 个 bigram 即覆盖达标；另需 **front 锚点**（前两个 bigram 至少
  出现一个——挡住"有限公司"类泛后缀单独命中）与 **back 锚点**（末 bigram 必须
  出现——挡住"同行业长前缀、尾部概念不同"的误配）；term 内每个 ≥2 字符的
  ASCII 字母数字 run（`001`、`c30` 等编号/型号）必须**连续出现**，编号永不被
  bigram 打散。短词/拼音/非汉字主导 run 仍走原来的连续子串路径。
- **精度回归（本轮修复）**：上一轮半成品只有 front 锚点，`TestExportWalksAllPages`
  暴露：205 家"测试导出供应商NNN"被"…供应商001"**全部命中**——6 个共同前缀
  bigram 已达半数阈值。back 锚点挡住纯汉字版本；ASCII 片段定长要求让
  `010/101/201` 不再混入，只有真正含连续"001"的 001、001x 命中（与原子串语义
  一致，11 条）。
- **两个适配器镜像同一逻辑**：内存适配器 `matchesKeyword` 删除私有 naive
  子串实现，直接调 `search.AllTermsMatch(DocumentText(d), kw)`；SQLite
  `keywordPredicates` 保持无协调词时的原 SQL 形状（合并 MATCH + 短词 LIKE，
  普通查询零变化），仅当存在可协调 term 时走逐 term 分支：`exact（trigram
  MATCH / LIKE）OR（bigram CASE 计数覆盖 AND front OR AND back AND 各 ASCII
  片段 LIKE）`，各 term 之间仍 AND。
- 测试：`search/cjk_test.go`（跨字段、锚点形状、不合格 term、ASCII 片段、
  纯汉字共享前缀拒绝、AND 多 term）；sqlite 包新增跨字段端到端、编号不扩散、
  以及 **`TestFTSCoordinationParityWithReferenceMatcher`**——13 个查询矩阵上
  SQL 命中 ID 集合必须与 `AllTermsMatch(DocumentText(doc))` **逐集合相等**
  （矩阵守卫：含可能只走 py 拼音列的 ASCII 词会直接 fail，防测试静默漂移）。
- 验证：default + `personal` 全量测试绿；`personal/small-business/enterprise`
  三 tag 编译通过（加上无 tag 即四配置）；gofmt/vet 净。后挂：前端搜索框可加
  一行"空格分词、拼音缩写也可"的提示；Meilisearch 适配器落地时照对等矩阵复用。

### 2026-09-09：前端启动连接闸门——sidecar 冷启动未就绪时自动重试，不再首屏永久"后端未连接"

Tauri 外壳注释一直声称"窗口立即打开、**前端轮询直到 API 就绪**"，但前端实际只在
App 挂载时发一次 `features()`、列表也只在挂载时拉一次，**根本没有重试**。桌面窗口在
Go sidecar 完成启动前就会首绘（冷盘 / 杀软扫描未签名 exe 可能拖几秒），此刻
`fetch` 直接 `Failed to fetch`：顶栏停在"后端未连接"琥珀标、列表停在错误/空态，必须
手动重启应用——直接伤害"双击即用"。本轮补上与外壳 20s readiness 预算对齐的启动闸门。

- **`App.tsx` 连接状态机** `connecting → online → offline`：挂载即以 500ms 间隔轮询
  `GET /api/v1/features`，最多 40 次（≈20s，对齐 `src-tauri/main.rs` 的 100×200ms
  readyz 预算）。成功→`online` 并保存 feature matrix；超时→`offline`。`connecting`
  显示品牌启动屏（旋转指示 + "正在启动本地服务…/首次启动可能需要几秒钟"）；`offline`
  显示"无法连接本地服务"+ 安全软件提示与**重新连接**按钮（回到 connecting 再轮询）。
- **只在 online 后挂载业务视图**（list/form/detail/import/settings/compare 连同铃铛），
  使各视图挂载期的首次请求必然成功；运行中之后的偶发失败仍由各视图就地报错（既有行为，
  不在本轮扩大范围）。移除了旧的一次性 `featError` 琥珀标（被闸门取代）。纯前端、零新
  依赖（只用 Tailwind 的 `animate-spin`，无额外库），web/Tauri 同源同构。
- 验证：tsc 通过、`build-frontend.sh` 构建并内嵌（bundle 214.6KB / gzip 66.4KB），
  产物实测含三条启动态文案；用 500ms 轮询脚本对"延迟 1.5s 才拉起 sidecar"建模——sidecar
  一起来下一次轮询即拿到 `features` 200（约 1s 内翻转），证明闸门依赖的"端口未通时
  fetch reject / 就绪后 200"转换成立；sidecar 内嵌的新 bundle 实测可下发并含启动闸门。
  本机无前端测试运行器（无 vitest/playwright），逻辑为简单的 setTimeout 轮询状态机，
  以 tsc+真机网络时序验证；后端无改动，全量 Go 测试不受影响。
- ~~后挂（非本轮）：online 之后 sidecar 中途崩溃的全局重连~~ **已落地**（见本轮顶部：
  5s 心跳 + 被动失败确认探针 → down → 无限重连 + gateKey 重挂载）。

### 2026-09-09：API 健壮性——畸形分页游标 / 附件 key 不再 500（输入错误统一 4xx）

对在跑的 personal/SQLite 真机做畸形输入黑盒扫描时发现两处"客户端输入错误被当成
服务器错误（HTTP 500）"，对桌面本地应用尤其刺眼（界面只显示"internal error"，
且监控/排障会把用户坏链接误判成崩溃）。本轮把它们归位为 4xx，并全量回归确认其余
端点对畸形输入也不产生 500。

- **分页游标**：`GET /suppliers?cursor=<坏值>` 任何非空非法游标（非法 base64
  `!!!`、合法 base64 但非游标 JSON、`null`、含 NUL 的 `%00…`、非法百分号字节
  `%ff%fe`）此前都 **500**。根因：适配器早已返回语义化哨兵 `datamodel.ErrInvalidPagination`
  （memory/sqlite 两适配器都经 `DecodeCursor` 返回同一哨兵），但 `writeServiceError`
  只特判了 `ErrNotFound`→404 和基于错误信息子串（"required"/"must be"）的 400，
  哨兵落入默认分支→500。修复：mapper 增加 `errors.Is(ErrInvalidPagination)`→**400**
  `invalid pagination cursor`（客户端可丢弃坏游标从第一页重来，而不是当成崩溃）。
- **附件 key 穿越**：`GET /attachments/..%2f..%2f..%2fetc%2fpasswd` 返回 **500**。
  localfs 的 `resolve` 早已挡住越界（不读 root 之外、无文件泄露，已验证响应体不含
  宿主文件），但返回普通 `fmt.Errorf`，HTTP 无法归类。按"接口先行"补哨兵
  `objectstore.ErrInvalidKey`（与既有 `ErrObjectNotFound` 并列：坏 key=400、格式
  正确但无对象=404），localfs `resolve` 两处改用 `%w` 包装；mapper 增加
  `errors.Is(ErrInvalidKey)`→**400**。一处映射同时覆盖下载/上传/删除（后两者 key
  由服务端构造，仍走同一 mapper）；日后 MinIO/S3 适配器复用同一哨兵。裸 `/..` 路径
  由 net/http 自身清洗成 301，到不了处理器，不做特判。
- 测试：`httpapi/pagination_test.go`（该包此前无测试）——非法游标 5 种变体→400、
  空游标→200；用服务端签发的真游标 limit=1 翻页 3 家、断言无重复且正常终止。
  `httpapi/attachment_test.go`——接真实 localfs（t.TempDir）：百分号编码穿越 key
  →400 且不泄露文件；格式正确但不存在→404；未挂对象存储→501。并把既有 localfs
  穿越测试从"仅断言有错"收紧为 `errors.Is(err, ErrInvalidKey)`。全量 default+personal
  绿，四 tag 编译，gofmt/vet 净。
- 黑盒回归（personal 真机，覆盖变更/合并/风险/可见性/偏好/导入/通知/附件等约 30 个
  畸形用例：坏 JSON、错误类型、越界枚举/可见性、空体、非 xlsx 字节 multipart、向
  不存在供应商传附件等）：除上述两处外**没有别的 500**，分别得 400/404/501；
  `limit=999999` 仍被 `Normalize()` 钳制（红线：单页 ≤100）。
- 方法论沉淀：本地 API 输入校验已较完整，残留风险集中在"**哨兵错误是否在 HTTP 边界
  被正确分类**"。后续再引入面向客户端的错误类型时，应在 `writeServiceError` 登记
  `errors.Is` 映射，而非依赖错误信息子串（现有子串分支是历史兜底，可在专门一轮收敛
  为哨兵，本轮不扩大改动面）。

### 2026-09-09：sidecar 单实例交接——修复崩溃后再次双击应用永久"后端未连接"

桌面端 Tauri 固定在 `127.0.0.1:7612` 拉起 Go sidecar，正常退出时 Rust 在
`RunEvent::ExitRequested` 里 kill 子进程。但若 Tauri 进程被**强杀/崩溃**（任务管理器、
断电、panic），孤儿 sidecar 仍占着端口与 SQLite 文件；下次双击应用，新 sidecar
`ListenAndServe` 报 `bind: address already in use` 后在 goroutine 里 `log.Fatalf`
退出——界面永远卡在"后端未连接"，普通用户无法自救。本轮在 **Go 侧（可本地验证、零依赖、
四 tag 同一业务代码）**加单实例交接兜底。

- **最早闸门**（`cmd/suppliderd/main.go`）：把 `net.Listen("tcp",addr)` 提前到
  `flag.Parse` 之后、**`applyPendingRestore` 与 `storefactory.Open` 之前**。随后用持有的
  listener 跑 `srv.Serve(ln)`（不再 `ListenAndServe`）。这样：① 判断为"重复实例"而退出时
  **绝不打开/替换活库、绝不消费暂存恢复包**；② 首个实例从启动起就占住端口，消除"探测期间
  被第三方抢绑"的竞态。
- **交接规则**（`cmd/suppliderd/instance.go`）：bind 返回 `EADDRINUSE`（Windows 上
  `syscall.EADDRINUSE` 即 WSAEADDRINUSE 10048，`errors.Is` 跨平台成立）时探测占端口者：
  `GET /readyz` 需 200 且 `status=="ok"`，`GET /api/v1/features` 需 200 且
  `tier ∈ {personal,small_business,enterprise}`。两者满足→认定是健康的本产品 sidecar，
  新进程打日志后 **exit 0**（Tauri 前端轮询到仍在服务的实例，应用照常可用）；否则按原状
  `log.Fatalf`（别的程序占端口，绝不误退）。`awaitExistingInstance` 以 100ms 节奏轮询约 2s，
  覆盖"对方已绑端口但尚未就绪"的同时启动窗口；探测用短超时 ctx + `MaxBytesReader(64KiB)`，
  不被坏响应拖住启动。
- 测试：`instance_test.go` 6 例（httptest）——健康 personal 实例识别；三 tag tier 各识别；
  全 404 的**外部进程被拒**（且在等待窗口内返回、不长挂）；readyz 非 ok / tier 未知 / 无
  tier / 非 JSON 四变体被拒；端口已无监听快速失败；对方 503 两次后才变健康也能识别。全量
  default+personal 绿，四 tag 编译，gofmt/vet 净。
- E2E（personal 真机二进制）：A 用 dataA 起在 7701 → B 用**不存在的** dataB 同端口启动：
  **exit 0**、日志"another suppliderd ... healthy; exiting"、`dataB 未被创建`（证明闸门在
  restore/open 之前）、API 仍由 A（pid 不变）提供、端口仅一个监听者。再起 python
  `http.server`（/readyz=404）占 7702 → sidecar 探测约 2s 后 **exit 1** `listen ... address
  already in use`、dataC 未被触碰。
- 边界与取舍：新进程只"让位"不"接管"——无法跨平台拿到并 kill 占端口的孤儿；极少数强杀场景
  会残留一个孤儿进程，但**应用每次都能正常打开**（连接到存活实例），远优于永久断连。Tauri
  侧可后挂 `tauri-plugin-single-instance`（Rust，需 CI 编译验证）在窗口层直接复用既有实例，
  与本兜底互补；本轮不动 Rust（本机无 Rust 工具链，无法提交"已验证"的改动）。

### 2026-09-09：关注供应商 + 应用内变更通知铃铛（PRD 维护：变更推送通知关注者 + 资质到期提醒）

关注一家供应商后，它出现空壳风险 / 被拉黑 / 移出黑名单 / 归档 / 恢复 / 合并，或资质进入
90/30/7 天到期窗口，都会进顶栏铃铛的未读通知。纯本地、零外部依赖、无 AI；企业版日后把
**同一个 `datamodel.Notification` payload** 扇出到钉钉/企微即可，业务代码不动（可降级原则）。
上一轮循环在写完后端后超时，本轮：修复了前端一个导致整模块编译崩溃的语法错误并把整条
前端链路接通，再做 HTTP+CLI+MCP+多次重启的真机 E2E，最后提交。

- **数据模型端口**（业务代码只认接口，不认驱动）：`datamodel.Notification` +
  `NotificationStore`（`AddNotification`/`ListNotifications`/`CountUnreadNotifications`/
  `MarkNotificationRead`/`MarkAllNotificationsRead`）。通知独立成表，**归档/合并/删除供应商
  不丢通知历史**（supplier_name 反范式化，档案没了也能渲染）。
  - 去重：`DedupKey` 非空时同键折叠为第一条、返回 `inserted=false`；离散用户动作用空键，
    每次都提醒。SQLite 用 **partial unique index** `(dedup_key) WHERE dedup_key<>''` 配合
    `INSERT OR IGNORE` 实现，空键不受唯一约束；`KeepNotifications=500` 修剪最旧。
  - 同一套契约 `datamodel/contract/notification.go` 在 memory（参考实现）与 SQLite 两适配器
    上跑（钉住去重/最新优先/未读计数/已读语义），日后 Mongo 照跑同一套。
- **关注态**：`domain.Supplier.Watched`。个人 UI 态——**不写 change_log、不碰 UpdatedAt、
  不重排列表**；归档后仍可关注、关注与通知历史跨归档保留。`Summary.Watched` 出列表投影；
  `SupplierFilter.WatchedOnly`（SQLite 走 `json_extract(doc,'$.watched')`，低频视图不做热列）。
- **Service**（`supplier/notify.go`）：`SetWatched`（幂等）；内部 `notify` 是 **best-effort
  副作用**——store 无 feed / 未关注即 no-op，通知失败绝不让生命周期动作失败。钩子：`Update`
  仅在**新**空壳判定（`!wasRisk && ShellRisk`）报 danger；Blacklist（body 带原因）/Unblacklist、
  Archive/Restore、`MergeSuppliers`（主档案"吸收了重复"+ 被并档案"已并入谁并归档"双向提醒）。
  `NotifyWatchedExpiring` 复用既有 `ExpiringQualifications(90)`，按 90/30/7/expired 窗口给
  severity/标题/中文正文，dedup key = `exp:<sup>:<证书身份>:<窗口>`，证书身份取 cert_no
  否则 type|level|expiry（换发新证算新事件）；跨 90→30→7→过期各提醒一次，日扫/启动幂等。
- **后台接线**：`httpapi.runMaintenanceSweeps` = 可见性处置 + 到期通知，启动 5s 后与每 24h
  各跑一次，两个 sweep 互相隔离、panic recover（一个挂不挡另一个）。
- **入口**：HTTP `POST /suppliers/{id}/watch`、`GET /notifications?unread&limit`（附总未读）、
  `POST /notifications/read-all`、`POST /notifications/{id}/read`、列表加 `?watched=true`；
  CLI `watch/unwatch`、`notifications [--unread] [--all-read] [--json]`、`list/search --watched`；
  MCP 新增**只读** `list_notifications`（刻意不暴露关注/处置——与"Agent 不擅自拉黑/合并"
  同一边界，关注与处置走 CLI/界面），skill 文档同步一行。
- **前端（本轮补齐）**：修复 `api.ts` 中误留的 `),`（在 `markAllNotificationsRead` 之后，
  使整个 `api` 对象语法错误、tsc 报 30+ 错、前端完全编译不过）；顶栏挂载 `NotificationBell`
  （60s 轮询、下拉、未读红点 99+、点条目即已读并跳详情、"全部标为已读"、按 severity 出
  🚫/⚠️/🔔）；详情页动作区加 ☆关注/★已关注（归档/在库都显示，关注跨归档）；列表行加
  ★已关注 徽标与"★ 仅看关注"筛选；`types.ts`/`api.ts` 补类型与方法。
- 测试：`supplier/notify_test.go` 7 例（关注不动 change_log/时间戳；黑名单只通知关注者；
  风险仅新判定才报；归档/合并双向；watched-only 过滤；到期按窗口只报一次；到期跳过未关注）
  + 通知 store 契约 3 例 ×两适配器。全量 default+personal 绿，personal/small_business/
  enterprise 四 tag 编译通过，gofmt/vet 净，tsc+vite 通过（bundle 213KB）。
- E2E（personal/SQLite，HTTP+CLI+MCP stdio+多次重启）：关注 A→拉黑 A 出一条 danger 且
  未关注的 B 被拉黑**不**产生通知；单条已读未读清零；移出黑名单再出一条 info；
  `?watched=true` 只回 A；`DELETE` 归档出 archived 且 A 归档后仍 watched；建 5 天后到期资质
  的 C 并关注→重启后启动 sweep 产生**唯一**一条 7d danger（《建筑工程施工总承包二级》…仅剩 5
  天，dedup key 带 cert 号），**再重启仍只 1 条**（幂等）；黑名单 JSON `{"reason":...}` 正确
  进入通知正文；CLI `notifications`/`--unread --json`/`watch`/`unwatch`/`list --watched`
  （★ 标记、归档 A 默认隐藏）/`--all-read` 全对；MCP `list_notifications` 经 SRM_DATA_DIR
  直开 WAL 库，全量 4 条 newest-first、unread_only 正确；daemon 内嵌的新前端 bundle 实测含
  铃铛/关注/仅看关注。注意踩坑：MCP 与 CLI 寻址不同——CLI 走 HTTP（`SRM_API_ADDR`），
  srm-mcp 直接开数据目录（`--data-dir`/`SRM_DATA_DIR`，WAL 允许与在跑的 sidecar 并发）。

### 2026-09-09：应用内一键恢复/换机迁移（备份闭环收尾）——停应用解压覆盖不再是唯一路径

备份导出早已落地，但恢复只能"停应用、手动解压覆盖数据目录"，普通桌面用户做不到。
本环补齐**应用内恢复**：设置页/CLI 上传备份 zip → 服务端校验并**暂存** → 下次进程启动
（打开任何数据库连接**之前**）原子换入；现有库先整体挪进时间戳回退目录，坏恢复可救回。
纯本地、无外部依赖。**绝不替换正在打开的活库**。

- **暂存-重启-生效**（`internal/backup/restore.go`，驱动无关）：
  - `Stage(r,dataDir,check)`：上传体先落临时文件（zip 中央目录要 ReaderAt，也避免整包进内存），
    解压到同级临时目录（同文件系统→rename 原子）。校验链：合法 zip → 条目白名单
    （仅 `supplider.db`/`manifest.json`/`attachments/**`，`safeExtractPath` 挡
    **zip-slip 穿越**与绝对路径）→ 拒非普通文件（symlink/设备）→ 单文件/总量 2GiB、
    10 万条目（**zip-bomb** 闸）→ manifest format/version 匹配 → 适配器 `check` 过库。
    通过后替换旧 staging（旧的先 rename 走再删，rename 不能覆盖非空目录）；任何失败
    删除 work dir，活数据零触碰。
  - `ApplyPendingRestore(dataDir)`：**只在 main 开 store 前调用**。先把
    `-wal/-shm/db/attachments` 依次挪入 `restore.rollback-<UTC时间戳>`（WAL 必须先挪，
    绝不能残留在恢复后的库里），再把暂存 db/attachments rename 到位；失败按逆序回滚已挪
    文件并报错（接线层 `log.Fatal`——拒绝在半交换的树上开库）。`pruneRollbacks` 只留
    最新一份回退，反复恢复不撑爆磁盘。附件目录是**整体替换不是合并**（备份后新增的文件
    随其库一起消失，db 引用与磁盘始终一致）。
  - `PendingRestore` / `CancelRestore`（幂等）。
- **适配器校验**（`sqlite/check.go` `CheckSnapshot`，放在适配器包——backup 包不碰驱动）：
  `mode=ro` + `query_only` 只读打开候选文件（**不产生 WAL/SHM 副作用**），跑
  `PRAGMA integrity_check`，并要求存在 `suppliers` 表——任意健康的外来 .db 也不接受。
- **接线**：
  - build tag 分文件：`cmd/suppliderd/restore_personal.go`（personal：注入 sqlite.CheckSnapshot
    + boot apply）/ `restore_other.go`（!personal：nil funcs，端点 501；MongoDB 版另做归档路径）。
    `main.go` 在 `storefactory.Open` **之前**调 `applyPendingRestore`。
  - HTTP：`GET/POST/DELETE /api/v1/backup/restore`（状态/暂存/取消）。POST 收 raw
    `application/zip` 或 multipart `file`，`MaxBytesReader` 256MiB；Stage 任何失败一律 **400**
    （坏包/坏库，绝不是 500），成功 202 + manifest。nil Restore（内存开发构建）三方法全 **501**。
  - CLI：`srm-cli restore <备份.zip>`（暂存并提示完全退出再打开）、`srm-cli restore-cancel`。
  - 前端：设置页"数据备份与迁移"卡片加⬆上传（拖拽不可见 input）、暂存中琥珀横幅（备份时间/
    附件数/重启提示/取消按钮）、校验失败红条（明示未改任何数据）；换机迁移指引更新。
- 测试：`backup/restore_test.go` 4 例（暂存-换入-回滚全流程 + 旧附件被替换/新附件到位/
  WAL 不残留/恰一份 rollback；重复暂存最新者胜 + cancel；坏包/zip-slip/format/version/
  checker 拒绝；空 dataDir/nil checker）；`sqlite/check_test.go` 1 例（健康快照只读通过且
  不生 WAL、垃圾字节失败、健康但无 suppliers 表的外来库被拒）。全量默认+personal 绿，
  三 tag 编译，gofmt/vet 净，前端 tsc+vite 通过。
- E2E（personal/SQLite，真实 HTTP+CLI+多次重启）：建 A → 备份 → 建 B → 传垃圾 400 且
  未暂存 → 传真包 **202**（暂存期活库仍 2 家、staging 落盘 0600）→ 重启日志
  `restored library from backup created … previous library kept in a restore.rollback-*` →
  **只剩 A（列表与 FTS 均无 B）**、staging 已消费、rollback 目录生成；把 rollback 的 db 用
  **另一个 sidecar 实跑确认是健康的 2 家库**（回退可用）；CLI restore/restore-cancel 正确；
  python 造的"健康但无 suppliers 表"的 sqlite 经真实端点 → 400 `not a Supplider backup`；
  附件链路：A 备份前传 license → 备份 → 备份后建 C 并传文件 → 恢复重启 → A 附件**字节一致**
  可下载、C 与其文件双双消失（替换非合并）；默认内存构建 restore 三方法 + /backup 均 501。

### 2026-09-09：录入去重增加低置信度"近似"档（简称/同音字/笔误）——核心痛点①增强

原录入去重只有两档：信用代码一致=确凿、规范化名称完全一致=疑似。真实场景最常见的
**简称 vs 全称**（录入"杭州一建"，库里"杭州一建集团有限公司"）、同音字误写
（一建/亿建）、一字笔误（一建/一井）全部漏网。新增第三档 **possible（近似）**，
纯本地、O(n) 廉价规则、同省才提示，继续遵循"flag, don't block"。

- **三条保守规则**（`internal/supplier/dedup.go` `fuzzyNameMatch`，确凿/疑似都不命中才跑）：
  1. **包含**：短名 ≥4 字且覆盖长名 ≥40% 才成立（"杭州一建"⊂"杭州一建集团有限公司"
     成立；"一建"2 字不成立；"杭州一建"⊂"杭州一建机械施工有限公司" 4/14=0.29
     不成立——不同主体的通用字号不误报）；
  2. **拼音全等**：全名拼音一致（go-pinyin，无声调）且长度比 ≥85%、≥6 字，抓
     同音字；含拉丁字母/数字的名字跳过该规则（go-pinyin 会丢拉丁，残余中文会假碰撞）；
  3. **一字之差**：rune 编辑距离 ≤1（双指针线性扫描，无 DP 矩阵），长度差 ≤1、
     ≥6 字，抓非同音笔误。
- **同省闸门**：近似档仅在双方省份相同（或任一方省份缺失）时提示；确凿/疑似不受
  地域限制。匹配键（含预计算拼音）收敛为 `matchKey`，候选与库内条目同形。
- **性能**：库索引每条预算一次拼音（5000 条 ~毫秒级）；匹配规则全是字符串包含/
  等值/线性扫描，撑住批量导入 1000×全库配对。实测富数据 1000 行导入 2.36s
  （红线 3s，原 1.85s，DB 写入仍是大头）。
- **skip 语义收紧**（关键保守决策）：Excel"跳过重复"只跳确凿/疑似；**近似档永远
  只警告不跳过**——低置信度不能静默丢掉可能是全新的公司。排序仍是 strong→
  probable→possible，同档黑名单优先，故用 matches[0].Level 判定即可。
- **入口**：HTTP/CLI（`近似` 表格标签 + help 更新）、MCP（find_duplicates 描述与
  add 警告文案、skill 文档注明 possible 不得当同一主体处置）、前端三处徽标三档化
  （表单实时查重红色确凿/琥珀疑似/**灰色近似**、合并选择器、导入结果列；导入说明
  注明近似只警告不跳过）。
- 测试：`supplier/fuzzy_dedup_test.go` 6 例——包含/拼音/笔误命中与阈值护栏
  （2 字短名、低覆盖、跨省、两字差异、通用前缀不同行业均不误报）、排序
  strong 居首、**skip 模式下近似行仍创建而疑似行被跳过**。全量默认+personal 绿，
  三 tag 编译，tsc+vite 通过。
- E2E（personal/SQLite HTTP+CLI+MCP stdio）：建"杭州一建集团/建设有限公司"后，
  查"杭州一建"→2 条近似（互为包含）；查"杭州亿建建设"→1 条近似（拼音）；
  "杭州建材机械"对"杭州建材贸易"→0（护栏）；跨省→0；带标点全称→疑似仍命中；
  CLI 表格"近似"、MCP JSON level=possible+中文原因均正确。

### 2026-09-09：CI 与 GitHub Releases 流水线——一键产出四平台安装包

个人版 MVP 功能闭环后，最大缺口是"用户拿不到安装包"：`tauri build` 需要 Rust，
本机无法验证。本环补齐 CI/CD（PRD"一套 CI/CD 产出 Tauri 安装包"），全程本机可验证
的只有脚本/actionlint；真正的 Tauri 打包在 GitHub runner（有 Rust）上跑。

- **`.github/workflows/ci.yml`**（push master / PR，三 job）：
  - backend：gofmt 门禁（顺带修了历史遗留 `cmd/genicons/main.go` 未格式化）、
    `go vet`（default+personal）、`go test`（default memory 与 personal SQLite 两套）、
    编译检查 personal（含 `./cmd/...`）+ small_business + enterprise，tag 漂移即红；
  - frontend：npm ci → `build-frontend.sh`，校验 frontend/dist 与内嵌 webui 两处产物；
  - cross-compile：实跑 `build-sidecar.sh` 与 `build-tools.sh`（纯 Go modernc，
    Linux runner 即可交叉全部 5 triple），断言 15 个产物齐备，upload-artifact 留存。
- **`.github/workflows/release.yml`**（`v*` tag 触发）：
  - desktop 矩阵：windows-2022/NSIS、macos-15-arm/dmg、**macos-15-intel**/dmg、
    ubuntu-22.04/deb+AppImage。每 runner：setup-go/node/rust（rust target 按矩阵）、
    Linux 装 webkit2gtk-4.1 等系统依赖、npm ci、build-frontend、build-sidecar（
    一次产出全部 5 个 sidecar triple，tauri 只消费当前 target 的 externalBin），
    最后 `tauri-apps/tauri-action@v0` 经 `npx @tauri-apps/cli@^2` 打包发布。
  - binaries job（needs desktop）：build-tools + build-sidecar → 收 15 个二进制 +
    生成统一 SHA256SUMS → `gh release upload --clobber` 挂到同一 Release。
  - 无签名密钥时产出未签名安装包（Tauri 在 macOS 自动 ad-hoc 签名）；留了
    TAURI_SIGNING_PRIVATE_KEY 与 Tauri updater 的后挂位。
- 本机验证：`actionlint`（go1.26 工具链）零告警（发现 macos-13 标签已下线，改用
  macos-15-intel）；清空产物后实跑两个 build 脚本，5 triple sidecar / 10 个 CLI/MCP
  二进制全部产出；模拟 release 收集步骤（cp+sha256sum 16 文件）成功。
- README 补"通过 CI 发布"段；SETUP.md 的 macOS 隔离解除说明被安装包复用。
- **未在 GitHub 上实跑过**（需推送 + tag）；首次发布需盯一次 Windows/macOS runner，
  风险点：tauri-action 版本漂移、Git Bash 跑 .sh、macOS 未签名 dmg 的 Gatekeeper。

### 2026-09-09：本地供应商偏好（本地优先排序）全链路——PRD 五大痛点之"本地偏好"

产品立项五大痛点之一"本地供应商偏好"此前无落点。本环落地**常驻地域偏好**：
设置后，列表/搜索结果中本地供应商**自动排最前（仅排序，外地供应商不被筛掉）**，
配置随库备份，HTTP/CLI/MCP 三入口同源生效。纯本地、无 AI。

- **数据模型层**（`datamodel.SupplierFilter`）：加 `PreferProvince/PreferCity`
  排序提示（与地域过滤 `Province/City` 严格区分——提示不缩小结果集）；
  `datamodel.LocalPrio(prov,city,prefProv,prefCity)` 是**唯一**优先级公式
  （1=本地），两适配器必须一致；游标 `Cursor` 加 `Prio`，keyset 变为
  **三元组**（prio, 排序键, id）。
  - **SQLite**：ORDER BY `(CASE WHEN s.province=? AND (?='' OR s.city=?) THEN 1 ELSE 0 END) DESC`
    前置；keyset 谓词 `(prio < cp) OR (prio = cp AND <原二元组尾部>)`，CASE 表达式
    在谓词中重复两次、参数严格按占位符出现顺序绑定（prioArgs×2 + cp×2 + tail）。
  - **memory**：`sortDocs` 签名加 filter，prio tier 优先、tier 内回退原排序；
    desc/asc 两个方向都实现（asc 时本地 tier 仍居前）。
  - **contract 套件**加 `LocalPreferenceRanking`（两适配器跑同一套）：5 杭州+5 宁波+
    1 江苏，创建时间刻意交错，page size 3 走完全部分页——验证本地行全部在前、
    **跨行不重不漏**（三元组 keyset 正确性）、省级偏好时两省内在先江苏垫底。
- **服务层**（`internal/supplier/preference.go`）：`SaveLocalPreference/
  LoadLocalPreference/ClearLocalPreference`（settings 键 `local_preference`，
  复用既有 settings 缝，随库备份）；`Service.List` 在**业务层**自动把持久化偏好
  填入 query（不是 HTTP 层——CLI/MCP/gRPC 同一行为）：显式 query 提示 > 已保存
  偏好；`Query.NoLocalPreference` 单次关闭；损坏值回落空偏好不炸列表；每次列表
  多一次单行主键查询。
- **HTTP**：`GET/PUT /api/v1/preferences/local`（GET/PUT 回 `{province,city,
  configured}`；空省份 400；`PUT ?clear=1` 清除）；列表加 `prefer=0` 关闭、
  `prefer_province/prefer_city` 显式覆盖（MCP/高级调用可用）。
- **CLI**：`srm-cli preference [--province 省 --city 市] [--clear] [--json]`
  （别名 `pref`/`local`）；`list/search` 共享筛选 flag 加
  `--prefer-province/--prefer-city/--no-local`；生效时 stderr 提示地域、
  本地行前打 📍（显式 flag 与已保存偏好都计算）；白空格 `--province` 本地拦。
- **MCP**：零改动自动生效（共用 Service.List）；Skill 文档加"本地优先排序"说明，
  提示 Agent 优先解读排前的本地供应商，MCP 不提供修改入口。
- **前端**：设置页新增「本地供应商偏好（本地优先）」卡片（省必填/市可选/清除/
  configured 徽标/保存反馈）；列表页在已配置时显示「本地优先（浙江·杭州）」勾选
  （单次关闭 → prefer=0），本地行打天蓝色「📍 本地」徽标。
- 测试：`supplier/preference_test.go` 2 例（保存/加载/清除/损坏值回退；List 自动
  应用、仅排序不筛选、opt-out 恢复日期序、显式提示覆盖保存值、省级偏好）；
  contract +1（两适配器）。全量 `go test` 默认+personal 全绿，三 tag 编译通过，
  前端 tsc+vite 通过。
- E2E（personal/SQLite + 真实 HTTP + CLI + MCP stdio）：杭州(最早建)/宁波/
  南京(最新建)——无偏好=日期序；城市偏好=杭州居首、南京仍在（只排序）；
  `prefer=0` 恢复；FTS 关键词搜索同样本地优先；显式江苏提示覆盖；省级偏好
  宁波杭州在前南京垫底；**limit=2 走 keyset 三页 3 行不重不漏**；空省份 400；
  clear 恢复；CLI 📍/--no-local/--prefer-province 三况正确；MCP 搜索杭州居首。

### 2026-09-09：修复供应商 ID 撞号静默覆盖（Create 重试本是死代码）

实现本地偏好时跑全量测试偶现 `TestExportWalksAllPages` 204/205（memory 适配器），
排查发现一个**真实数据安全缺陷**：供应商 ID = `sup_<年>_<6 位 crypto/rand>`
（10⁶ 空间，生日悖论：~1.2k 行时撞号概率 50%，205 行热循环也有 ~2%）。Create 原有
撞号重试循环只认 `datamodel.ErrConflict`，但接口规定 **Put 是 upsert**（Update 依赖
该语义），memory 与 SQLite（`ON CONFLICT DO UPDATE`）**都不返回 ErrConflict**——
撞号时 Put 直接**静默覆盖另一供应商整份文档**（被覆盖方从列表消失，其 id 变成新公司）。

- **修复**（业务层，适配器无关；`service.go` Create）：写入前先 `Get(id)` 探测，
  id 已存在（含已归档——id 永久不回收）则重新生成；非 ErrNotFound 错误直接返回；
  Put 若收到 ErrConflict（未来适配器可能实现的多进程竞争）仍重试；5 次失败报错。
  新增 `Service.WithIDGenerator` 测试缝（同 WithClock 模式）。
- 测试：`supplier/id_collision_test.go` 2 例（确定性序列：首个 id 撞已存在供应商
  → 重新生成且旧档字段完好；始终撞号 → 耗尽报错且不覆盖）。全量默认+personal 绿。
- **教训记入踩坑 #5**：upsert 接口 + 随机 ID 时，唯一性必须由"知道 create vs update
  语义的层"（服务层）保证，不能指望适配器报错。

### 2026-09-09：搜索/筛选延迟红线复测——5000 家库 p99 远低于 200ms/100ms

PRD 性能红线"搜索响应 <200ms；条件查询 P99 <100ms"此前没有测试护栏。新增
**opt-in** 延迟测试 `sqlite/query_latency_test.go`（`SUPPLIDER_PERF=1` 运行，
默认跳过——造 5000 家库约 25s，不拖慢常规 `go test`）：经 service.Create 真实
入库（带 FTS 索引）5000 家、城市/品类分散，再各跑 200 次取 p50/p99：
- 中文 FTS 关键词搜索（"混凝土"，约命中 1/4 库）p99 ≈ **42–47ms**（红线 200ms，
  ~4x 余量）；
- 地域+品类结构化筛选（杭州+市政工程）p99 ≈ **13ms**（红线 100ms，~7x 余量）。

证实 FTS5 trigram/拼音索引与 suppliers 反范式索引列在中型库上无需优化；该测试
作为防回归护栏（全表扫描/漏索引会直接打穿阈值）。常规导入红线仍由
`TestImport1000RichRowsUnder3s` 守护。

### 2026-09-08：导入性能红线复测——1000 行富数据 <3s

既有 `TestImport1000RowsUnder3s` 只覆盖基础列。新增
`TestImport1000RichRowsUnder3s`：每行带联系人/电话/网址/品类/**主营产品**/
资质 + **五列绩效评分**（触发评分聚合 + FTS/评分索引写入），是导入器接受的
最重形态，针对真实 SQLite 适配器。实测：基础 1000 行 **1.73s**（~580 行/s）、
富数据 1000 行 **1.85s**（~540 行/s），均满足 PRD"1000 条 Excel 导入 <3s"
红线，新增列未引入明显回归（每行多写评分/产品仅 ~0.1s 增量）。

### 2026-09-08：单二进制 Web 发行链路验证（build-frontend → embed → suppliderd）

无 Rust 工具链下能验证的最后一段打包集成：跑 `scripts/build-frontend.sh`（
`VITE_API_BASE=http://127.0.0.1:7612` 构建并同步到 `backend/internal/webui/dist`）
→ `go build -tags personal` 内嵌 → 启动单二进制：
- `/` 返回真实生产 `index.html`（引用带 hash 的 /assets/*.js、css）；
- 打包后的 JS/CSS 资源 200、content-type 正确（text/javascript；charset=utf-8）；
- 同一二进制同时提供 `/api/v1/features` 等 API——UI 与 API 共存于 ~24MB 单文件。
至此除 `tauri build` 桌面安装包本身（需 cargo/rustc）外，整条构建-嵌入-服务链路均
实测通过；前端在 Tauri 内的连接由运行时 `resolveBase()` 保证（见下）。

### 2026-09-08：三构建标签运行时验证 + go vet 全量复检

个人版功能闭环后做跨版本质量复检：`go vet`（default/personal）零告警；全量
`go test -tags personal` 绿；`personal / enterprise / small_business` 三个 tag 均
**编译并运行**通过。首次在运行时（不只是编译）验证 `small_business`：功能矩阵
正确报告 `visibility_levels:5 / rbac / audit_log`、`storage:mongodb` 等目标
标签；建供应商（L1 可见性）→ FTS 命中 → 持久化可见性策略默认 cap L4 → 备份 zip
全部正常——**业务代码三版本同构**得到确认。

**已知现状（非缺陷）**：`small_business` 的 feature flag 报告 MongoDB/Meilisearch/
MinIO/Qdrant，但当前接线（`storefactory/tier_small_business.go` 等）仍用 SQLite +
localfs 适配器跑——MongoDB/Meilisearch/MinIO 适配器是小企业版后续的**适配层替换**
工作（业务代码不改，符合"接口先行，实现可换"）。这与优先级 #2（Meilisearch 适配器）
对应。

### 2026-09-08：补齐项目 README（三种使用方式 / CLI / 数据备份 / 架构红线）

仓库 README 原本只有一行标题。个人版 MVP 功能已闭环，补上面向用户与贡献者的
入口文档：桌面打包（build-frontend/build-sidecar + tauri build，需 Rust）、
从源码运行（`go run -tags personal ./cmd/suppliderd` + `vite dev`，并注明无
tag 默认是内存沙箱不落盘）、CLI/MCP（链 SETUP.md / skill 文档）、CLI 命令速览、
三系统数据目录与备份恢复、目录结构与三条架构红线（接口先行/AI 可降级/个人版
零依赖）、测试命令（默认/personal/enterprise）。

### 2026-09-08：修复 Tauri 打包后前端连不上 sidecar（API base 运行时检测）

静态审计 Tauri 打包配置时发现一个会导致**打包后双击应用"后端未连接"**的真实
缺陷：`tauri.conf.json` 的 `beforeBuildCommand` 是 `npm --prefix ../frontend run build`
（不带环境变量），而前端 `BASE = import.meta.env.VITE_API_BASE ?? ''`——`tauri build`
出来的包 BASE 为空，所有请求用相对路径，在 Tauri 里解析到 `tauri://localhost`（
Windows 为 `http://tauri.localhost`）而非 Go sidecar（127.0.0.1:7612），全部失败。
（仅 `scripts/build-frontend.sh` 设置了该变量，但 Tauri 打包走的是 conf 里的命令，
不经过它；在 beforeBuildCommand 里写 `VAR=val npm build` 又不兼容 Windows cmd。）

- **修复**（`frontend/src/api.ts` `resolveBase()`，运行时检测，跨平台）：
  1. 显式 `VITE_API_BASE` 优先（embedded-web 构建）；
  2. 在 **Tauri 生产 origin**（`__TAURI_INTERNALS__` 存在 且 protocol `tauri:` 或
     hostname `tauri.localhost`）→ 直连 `http://127.0.0.1:7612`；
  3. 其余（浏览器访问内嵌 webui、`vite dev`、`tauri dev` 的 localhost:5173）→
     相对路径（dev 走 vite 代理、webui 同源）。
- 关键细节：`tauri dev` 页面在 `localhost:5173`（vite，相对路径经代理正确），不能
  因为有 Tauri 全局变量就切绝对地址——故必须按 **origin 判定**而非仅查 Tauri 全局。
- 下载/附件 `<a href>` 同样经 `BASE`（apiUrl），在 Tauri 里一并修好；CSP
  connect-src 已含 127.0.0.1:7612，CORS 已放行 tauri 两个 origin。
- 验证：不带 `VITE_API_BASE` 构建（= tauri build 路径）产物中确认检测代码已打包；
  tsc+vite 通过。
- **静态审计结论（Tauri 打包，#1）**：其余配置一致——externalBin
  `binaries/suppliderd` 与 `build-sidecar.sh` 产物命名（`suppliderd-<triple>[.exe]`）
  匹配；六个图标齐全；capabilities 含 shell spawn/execute/kill 并限定 sidecar；
  main.rs 以 `--addr 127.0.0.1:7612 --data-dir <app_data_dir>` 拉起、轮询 /readyz、
  退出 kill；reqwest 用 rustls 免系统 OpenSSL。真正的 `tauri build` 体积/双击验证
  仍需 Rust 工具链（本机无 cargo/rustc）。

### 2026-09-08：Excel 导入补齐官网/主营产品列——批量导入与手工表单字段对齐

手工表单已有网址/官网与产品线（产品/服务），Excel 导入缺这两列：迁移贸易商
台账（主营产品是核心数据）或带官网的档案时会丢失。补齐（复用 fieldSpecs 机制，
与品类同法做逗号分隔列表）。

- 新增可映射列：**网址**（官网/网站/公司网址 → `BasicInfo.Website`）、
  **主营产品**（产品线/主营/供应产品… → `ProductsServices`，逗号/顿号/分号
  分隔，逐项建一条产品线）。模板表头由 fields 自动生成，示例行补
  `土建施工,道路工程` + `www.example.com`，填写说明补"主营产品多个用逗号分隔"。
- 测试：`importer/website_products_test.go` 1 例——别名自动映射（官网→
  website、主营产品→products）、`水泥,黄沙、商品混凝土` 拆为 3 条产品线、
  服务端持久化后 website 与 3 条 ProductsServices 正确。全量 `go test`
  默认+personal 全绿，enterprise/personal 编译通过。
- E2E（personal/SQLite，HTTP 真实导入模板）：模板含"网址/主营产品"列；导入
  示例行后供应商 website=`www.example.com`、产品线=`['土建施工','道路工程']`。

### 2026-09-08：Excel 导入支持绩效评分列（综合/交付/质量/配合度/评价）

三维绩效评分（交付/质量/配合度）与综合评分此前只能在**手工表单**填写，Excel
批量导入没有对应列——迁移历史供应商台账（评分数据）时无法带入评分，导入的
供应商评分全为 0。本环在导入适配器补齐（业务逻辑零改动，复用既有
`domain.Performance` 与服务端评分聚合）。

- **新增列**（importer fieldSpecs + 中文别名自动映射）：综合评分（评分/总分/
  合作评分）、交付评分（交付分/交付）、质量评分、配合度评分（配合分/协作评分）、
  合作评价（评价/项目评价）。模板表头由 fields 自动生成，5 列自动出现；示例行
  填"综合评分 4.5"；填写说明加一行评分取值 0–5、可只填综合或只填三维。
- **映射逻辑**：每行若出现任一评分/评价列，生成**一条摘要合作记录**
  `domain.Performance`（综合分优先，否则服务端按三维均值聚合评分——与手工/
  CLI/合并同源）。数字解析容忍"4.5分"等尾缀；**非数字或越界（0–5 外）分数
  静默忽略**，不致整行失败（服务层仍校验）。
- 测试：`importer/perf_import_test.go` 1 例——综合分行（score=5+评价）、
  三维-only 行（交付4/质量5/配合3 → 导入后评分 4.0）、越界分行（9分忽略仍
  成功创建）；别名自动映射断言。全量 `go test` 默认+personal 全绿，
  enterprise/personal 编译通过。
- E2E（personal/SQLite，HTTP 真实导入）：下载模板（表头含五个新列）→ preview
  自动建议映射 score/delivery/quality/cooperation/feedback 全部正确 → commit
  导入示例行 → 供应商"杭州示例建设有限公司"评分 **4.5** 落库。

### 2026-09-08：附件删除——附件管理从"只进不出"补齐

附件此前只能上传（合同/资质扫描件/营业执照），没有删除入口：传错文件或替换
扫描件后旧文件永久残留、占用空间且无法清理。本环补齐删除（文档式档案管理
必备）。**MCP 不暴露删除**（与归档/合并/黑名单同边界——破坏性动作由人在 UI 执行）。

- **服务层**（`Service.RemoveAttachment(ctx, id, url)`）：按 URL 定位附件记录，
  从 `doc.Attachments` 移除并写 `attachments` change_log（Old=文件名）；空 URL
  报错、记录不存在报"attachment not found"、归档供应商拒绝编辑。**只改文档**——
  对象字节由接线层（HTTP）删除，与上传路径（HTTP 负责 Put/Remove 字节、服务
  负责文档）对称。返回被删记录。
- **HTTP**：`DELETE /api/v1/suppliers/{id}/attachments?url=<附件URL或key>`——
  先 `RemoveAttachment` 删记录（记录不存在直接 404，绝不动别的文件），成功后
  `Objects.Remove(key)` 删字节（best-effort，对象已不存在不报错）。key 必须以
  `<id>/` 前缀开头才删字节，防止越权删其他供应商文件。
- **错误码修正**：`writeServiceError` 加"not found"子串 → 404（此前领域层
  "记录未找到"会落到 500）；缺 url 参数 400。
- **前端**：详情页附件列表每项加"删除"按钮（归档供应商不显示），confirm 提示
  "文件将从档案与磁盘移除，不可恢复"，删除后刷新；`api.deleteAttachment`。
- 测试：`supplier/attachment_test.go` 2 例——双附件删其一（剩正确记录 + 返回
  被删件 + change_log Old 条目）、重复删/空 URL 报错、归档供应商拒绝。全量
  `go test` 默认+personal 全绿，enterprise/personal 编译通过；前端 tsc+vite 通过。
- E2E（personal/SQLite + 真实对象存储）：上传 license.txt → 磁盘有文件 →
  DELETE ?url= → HTTP 200、文档附件数 0、下载 404、**磁盘文件已删除**；重复删
  404、缺参 400、他人 key（sup_OTHER/…）记录不存在 404 且字节不触碰。

### 2026-09-08：前端供应商对比/比价——"使用"阶段选型对比进入 UI

PRD 使用环节含"项目比价/合作记录"，`srm-cli compare` 与 MCP `compare_suppliers`
早已可用，但**桌面 UI 没有对比入口**——用户搜索出一批候选后无法在界面里并排
比较。本环补齐（纯前端；数据全部来自既有 GET 接口，后端零变更）。

- **列表勾选**（`SupplierList`）：每张供应商卡片加复选框（卡片由 `<button>`
  改为 `<div onClick>`，checkbox stopPropagation 不触发跳转）；选中后底部出现
  浮动条"已选 N 家 · ⚖ 开始对比（<2 家禁用）· 清空"，选择跨"加载更多"分页保留。
- **对比页**（新 `CompareView`，路由 `{name:'compare', ids}`）：并发拉取各家
  **完整档案**（`GET /suppliers/{id}`，维度均分/联系人/价格区间需要全档），
  横向表格一行一维度：地域、品类、联系人、资质（等级+类型）、综合评分（最高分
  琥珀底高亮）、交付/质量/配合度均分（前端按绩效记录重算，与 CLI fmtAvg 同口径）、
  合作项目数、价格区间（产品线 unit_price_range）、风险/状态（🚫黑名单/⚠空壳/
  ✓已核验徽标）；公司名可点进详情；表格可横向滚动。
- 类型坑：全档 `rating` 为 omitempty 可选字段（0 分不下发），对比页统一
  `d.rating ?? 0`；`performance_history` 可选 → dimAvg 用独立 `Performance`
  类型而非下标索引。
- E2E（personal/SQLite）：建甲（施工/一级资质/两条绩效含三维+总评/价格区间/
  联系人）与乙（贸易/仅价格）→ 全档接口数据齐（甲评分 4.75、维度值、价格；
  乙 rating 字段省略=前端按 0 显示"—"），对比页字段全部有数据源；前端
  tsc+vite 通过。

### 2026-09-08：录入表单补齐联系方式/类型/地址/官网——手工录入与 Excel 导入字段对齐

空壳检测 R202 会标记"缺联系人/电话"，详情页也展示联系人/邮箱/网址/地址，Excel
导入模板支持这些列——但**手工录入表单没有这些输入框**：用户在界面上根本无法
填写联系人/电话（一个供应商联系不上是最基础的可用性缺陷），也无法通过 UI 补全
资料来消除 R202。纯前端修复（字段在 domain/HTTP/导入/详情/MCP 早已全通，
`basic_info` 整体提交、编辑态 `...s.basic_info` 整体回填，无需后端改动）。

- `SupplierForm` 基本信息区加**供应商类型**（`<input list>` + datalist：
  施工商/建筑施工/贸易商/服务商/设备租赁商/生产商；保留自由输入——风险引擎按
  名称 Contains "施工/建筑" 判定，datalist 的"施工商"可正确触发 R203）；
  新增**联系方式**区块：联系人、联系电话、联系邮箱、网址/官网、详细地址。
- E2E（personal/SQLite）：资料齐全供应商（法人+资本+成立+经营范围+联系人/电话/
  邮箱+类型+地址+官网）→ 字段全部正确回读，且 **R202 不再出现**（只剩我测试用
  假信用代码触发的 R103 和"施工商无资质"的 R203，均为预期）；资料不全的对照
  组仍报 R202"缺法定代表人、联系人/电话、注册资本、经营范围"。
- 前端 tsc+vite 通过。

### 2026-09-08：数据备份导出（数据库一致性快照 + 全部附件 zip）——备份/迁移基线

PRD 管理员平台要求"数据备份与迁移"。个人版所有家当就是 `<DataDir>/supplider.db`
+ `<DataDir>/attachments/`，库丢了一切归零，此前没有任何备份入口。本环补齐**导出
备份**（恢复为停应用解压覆盖，MVP 够用且零风险）。纯本地、无外部依赖。

- **SQLite 快照**（`sqlite.SnapshotTo(ctx, path)`）：`VACUUM INTO '<path>'`——
  在 **运行中**的库上产出独立一致快照（含全部已提交事务，无需 -wal/-shm 附带，
  目标路径必须不存在）。比裸拷 DB 文件安全（WAL 下裸拷可能不一致）。
- **归档包**（`internal/backup`）：`WriteArchive(ctx, w, snapshotFn, attachmentsRoot,
  extra)` 流式写 zip——`supplider.db`（临时快照）+ `attachments/<供应商id>/<文件>`
  （WalkDir 保持层级）+ 末尾 `manifest.json`（format/version/created_at/
  attachment_count/extra，未来恢复路径可识别和迁移旧备份）。附件目录不存在也
  正常出包（仅库）。
- **HTTP**：`GET /api/v1/backup` 流式返回 zip（文件名 `supplider备份_YYYYMMDD.zip`）；
  `Server.WithBackup(fn)` 接线，无文件系统（memory 开发构建）时 501。接线在
  suppliderd main 用**能力断言**：store 实现 `SnapshotTo`（*sqlite.Store）且
  objectstore 实现 `Root()`（*localfs.Store）才启用——业务代码不碰具体类型。
- **CLI**：`srm-cli backup [--out file.zip]`（`-`=stdout），下载并提示恢复步骤。
- **前端**：设置页新增"数据备份与迁移"卡片——下载按钮（直链 GET，浏览器/
  Tauri webview 直接下载）+ 恢复说明（关闭应用、解压覆盖 `com.supplider.desktop`
  数据目录，附三系统路径）。
- 测试：`sqlite/backup_test.go` 1 例（写入供应商+settings → 快照 → 独立打开
  快照内容齐全；**快照后源库再写不入快照**=时点副本）；`backup/backup_test.go`
  2 例（zip 含 DB 内容 + 嵌套附件保持层级 + manifest 字段/extra；无附件目录
  仍出包含 db/manifest 的有效 zip）。全量 `go test` 默认+personal 全绿，
  enterprise/personal 编译通过；前端 tsc+vite 通过。
- E2E（personal/SQLite）：建供应商+上传附件 → `srm-cli backup` 得 zip（69KB
  快照 + attachments/<id>/<唯一前缀>_license.jpg + manifest，count=1）→
  解压到**全新空目录** → 新 sidecar 以该目录启动 → 全文搜索命中供应商 ✓、
  按档案记录的附件 URL 下载 **字节一致** ✓；memory 开发构建 `/api/v1/backup`
  回 501 ✓。备份恢复后附件 key 路径（首段=供应商 id）无需任何重定向。
- **后挂**：备份加密（PRD 提"备份加密"，与 SQLCipher 字段加密同批做小企业/
  企业版）；应用内"一键恢复"（需停服换文件，桌面端可走 Tauri 重启流程，
  当前文档化的手动恢复足够 MVP）。

### 2026-09-08：三维绩效评价（交付/质量/配合度）端到端——"使用"阶段评分闭环

PRD 生命周期"使用"环节要求**绩效评价（交付/质量/配合度）**。数据模型早有
`Performance.Delivery/Quality/Cooperation` 三个字段，但**全链路不可达**：表单
不能填、详情不显示、评分聚合只算 `Score` 总评、CLI 不渲染（`srm-cli compare`
的三维均分早就在用，却没有任何入口能写入这些字段）。本环把三维评分打通。纯
本地，无 AI。

- **聚合规则**（`domain.Performance.EffectiveScore()`）：一条合作记录的有效分
  = 显式总评 `Score`（优先）；未填总评则取**已填三维的均值**；三者都没填则该条
  不计入评分。`recomputeRating` 与 CLI info 共用此方法——旧数据（只有总评）与
  新数据（只填三维）在同一均值里都正确，兼容历史库。
- **校验**（`supplier.validatePerformance`，Create + Update 都拦）：总评/交付/
  质量/配合度任一超出 0–5 或为负值即拒绝（400，中文报错指名字段），与可见性
  0–4 校验同款。
- **前端**：
  - `SupplierForm` 绩效记录改为卡片式两行——第一行项目名+总评(0–5)+删除，
    第二行**交付/质量/配合度**三个小数字框+评价反馈；提示"总评可直接填，也可
    只填三维，系统按均值计入"。
  - `SupplierDetail` 绩效卡片在每条记录下渲染已填维度（交付/质量/配合度分数），
    没填的维度不显示。
- **CLI**：`info` 每条绩效在 ★ 后追加 `（交付 X / 质量 Y / 配合度 Z）`（只显示
  已填维度）；总评列对"仅三维"记录显示有效分（三维均值）。`compare` 三维均分
  原本就有，现在终于有数据可算。
- 测试：`supplier/performance_test.go` 3 例——①三维-only 记录（4/5/3→4.0）与
  总评记录（5.0）混合 → 综合 4.5；`EffectiveScore` 单测（无分数=0 不计入、
  单维=该值）；②越界校验：create delivery=5.5 拒、update score=6 拒、负值拒。
  全量 `go test` 默认+personal 全绿，enterprise/personal 编译通过；前端
  tsc+vite 通过。
- E2E（personal/SQLite）：建供应商带两条记录（三维-only 交付4/质量5/配合3 →
  有效 4.0；总评 5.0）→ 综合评分 **4.5** ✓；PATCH 加第三条（仅质量 4.0）→
  **4.33** ✓；delivery=9 创建 → **400** `performance delivery(交付) must be 0–5`；
  `srm-cli info` 三维行显示 `（交付 4.0 / 质量 5.0 / 配合度 3.0）`、仅质量行
  显示 `（质量 4.0）`、纯总评行不显示维度。

### 2026-09-08：srm-mcp / srm-cli 打包分发 + MCP 主机接入指南——开放接入闭环

MCP 服务端早已可用（`go build` 出独立 stdio 二进制），但用户**拿不到也配不上**：
没有构建脚本产出发布物、没有主机配置文档。本环补齐分发与接入：

- **`scripts/build-tools.sh`**（与 build-sidecar 同款纯 Go 交叉编译）：
  `CGO_ENABLED=0 -tags personal -ldflags="-s -w"` 一次产出 **srm-mcp + srm-cli
  × 5 个平台 triple**（linux amd64/arm64、windows amd64、macos amd64/arm64）到
  `dist/tools/`，并生成 `sha256sums.txt`（发布校验）。产物大小：srm-mcp
  7.7–8.2MB、srm-cli 5.6–6.0MB（剥离符号后）。`dist/` 加入 .gitignore。
- **`docs/mcp/SETUP.md`**（面向用户）：发布物命名与下载对照表、sha256 校验、
  macOS Gatekeeper 隔离属性解除、各系统**默认数据目录路径**（`%APPDATA%` /
  `~/Library/Application Support` / `~/.local/share` 下 `com.supplider.desktop`）、
  `--data-dir`/`SRM_DATA_DIR` 独立库用法；Claude Desktop（Win/macOS 配置文件
  路径 + 日志排障位置）与 Cursor（`~/.cursor/mcp.json`）的 mcpServers 配置；
  命令行 stdio 自测片段；srm-cli 常用命令速览（含 expiring 可挂 cron）。
  skill 文档（面向 Agent）顶部加 SETUP.md 指引链接。
- 验证：脚本实跑 5 平台全绿（纯 Go modernc 交叉编译无需 C 工具链）；linux
  发布产物 stdio 冒烟——initialize/tools/list（10 工具注册）/add_supplier（
  含空壳规则自动检测）/search_suppliers 命中/resources 读取/**二次启动数据
  仍在**；srm-cli usage 正常；sha256sum -c 全部 OK。
- **边界重申**：MCP 只暴露只读/录入类工具；归档/合并/黑名单/可见性处置不向
  Agent 开放（SETUP 中写明）。
- **后挂**：GitHub Releases CI 工作流（三个 build 脚本产物上传）需 CI 环境
  验证，列入优先级 #6 尾巴。

### 2026-09-08：合并重复档案选择器——从查重结果直接挑选，不再手输 id

人工合并的服务/API/CLI 已可用，但前端入口是 `prompt('输入 sup_ 开头的 id')`——
真实用户拿不到、也不该接触内部 id。本环把入口换成**查重候选选择器**（纯前端改动，
复用既有 `/suppliers/duplicates` 与 `/merge`，后端零变更）。

- **详情页「🔀 合并重复」**（`SupplierDetail.tsx`）：点击后展开选择面板，自动用当前
  档案的公司名+信用代码+地域跑录入查重，列出候选：公司名 + 确凿（信用代码一致）/
  疑似（同名）徽标 + 🚫黑名单/已归档状态 + id/地域/命中原因。
  - **在库候选**：「并入此档案」按钮，confirm 文案用公司名（不再是裸 id），合并后
    关闭面板、刷新档案、alert 并入计数。
  - **黑名单/归档候选**：按钮替换为"不可合并"提示（黑名单=造假档案不能并入，先移出；
    归档=先恢复），与服务端 400 拒绝形成双重防线。
  - **自身过滤**：查重会命中当前档案自己（同名/同代码），按 supplier_id 过滤。
  - **手动 id 兜底**：面板底部保留输入框——名称与代码都不同的重复（理论上查重无法
    发现）仍可手输 id 合并。
- 无候选/查重中/加载失败三态齐备；面板可取消关闭。
- E2E（personal/SQLite + 真实 HTTP）：4 家档案（同代码异名强匹配 B、同名黑名单 C、
  同名归档 D）→ 查重返回 self（强/在库）+B（强/在库）+C（疑似/blacklisted）+
  D（疑似/archived），正是选择器渲染/禁用所需的全部状态；A←B 合并成功计数正确；
  A←C、A←D 均 400（与面板禁用一致）。前端 tsc+vite 通过。
- **后挂**：合并后面板候选列表不实时刷新（面板已关闭、详情刷新，无影响）；MCP 仍
  刻意不暴露 merge。

### 2026-09-08：可见性策略持久化 + sidecar 自动处置 ticker——策略收紧闭环收尾

此前处置状态机完整（扫描/标记/缓冲/降级/申诉），但策略只在每次调用时以参数传入、
处置只在人工 `--enforce` 时发生——桌面版"7 天超时自动降级"永远不会自己跑。本环：
策略落库（随供应商数据库备份/迁移）、所有入口统一读持久化策略、sidecar 开机+每 24h
自动处置。纯本地，无 AI。

- **存储接口扩展**：`datamodel.SupplierStore` 加 `GetSetting/PutSetting(key,value)`
  ——通用 settings 缝（策略是第一个用户，后续管理员配置同路径），随库存放、同走
  备份/迁移。memory 适配器加 settings map；sqlite 适配器加 `settings` 表
  （WITHOUT ROWID、upsert ON CONFLICT，`CREATE TABLE IF NOT EXISTS` 即旧库迁移，
  无需 bump schemaVersion）。**contract 测试套件加 3 例**（round-trip/overwrite/
  缺失 ErrNotFound），两适配器跑同一套。
- **服务层**（`internal/supplier/policy.go`）：`SaveVisibilityPolicy`（normalized
  后 JSON 落 settings）、`LoadVisibilityPolicy(ctx, fallback)`——返回有效策略+
  `configured` 位；未配置/损坏值回落**接线层传入的 fallback**（业务代码仍不读版本
  配置；损坏不楔死处置）。
- **HTTP**：`GET /api/v1/visibility/policy`（生效策略 + configured + 版本上限）、
  `PUT|POST /api/v1/visibility/policy`（保存并**立即处置一次**——收紧策略当下即完成
  扫描→标记）；守卫：max_level 必填、0-4、不得超过版本上限（个人版 0-1，超出 400）。
  violations/enforce/resolve 三处默认策略改为 `resolvePolicy`：请求显式覆盖 > 持久化
  策略 > 版本默认。
- **定时处置**（`httpapi.StartMaintenanceLoops`，sidecar main 接线）：开机 5s 后跑
  一次处置 sweep（桌面 App 不是每天都开——开机兜底捕获超时降级），之后每 24h 一次；
  仅在 flagged/downgraded/resolved>0 时打日志，错误/panic 只记日志不崩 sidecar。
- **CLI**：`srm-cli visibility policy`（无参数=查看，标注"管理员已配置/版本默认"；
  `--max-level N [--buffer-days N]`=保存并立即处置，回传 flagged/downgraded 计数）。
- **MCP**：`visibility_violations` 无显式 max_level 时改读持久化策略（srm-mcp 直连
  同一 SQLite，多进程 WAL 共享）；Skill 文档同步默认值语义。
- **前端**：新 `SettingsView`（头部 ⚙ 设置 入口，App 路由加 `settings` 视图）——
  可见等级下拉（按版本上限生成 L0..LtierMax + 中文等级名）、缓冲天数、保存即处置、
  configured 徽标、处置结果条（新标记/降级/解除计数）。
- 测试：`supplier/policy_test.go` 3 例——fallback 规范化、保存后**另一个 Service
  实例**（=重启/多进程）读到持久策略、持久策略驱动 enforce 收紧（L0 cap 标记 L1）；
  contract 套件 +3 例（两个适配器）。全量 `go test` 默认+personal 全绿，
  enterprise/personal 编译通过；前端 tsc+vite 通过。
- E2E（personal/SQLite + sidecar + CLI + MCP stdio）：默认策略 configured=false
  L1/7 天；PUT max_level=2（超个人版上限）400、缺 max_level 400、9 越界 400；
  CLI 保存 L0 → 立即处置；策略保存后新建 L1 供应商（未标记的 violation）→ **重启
  sidecar，开机 5s 后日志 `visibility sweep (cap L0): flagged=1 ... remaining=2`，
  记录自动进入待调整（截止 2026-09-15），全程零人工调用**；MCP 独立进程读同一库，
  按持久化 L0 策略报 2 条 pending；CLI 扫描不带 `--max-level` 也按 L0 出报告。
- **坑**：E2E 用 `pkill -f srm-e2e/suppliderd` 会连带杀掉执行命令的 wrapper shell
  （其命令行也含该串）且匹配不到 setsid 启动的 `./suppliderd`（cmdline 不含该路径
  串）——表现为"重启了但 boot sweep 没跑"（实际是旧进程没死）。改用 `pkill -x
  suppliderd`（精确进程名）后正常。

### 2026-09-08：人工合并重复供应商——查重体系的"存量清理"闭环

录入去重（CheckDuplicates）防的是**新增**重复；本环补上**存量**重复的清理：
dedup 上线前积累的、或跨人重复录入的同一主体两条档案，人工确认后合并为一条。
纯本地、无 AI。合并是**主档案定向（master-directed）**的保守操作。

- **服务层**（`internal/supplier/merge.go`）：`MergeSuppliers(ctx, masterID, dupID)`
  ——**主档案保留身份**（basic_info/owner/可见性/状态/风险结论不动），吸收重复方的
  增量集合：品类（字符串集合）、资质（证书号相同判重；无证书号时按类型+等级）、产品线
  （按名称）、绩效记录（按项目+日期判重，追加后 `recomputeRating` 重算聚合评分）、
  附件（**按 URL 引用合并**——文件字节不移动，下载路径按 object key 首段解析属主、
  归档文档仍可 Get，故重复方归档后附件链接依然有效）、自定义字段（**主档案值优先**，
  仅主档案没有的键并入）、shared_with 可见名单（id 并集）。
  - 双方各写一条 change_log：主档案 `merged_from`（含各项并入计数）、重复方
    `merged_into`（指向主档案 id+名称）——审计可追溯。
  - 重复方走**已测试的归档路径**（Put 指针后 `store.Delete`，适配器 archive 语义：
    行保留、status=archived、默认列表/搜索隐藏、Get 仍可读），不硬删除。
  - **守卫**：空 id / 自身合并拒绝；任一方已归档拒绝（提示先 restore，报错带双方
    status）；**任一方在黑名单拒绝**——确认造假的公司绝不能被"洗"进干净档案，
    须先移出黑名单或保持两条独立。
- **API**：`POST /api/v1/suppliers/{id}/merge`，body `{duplicate_id}`（或
  `?duplicate_id=`），路径 id 即主档案；返回 `{supplier, merged:{各项计数}}`。
  校验错误 400、不存在 404（复用 writeServiceError）。
- **CLI**：`srm-cli merge <保留id> <并入并归档id> [--json]`，CJK 输出并入计数 +
  归档提示 + 合并后评分。
- **前端**：详情页头部 `🔀 合并重复` 按钮（仅在库非黑名单态显示）——prompt 输入
  重复方 id、自身合并前置拦截、confirm 二次确认（明示"不可撤销、对方归档"）、
  结果 alert 展示并入计数后刷新档案。
- **MCP 刻意不暴露**：合并会归档数据、不可逆，属需用户明确指示的破坏性操作
  （比黑名单更甚——黑名单可逆）。Agent 仍可用 `find_duplicates` 发现并报告重复，
  由用户在 UI/CLI 执行合并。
- 测试：`supplier/merge_test.go` 2 例——①全量合并：计数正确、主档案身份不变、
  各集合并去重（资质/绩效/品类/产品线/附件/自定义字段冲突主方胜）、评分重算
  (4.0+5.0)/2=4.5、双方 change_log、重复方归档且默认列表只剩主档案；②守卫：
  自身/空 id/黑名单（任一向）/归档拒绝。全量 `go test` 默认+personal 全绿，
  enterprise/personal 编译通过；前端 tsc+vite 通过。
- E2E（personal/SQLite + sidecar + CLI）：建主档案（1 条绩效 4.0）与重复档案
  （额外品类/资质/产品线/绩效 5.0/2 个自定义字段）→ `srm-cli merge` → 主档案
  品类 2、资质 1、产品线 1、绩效 2（重复项目剔除）、评分 4.5、merged_from 在档；
  重复方 status=archived + merged_into；默认列表 1 条、FTS 搜索只命中主档案，
  而 `duplicates` 查重仍能扫到归档重复方（带"已归档"状态）；守卫四况：黑名单
  （两个方向）400 且主档案未改动、归档 400（报错带双方 status）、自身 400、
  不存在 404。
- **待办（后挂）**：前端合并时从查重结果列表直接挑选重复档案（当前手输 id）；
  附件字节级归并（当前引用合并已满足下载，属可选优化）。

### 2026-09-08：Excel 批量导入逐行查重——把录入去重接到批量入口

录入去重原语 `CheckDuplicates` 此前只接了手动表单/CLI/MCP；批量 Excel 导入是最大的
"一次灌进几百条重复供应商"入口，本环把同一套保守规则（信用代码=确凿 / 规范名=疑似，含
黑名单/归档）接到 `Service.Import`。**默认只警告仍导入，勾选则命中行跳过**（flag-don't-
block 哲学一致）。

- **查重索引重构**（`internal/supplier/dedup.go`）：抽出 `dedupEntry{code,name,doc}`
  （规范化后的匹配键只算一次）+ `matchEntry` + `loadDedupIndex`（整库 keyset 遍历一次，
  含归档/黑名单）。手动录入的 `CheckDuplicates` 行为不变（改为复用同一排序/匹配），批量
  导入则**整库只扫一遍**，每行 O(索引) 匹配——避免 N 行各做一次全表扫描，守住
  "1000 条 Excel <3s"红线。
- **批内查重**：本批次前面已成功创建的行会追加进索引，所以**同一个文件里两行相同公司**
  第二行也会命中（warn 模式记一次警告，skip 模式跳过）。
- **服务层**（`service.go`）：`Import(ctx, items, opts ImportOptions{SkipDuplicates})`；
  `ImportReport` 加 `Skipped int` 与 `Duplicates []ImportDuplicate{Row,Name,Matches}`。
  命中黑名单既有供应商时 `Matches[].status=blacklisted`（确凿在前、同级黑名单靠前）。
  索引加载失败不致命（降级为不去重，Create 逐行仍会报存储错误）。
- **API**：`POST /api/v1/import/commit` 增表单字段 `skip_duplicates=true|1|yes|on`；
  默认 false（仍导入、报告 duplicates）。
- **前端**（ImportView）：提交区加"跳过重复供应商"勾选框（默认勾选，批量场景更安全）；
  结果栏加"跳过重复 N 条"琥珀计数 + 逐行警告表（行号/本行公司名/命中既有供应商[🚫黑名单/
  已归档徽标]/确凿(信用代码)·疑似(同名)）。
- 测试：`supplier/import_dedup_test.go` 5 例——①默认 warn：重复行仍创建但进 duplicates；
  ②skip 模式：确凿(代码,名称可不同)命中跳过、新公司创建；③同文件批内重复（warn 记警告 /
  skip 跳过第二行）；④命中黑名单既有供应商带 blacklisted 状态；⑤三家互不相同全导入零警告。
  既有 importer 两个测试调用点补 `ImportOptions{}`（签名变更，最小改动）。全量 `go test`
  默认+personal 全绿，enterprise/personal 编译通过；前端 tsc 通过。
- E2E（personal/SQLite + 真实 .xlsx）：seed 1 家 → 批量 4 行（同名/同代码异名/新公司/
  新公司再重复）`skip=true` → **created=1 skipped=3**，逐行 strong/probable/批内 strong
  正确；`skip=false` 再导 → created=4、4 条警告；把含该信用代码的记录拉黑后重导 →
  skipped=1、top match status=blacklisted strong。
- **待办（后挂）**：人工"合并重复供应商"流程（合并字段/附件/合作记录后归档被合并方）。

### 2026-09-08：可见性策略收紧数据处置流程（五级权限体系收尾）——策略收紧骨架

补齐 PRD 核心功能 #2 的最后一环：**策略收紧时的数据处置流程**（扫描不合规 → 通知录入者
→ 7 天缓冲标记"待调整" → 超时自动降级 → 支持申诉）。可见性字段/校验早已在库，本环把
"管理员调低最高可见等级后，存量超范围记录怎么办"做成完整状态机。个人版只跑 0/1 两级
（cap=1），但状态机 tier 无关，企业版同一份代码跑全五级。纯本地，无 AI。

- **数据模型**（`domain`）：
  - `Supplier.VisEnforcement *VisibilityEnforcement`（处置子文档，**不是状态变更**——
    缓冲期内供应商仍可见可用）：`pending_adjustment / flagged_at / deadline /
    previous_visibility / reason` + 申诉字段 `appealed / appeal_note / appealed_at`。
    合规后整体移除；每次跃迁写 change_log。
  - `Supplier.VisException bool`：申诉成立的管理员例外（可保留超 cap 等级）；**下次编辑
    可见性即清除**（例外只针对被批准的那个等级，不跟随后续修改）。
  - `Summary.VisPending` 列表徽标位；新增 change_log 来源 `SourceSystem`（系统自动执行）。
- **服务层**（`internal/supplier/visibility.go`）：
  - `VisibilityPolicy{MaxLevel, BufferDays}` 由接入层传入（admin/flag → HTTP/CLI），业务
    代码不直接读版本配置；`normalized()` 钳制 0-4、缓冲默认 7 天。
  - `ScanVisibilityViolations(ctx, policy)`：只读扫描（keyset 分页，同 Export 走法），
    返回每条不合规记录的处置状态 `violation|pending|appealed|overdue` + 剩余天数，按紧迫
    度排序。这是"扫描/通知录入者"的负载（企业版通知服务推同一形状）。
  - `EnforceVisibilityPolicy(ctx, policy)`：一次处置 sweep——①未标记的新违规→标记待调整、
    deadline=now+缓冲；②缓冲期内录入者已自行降到合规→清标记(resolved，不降级)；③到期且
    未申诉→自动降级到 cap（最近合规等级）；④已申诉→倒计时暂停不动；⑤申诉成立例外→跳过。
    幂等：重复跑只重计状态。报告含 flagged/pending/appealed/downgraded/resolved 计数 +
    剩余待处理列表。
  - `AppealVisibility(id, note)`：录入者申诉，暂停倒计时（幂等）。
  - `ResolveVisibilityAppeal(id, grant, policy)`：管理员裁决——grant=成立保留等级并打例外
    标记；deny=驳回立即降级到 cap。
  - Update 路径：编辑 visibility 时自动清 `VisException`。
- **API**：`GET /api/v1/visibility/violations`（只读扫描）、`POST /api/v1/visibility/enforce`
  （处置 sweep，body/query 可带 `max_level`/`buffer_days`，默认取版本 cap）、
  `POST /suppliers/{id}/appeal-visibility`（body `{note}` 或 `?note=`）、
  `POST /suppliers/{id}/resolve-visibility-appeal`（`{grant}` 或 `?grant=true|false`）。
- **CLI**：`srm-cli visibility [--enforce] [--max-level N] [--buffer-days N] [--json]`
  （默认只读扫描，CJK 等宽表格：状态/剩余时间/截止日/L旧→L新/供应商/地域/录入人）；
  `srm-cli appeal <id> [--note ...]`；`srm-cli resolve-appeal <id> --grant|--deny`。
- **MCP**：新增只读工具 `visibility_violations`（Agent 只报告、提示录入者，**不自动处置/
  降级/裁决**——这些是管理员动作，经 CLI/后台）；Skill 文档同步该工具与边界。
- **前端**：列表卡片琥珀色 `⏳ 待调整` 徽标；详情页琥珀横幅（当前可见范围超上限 + 截止日
  + 申诉理由 + `我要申诉`/`去调整可见范围` 按钮，申诉后显示"倒计时暂停待裁决"）；申诉成立
  显示绿色例外横幅。
- 测试：`supplier/visibility_test.go` 4 例（用可冻结时钟）——①扫描→标记(7天缓冲,可见性
  不变,change_log/徽标)→同日幂等→第 8 天超时自动降级到 cap；②申诉暂停倒计时(30 天后仍不
  降级)→grant 成立打例外(后续 sweep 跳过)→编辑可见性后例外失效重新入流程；③deny 立即降级
  + 无申诉/无待调整时调用报错；④缓冲期内自行降级→下次 sweep resolved 清标记。全量
  `go test` 默认+personal 全绿，enterprise/personal 编译通过；前端 tsc 通过。
- E2E（personal/SQLite，sidecar + CLI）：建 L4 违规 + L0 合规 → 只读扫描命中 1 条"新发现"
  → enforce 标记"待调整"截止 2026-09-15 → appeal 后扫描变"⏸ 申诉中 倒计时暂停"→
  resolve --grant 保留 L4（例外）扫描清空；另建 L4/L3 → appeal 后 --deny 立即降到 L1；
  L3 自行 PATCH 到 L0 后 enforce 显示"自行调整已解除 1"；最终扫描 0 条。MCP stdio
  `visibility_violations` 注册并返回。
- **待办（后挂）**：管理员可见性策略配置 UI、sidecar 日级定时 enforce、企业版通知推送
  （负载形状已留）。

### 2026-09-08：录入去重（非 AI 重复检测）——录入阶段防重复建库

直接服务于产品第一痛点"供应商资源分散、重复录入"。录入前按纯本地规则查重，**只提示
不阻断**（与风险引擎同一"flag, don't block"哲学）；模糊/AI 相似度是后挂增强层。

- **匹配规则**（`internal/supplier/dedup.go`，保守可解释）：
  - **确凿 strong**：18 位统一社会信用代码一致（同一主体的确凿标识；先 trim+大写归一，
    仅长度为 18 的成形代码参与比较）。
  - **疑似 probable**：规范化后公司名一致——去掉空白与标点（`（）()【】·.,，、-—_/`等，
    Latin 小写），使"杭州一建（集团）有限公司"与"杭州一建集团有限公司"、空格变体判同。
  - **全库扫描含黑名单/归档**：IncludeArchived 遍历；命中结果带 `status`。最高价值场景是
    **重新录入一个已在黑名单的造假供应商**——排序确凿在前、同级别黑名单靠前。
  - 排序 matchRank：确凿<疑似；同级黑名单优先，再按名称。候选既无成形代码又无名称→空。
- **服务层**：`CheckDuplicates(ctx, domain.BasicInfo) ([]DuplicateMatch, error)`，纯只读、
  不改数据；Export 同款 keyset 分页遍历，上限 MaxExportDocs。
- **API**：`GET /api/v1/suppliers/duplicates?name=&credit_code=&province=&city=`
  → `{count, matches[]}`（match 含 supplier_id/name/地域/status/level/reason）。
  字面路径 `/duplicates` 在 ServeMux 中优先于 `/{id}` 通配，无路由冲突。
- **CLI**：`srm-cli duplicates --name X [--credit-code C --province --city] [--json]`
  （别名 `dedup`）；CJK 等宽表格（确凿/疑似 × 在库/🚫黑名单/已归档），命中黑名单追加
  "请勿重复录入/合作"警告。
- **MCP**：新增 `find_duplicates` 工具；`add_supplier` 在创建前自动查重，命中则在结果
  文本前置警告（仍创建）；Skill 文档新增该工具并要求"add 前先查重、命中黑名单明确警告"。
- **前端**：新建表单（非编辑态）对 公司名/信用代码/地域 做 400ms 防抖实时查重，命中展示
  可展开警告条（确凿红/疑似琥珀；命中黑名单整条转红 + 🚫黑名单徽标），点击直达已有档案；
  编辑态不触发（避免供应商匹配自身）。
- 测试：`supplier/dedup_test.go` 4 例——信用代码强匹配(大小写/空格归一)、名称标点/空格
  变体疑似匹配且不同名称不误报、**黑名单+归档仍被命中且带状态**、空候选无匹配。全量
  `go test` 默认+personal 全绿，enterprise/personal 编译通过；前端 tsc+vite 通过。
- E2E：HTTP 按代码(确凿,即使名称不同)/按名(疑似)/不同名(0) 三况正确；黑名单后查重 status
  正确回传；CLI 表格 + 黑名单提示；MCP `find_duplicates` 与 `add_supplier` 查重警告（创建
  一个同名新供应商时结果前置"⚠ 查重发现 1 家…黑名单"）。
- **待办（后挂）**：Excel 批量导入的逐行查重报告（ImportReport 加 duplicates 维度）与
  人工"合并重复供应商"流程，可基于同一 CheckDuplicates 原语做。

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
5. **upsert 接口下随机 ID 的唯一性不能靠适配器**：`SupplierStore.Put` 契约是 upsert
   （Update 需要），适配器永远不返回 ErrConflict；6 位随机后缀撞号时 Put 会静默覆盖
   旧供应商。唯一性探测（Get→regenerate）必须放在知道 create/update 之别的服务层。
   排查信号：memory 适配器热循环造数偶发少一行（TestExportWalksAllPages 204/205）。
6. **本地优先是"排序提示"不是"过滤条件"**：放 `SupplierFilter.PreferProvince` 但绝不
   进 WHERE——进了 WHERE 就变成只看本地供应商，与产品语义（外地仍可见，只是靠后）
   相反；keyset 必须带上 prio 成为三元组，否则跨页边界会重行/漏行。

## 架构红线自查

- 业务代码（supplier service / httpapi / exporter / importer）只依赖 `datamodel`、
  `search`、`objectstore` 等接口；具体驱动（modernc sqlite、go-pinyin）仅出现在
  adapter 包（sqlite/、localfs/）与 storefactory/objectfactory 接线文件中。
- `go build -tags personal` 与 `-tags enterprise` 均编译通过（每轮验证）。
