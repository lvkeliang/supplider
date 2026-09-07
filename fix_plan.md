# fix_plan.md — Supplider 开发记录与待办

Ralph 每轮循环在此记录：已完成项、踩过的坑、下一步最重要的事。
新条目追加到对应章节顶部（最新在前）。

## 下一步优先级（个人版 MVP）

1. **资质到期提醒（90/30/7 天）**：纯业务逻辑，无需新基础设施——supplier service 加
   `ExpiringQualifications(days []int)` 扫描 + CLI `srm-cli expiring` + 前端列表页提醒横幅；
   工商变更监控留 [AI]/API 占位，先有非 AI 的手动到期扫描。
2. **Tauri 安装包真机验证**：shell 代码与 sidecar 已完成并通过 HTTP 层 E2E（见下），
   但本机无 Rust 工具链，未跑过 `tauri build`；需在有 Rust 的机器执行
   `scripts/build-sidecar.sh && cd src-tauri && tauri build`，验证安装包体积（目标 ~15MB）
   与双击拉起 sidecar。图标已由 `backend/cmd/genicons` 离线生成（PNG/ICO/ICNS）。
3. **Meilisearch 内嵌适配器**：实现 `search.Index` 接口替换 SQLite FTS5（当前 FTS 是过渡方案，行为已被测试钉住，可直接对照）。
4. 可见性策略收紧流程（个人版只需 0/1 两级的数据处置骨架）。
5. MCP 暴露（srm-cli 已有 search/add/list/info/export/compare，包一层 MCP Tools/Resources）。

## 已完成

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
