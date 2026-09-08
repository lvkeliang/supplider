---
name: supplider
description: 访问本地 Supplider 供应商资源管理平台（供应商档案、资质、绩效、空壳风险、资质到期提醒）。用于搜索供应商、做合作前尽调/背调、比价选型、录入供应商。
---

# Supplider 供应商平台 Skill（MCP）

Supplider 是面向项目型团队（起步于建筑施工行业）的本地供应商资源管理平台。
通过 MCP，你可以直接读写用户**本地**的供应商库：全文搜索、查看文档式档案、
资质到期与空壳风险扫描、录入供应商、并排比价。

所有数据都在用户本机（SQLite 单文件），**无云端、无外部 AI、无网络请求**。
风险扫描是纯本地规则（如统一社会信用代码 GB 32100 校验位、成立时长、资料
完整度、资质登记），其结果**仅供人工参考**，不代表法律或财务结论。

## MCP 配置

MCP 主机（Claude Desktop / Cursor 等）配置里添加（个人版二进制名 `srm-mcp`）：

```json
{
  "mcpServers": {
    "supplider": { "command": "srm-mcp", "args": [] }
  }
}
```

可用 `--data-dir <目录>` 或环境变量 `SRM_DATA_DIR` 指定数据库目录；缺省时
自动使用桌面 App 的数据目录（Tauri identifier `com.supplider.desktop`）。
桌面 App 关闭时 MCP 仍可独立读写（SQLite WAL，多进程安全）。

## 工具（Tools）

| 工具 | 作用 | 关键参数 |
| --- | --- | --- |
| `search_suppliers` | 中文全文/拼音/同义词搜索 + 多维筛选；`q` 留空即列出首页 | `q, province, city, category, min_qual_level, min_rating, include_archived, limit` |
| `get_supplier` | 读取完整文档式档案（基本信息/资质/品类/产品/绩效/风险/自定义字段/附件/变更记录） | `id` |
| `add_supplier` | 录入供应商（必填 `company_name, province, city`）；录入前自动查重，命中会在结果中给出重复警告（不阻断） | 见工具 schema |
| `find_duplicates` | 录入前查重：按信用代码(确凿)/公司名(疑似)找可能重复的供应商（含黑名单/归档）；建议 add 前先调用 | `name?`, `credit_code?`, `province?`, `city?` |
| `shell_risk_queue` | 待人工审核的空壳风险供应商队列（含逐条信号） | — |
| `supplier_risk` | 某供应商的空壳检测逐条信号（规则代码 + 中文解释） | `id` |
| `expiring_qualifications` | 资质到期提醒（已过期 / 90/30/7 天窗口），按紧迫度排序 | `within`（默认 90） |
| `compare_suppliers` | 多家供应商并排对比（地域/品类/最高资质/评分/绩效/风险） | `ids[]`（≥2） |
| `blacklist_supplier` | 淘汰阶段：列入黑名单（`remove:false` + `reason`，仍可搜到但醒目标记禁用）或移出（`remove:true`） | `id`, `reason?`, `remove?` |

供应商 id 形如 `sup_2026_XXXXXX`，由 `search_suppliers` / `add_supplier` 返回。

## 资源（Resources）

- `supplider://suppliers` — 供应商摘要首页（含空壳风险/审核标记）。
- `supplider://supplier/{id}` — 某供应商完整档案（JSON）。
- `supplider://risk/shell` — 空壳风险待审核队列。
- `supplider://reminders/expiring` — 资质到期提醒清单。

## 提示（Prompts）

- `supplier-due-diligence`（参数 `query`：供应商 id 或公司名关键词）——合作前
  尽调流程：定位供应商 → 拉空壳风险信号 → 查资质到期 → 给出「可合作 / 需补料
  复核 / 不建议」建议及依据。需要你（Agent）实际调用上述工具完成。

## 推荐工作流

1. **找供应商**：先用 `search_suppliers`（可带 `q` 关键词、`city`/`category`/
   `min_qual_level`/`min_rating` 筛选）；列表只返回摘要，拿到 id。
2. **看详情**：`get_supplier` 读取资质、绩效、风险、自定义字段。
3. **合作前尽调**：调用 `supplier_risk`（或直接用 `supplier-due-diligence`
   prompt），并结合 `expiring_qualifications` 确认资质未过期/临期。
   - 高严重度信号（如信用代码校验位不符 R103）必须提示用户人工核验原件。
   - 空壳风险**不自动否决**——它只是审核线索；最终判断在用户。经用户确认属实的，
     可由用户决定后调用 `blacklist_supplier`（你不应擅自拉黑，需用户明确指示）。
4. **比价选型**：对 2 家以上候选调用 `compare_suppliers`，结合评分/资质/风险给
   出推荐排序，并说明理由。
5. **录入新供应商前先查重**：调用 `find_duplicates`（或 add 自带的查重警告）；
   命中黑名单/已有档案时提示用户核对，不要重复录入；**命中黑名单要明确警告不要合作**。
   确认无重复后再 `add_supplier`（信息不全也可先建，资料简陋会触发提示性信号，
   不阻断），事后让用户补全信用代码/资质等。

## 输出约定

- 工具结果为中文 JSON 文本。面向用户汇报时用中文，风险/到期项要醒目。
- 引用风险信号时带上规则代码（如 `[R103]`）与中文解释，便于用户核对。
- 不要编造平台数据之外的事实；外部工商/司法数据在个人版中不存在。
