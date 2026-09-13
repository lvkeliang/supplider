# Supplider · 供应商资源管理平台

Supplider 是面向**项目型团队**（起步于建筑施工行业）的供应商资源管理平台：
把分散在各人手里的供应商资料、合作历史与风险信息，沉淀成一个**本地、零依赖、
可离线使用**的库。

- 🔍 中文全文搜索（分词 + 拼音 + 同义词），地域/品类/资质/评分多维筛选
- 📄 文档式档案：固定核心字段 + 自由 `custom_fields`，附件、资质、产品线
- 🛡 空壳/假资质风险检测（纯本地规则：信用代码校验位、成立时长、资质登记…）
- ⏰ 资质到期提醒（90/30/7 天）、五级可见性策略 + 自动处置、黑名单/归档/合并去重
- 📊 绩效评价（交付/质量/配合度三维）、并排比价、Excel 批量导入/导出、一键备份
- 🤖 通过 **MCP** 把本地库安全地接入 Claude Desktop / Cursor 等 AI Agent

**个人版**是单个 Go 二进制（纯 Go SQLite，无 cgo）+ 一个 Tauri 桌面壳，数据全部
保存在本机；**无需 Docker、数据库或任何外部服务**。小企业版/企业版共享同一套
业务代码，仅替换存储/搜索/对象存储适配器（MongoDB/Meilisearch/S3…）。

---

## 三种使用方式

### 1. 桌面应用（最终用户）

用 [Tauri](https://tauri.app) 打包成安装包（Windows / macOS / Linux）。打包需要
Rust 工具链：

```bash
# 1. 构建前端并内嵌到 Go（web 模式）
scripts/build-frontend.sh
# 2. 交叉编译 Go sidecar（纯 Go，无需 C 工具链）
scripts/build-sidecar.sh
# 3. 打包桌面安装包（需要 cargo/rustc）
cd src-tauri && cargo install tauri-cli --version "^2" && tauri build
```

Tauri 启动时会自动拉起 Go sidecar（`suppliderd-<target>`），轮询就绪后加载界面；
退出时自动结束进程。双击即用，安装包约 15 MB。

**通过 CI 发布（推荐）**：推送 `v*` 标签即触发 `.github/workflows/release.yml`，
GitHub Actions 在 Windows / macOS（arm64 + Intel）/ Linux 四种 runner 上交叉编译
sidecar 并打包安装包（NSIS / dmg / deb / AppImage），同时上传五平台的
`srm-cli`、`srm-mcp` 与独立 `suppliderd`（含 SHA256SUMS）到该 tag 的
GitHub Release。当前产物未做代码签名，macOS 首次打开需解除隔离属性
（见 [docs/mcp/SETUP.md](docs/mcp/SETUP.md)）。每次推送到 master / PR 由
`.github/workflows/ci.yml` 跑三 tag 编译与全部测试。

### 2. 从源码运行（开发者）

```bash
# 后端：本地 HTTP API + 内嵌 Web UI（默认 127.0.0.1:7612）
cd backend
go run -tags personal ./cmd/suppliderd --data-dir ./data
# 浏览器打开 http://127.0.0.1:7612

# 前端开发服务器（热更新，自动把 /api 代理到 sidecar）
cd frontend && npm install && npm run dev
# 打开 http://localhost:5173
```

> 注意：E2E/生产构建请用 `-tags personal`（个人版走 SQLite + 本地附件）。
> 不带 tag 的默认构建是内存存储的开发沙箱（数据不落盘）。

### 3. 命令行 / MCP（自动化与 AI）

- `srm-cli`：通过 HTTP 操作正在运行的 sidecar（脚本、cron 友好）。
- `srm-mcp`：独立的 MCP stdio 服务，**直连同一个 SQLite 库**（桌面 App 开着或
  关闭都能用），把搜索/查重/录入/风险扫描等能力开放给 AI Agent。

```bash
# 交叉编译全部平台的 srm-cli 与 srm-mcp 到 dist/tools/（附 sha256）
scripts/build-tools.sh
```

MCP 主机（Claude Desktop / Cursor）的配置与各系统数据目录见
**[docs/mcp/SETUP.md](docs/mcp/SETUP.md)**，Agent 工作流见
**[docs/mcp/supplider-skill.md](docs/mcp/supplider-skill.md)**。

---

## CLI 速览

`srm-cli` 连接运行中的 sidecar（环境变量 `SRM_API_ADDR`，默认 `http://127.0.0.1:7612`）：

```bash
srm-cli search 商砼 --city 杭州        # 全文搜索 + 筛选
srm-cli list --min-qual 二级            # 条件列表
srm-cli info <id>                       # 完整档案（含绩效三维/附件）
srm-cli export --format xlsx            # 导出 Excel（json 为完整备份）
srm-cli backup --out library.zip        # 完整备份（库快照 + 附件）
srm-cli compare <id> <id>...            # 并排比价（评分/资质/价格/风险）
srm-cli expiring                        # 资质到期提醒（可挂 cron）
srm-cli risk                            # 空壳风险审核队列
srm-cli duplicates --name 某某公司      # 录入前查重
srm-cli ainl "杭州本地二级市政商砼"     # AI 自然语言搜索（需已配模型）
srm-cli analyze ./需求.docx             # AI 文档分析：提取需求→推荐供应商
srm-cli audit [--limit N]               # 操作审计日志（创建/归档/黑名单/合并/导出）
srm-cli merge <保留id> <并入id>         # 合并重复档案（后者归档）
srm-cli blacklist <id> --reason 造假     # 黑名单（淘汰）/ unblacklist
srm-cli visibility policy --max-level 0 # 收紧可见性策略并立即处置
srm-cli visibility --enforce            # 手动执行处置扫描
srm-cli add suppliers.json              # 从 JSON 录入
```

---

## 数据位置与备份

个人版所有数据在应用数据目录（Tauri 标识符 `com.supplider.desktop`）下：

| 系统 | 目录 |
|---|---|
| Windows | `%APPDATA%\com.supplider.desktop\` |
| macOS | `~/Library/Application Support/com.supplider.desktop/` |
| Linux | `~/.local/share/com.supplider.desktop/` |

- `supplider.db` — SQLite 单文件（JSONB 文档列 + 全文索引）
- `attachments/` — 附件原文件（`<供应商id>/<文件>`）

**备份**：设置页「数据备份与迁移」或 `srm-cli backup` 下载一个 zip（一致性
数据库快照 `VACUUM INTO` + 全部附件 + 清单），应用运行中也可安全导出。
**恢复**：关闭应用，把 zip 解压覆盖数据目录后重开。

---

## 架构

```
frontend/   React + TypeScript + Tailwind（Web 与 Tauri 共用一套代码）
src-tauri/  Tauri 2 桌面壳（零业务逻辑：拉起/监控/回收 Go sidecar）
backend/
  cmd/
    suppliderd/   HTTP API + 内嵌 Web UI 的守护进程（Tauri sidecar）
    srm-cli/      命令行客户端（HTTP）
    srm-mcp/      MCP stdio 服务（直连存储，独立二进制）
  internal/
    datamodel/    SupplierStore 接口（memory + sqlite JSONB 适配器，契约测试共用）
    search/       FTS5 中文分词/拼音/同义词（搜索引擎端口，可换 Meilisearch）
    supplier/     生命周期业务逻辑（tier 无关，只依赖接口）
    risk/         空壳特征规则引擎（非 AI）
    importer/     Excel 模板/预览/列映射/批量导入
    exporter/     JSON / XLSX 导出
    backup/       备份归档（DB 快照 + 附件 zip）
    objectstore/  附件对象存储端口（localfs / MinIO / S3）
    featureflag/  版本功能矩阵、tier/ 构建标签
    httpapi/      HTTP 路由（业务代码）
    mcp/          MCP Tools/Resources/Prompts
```

**三条红线：**

1. **接口先行，实现可换**：存储/搜索/对象存储/MQ/AI 都是接口，业务代码
   `grep` 不到具体驱动；三级部署共享同一份业务逻辑，差异只在适配层 + 构建标签。
2. **AI 原生但可降级**：无任何 API Key 时平台 100% 可用，AI 入口整段隐藏；
   每个 AI 功能点都有非 AI 基线（如空壳检测用本地规则、Excel 列名用中文别名匹配）。
3. **个人版零依赖**：纯 Go（modernc SQLite，无 cgo）单二进制 + 本地文件，
   不要求 Docker、数据库或外部服务。

### 测试

```bash
cd backend
go test ./...                 # 默认（memory 适配器）
go test -tags personal ./...  # 个人版（sqlite）
go build -tags enterprise ./...  # 企业版编译检查
```

存储适配器跑同一套契约测试（`internal/datamodel/contract`），保证 SQLite 与
未来的 MongoDB 适配器语义一致。

---

## 许可证

AGPL（个人版开源发布）。
