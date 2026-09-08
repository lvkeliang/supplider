# srm-mcp 安装与配置（MCP 主机接入指南）

`srm-mcp` 是 Supplider 的 **MCP stdio 服务**：让 Claude Desktop / Cursor 等 AI Agent
直接查询与录入你的本地供应商库（搜索、查重、录入、风险队列、资质到期、比价、
可见性合规扫描）。

- **零外部依赖**：单二进制，纯本地运行，不需要启动桌面 App、不需要 Docker、
  不需要任何 API Key（所有已暴露工具都是非 AI 基线功能）。
- **与桌面版共享同一个数据库**：默认直接读写桌面 App 的数据目录（见下），
  SQLite WAL 模式下 App 开着也能并发读写；App 关着 MCP 照常工作。
- Agent 边界：只读/录入类工具对 Agent 开放；**归档、合并、黑名单、可见性处置
  等破坏性/管理动作不暴露给 Agent**（由人在 CLI/桌面端执行）。

工具清单与 Agent 工作流见同目录 [`supplider-skill.md`](./supplider-skill.md)。

---

## 1. 获取二进制

### 方式 A：下载发布产物（推荐）

从 GitHub Releases 下载与你系统对应的资源（由 `scripts/build-tools.sh` 产出）：

| 平台 | 文件名 |
|---|---|
| Windows x64 | `srm-mcp-x86_64-pc-windows-msvc.exe` |
| macOS Intel | `srm-mcp-x86_64-apple-darwin` |
| macOS Apple Silicon | `srm-mcp-aarch64-apple-darwin` |
| Linux x64 | `srm-mcp-x86_64-unknown-linux-gnu` |
| Linux ARM64 | `srm-mcp-aarch64-unknown-linux-gnu` |

发布包内附 `sha256sums.txt`，下载后可校验：
`sha256sum -c sha256sums.txt`（Windows 可用 `certutil -hashfile <file> SHA256`）。

建议放到固定目录，例如：
- Windows：`C:\Users\<你>\AppData\Local\Programs\supplider\srm-mcp.exe`
- macOS / Linux：`/usr/local/bin/srm-mcp`（或 `~/.local/bin/srm-mcp`）

macOS 首次运行若被 Gatekeeper 拦截：`xattr -d com.apple.quarantine /路径/srm-mcp`。
Linux/macOS 记得加执行权限：`chmod +x srm-mcp-*`。

### 方式 B：从源码构建

```bash
# 构建全部 5 个平台的 srm-mcp + srm-cli 到 dist/tools/
scripts/build-tools.sh

# 或只构建当前平台
cd backend && CGO_ENABLED=0 go build -tags personal -o srm-mcp ./cmd/srm-mcp
```

## 2. 数据目录（默认无需配置）

默认打开**桌面 App 的同一个库**（Tauri 标识符 `com.supplider.desktop`）：

- Windows：`%APPDATA%\com.supplider.desktop\`
- macOS：`~/Library/Application Support/com.supplider.desktop/`
- Linux：`$XDG_DATA_HOME/com.supplider.desktop/` 或 `~/.local/share/com.supplider.desktop/`

想让 MCP 用**独立的库**（例如给 Agent 一个沙箱环境），两种方式：

- 命令行参数：`srm-mcp --data-dir /path/to/data`
- 环境变量：`SRM_DATA_DIR=/path/to/data`

## 3. 配置 MCP 主机

把下面的 JSON 加入对应主机的配置（`<COMMAND>` 换成 srm-mcp 的**绝对路径**）：

```json
{
  "mcpServers": {
    "supplider": {
      "command": "<COMMAND>",
      "args": []
    }
  }
}
```

使用独立数据目录时：`"args": ["--data-dir", "/path/to/data"]`。

### Claude Desktop

- Windows：`%APPDATA%\Claude\claude_desktop_config.json`
- macOS：`~/Library/Application Support/Claude/claude_desktop_config.json`

编辑后**重启 Claude Desktop**；在对话窗口右下角工具图标里应能看到
`supplider` 及其 10 个工具。故障排查：Claude Desktop 的 MCP 日志在
`~/Library/Logs/Claude/mcp*.log`（macOS）/`%APPDATA%\Claude\Logs\`（Windows）。

### Cursor

`Settings → MCP → Add new MCP server`（或编辑 `~/.cursor/mcp.json`），
内容同上。添加后在 Settings → MCP 中确认 `supplider` 状态为绿色。

### 其他 MCP 主机

任何支持 stdio MCP 的主机（VS Code Copilot Chat、Continue、自研 Agent 等）
都使用同一配置：command 绝对路径、无网络端口、换行分隔 JSON-RPC。

## 4. 验证

配置重启后，对 Agent 说：

> 用 supplider 搜索一下库里有没有杭州的混凝土/商砼供应商。

或直接命令行自测（应返回工具列表）：

```bash
printf '%s\n%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | srm-mcp
```

## 5. srm-cli（命令行工具，可选）

发布产物中的 `srm-cli-*` 是终端客户端，适合脚本/cron 使用。它通过 HTTP 连接
**正在运行的桌面 App / sidecar**（默认 `http://127.0.0.1:7612`，可用
`SRM_API_ADDR` 覆盖）。常用：

```bash
srm-cli search 商砼 --city 杭州
srm-cli expiring            # 90/30/7 天资质到期提醒（可挂 cron）
srm-cli risk                # 空壳风险审核队列
srm-cli visibility          # 可见性不合规扫描
srm-cli visibility policy --max-level 0   # 收紧策略（保存并立即处置）
srm-cli merge <保留id> <并入id>           # 合并重复供应商
srm-cli export --format xlsx
```
