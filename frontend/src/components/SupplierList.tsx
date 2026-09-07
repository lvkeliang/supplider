import { useCallback, useEffect, useState } from 'react'
import { api, type ListParams } from '../api'
import type { ExpiringReport, ShellRiskReport, SupplierSummary } from '../types'
import { QUAL_LEVELS, STATUS_ARCHIVED } from '../types'
import type { Go } from '../App'

/** Chinese tag + tailwind classes for one risk severity. */
function severityTag(sev: string): { label: string; cls: string } {
  switch (sev) {
    case 'high':
      return { label: '高', cls: 'bg-red-200 text-red-900' }
    case 'medium':
      return { label: '中', cls: 'bg-orange-200 text-orange-900' }
    default:
      return { label: '低', cls: 'bg-slate-200 text-slate-700' }
  }
}

/** Human tag for one expiry bucket. */
function expiryTag(bucket: string): string {
  switch (bucket) {
    case 'expired':
      return '已过期'
    case '7d':
      return '7 天内到期'
    case '30d':
      return '30 天内到期'
    default:
      return '90 天内到期'
  }
}

/**
 * List page: keyword full-text box + structured filters (地域/品类/资质/评分)
 * and keyset "load more" pagination. Lists render SUMMARIES only (the
 * backend never returns full documents for list calls — performance red
 * line of ≤100 rows/page, no OFFSET).
 */
export function SupplierList({ go }: { go: Go }) {
  const [params, setParams] = useState<ListParams>({ limit: 20 })
  const [items, setItems] = useState<SupplierSummary[]>([])
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [reminders, setReminders] = useState<ExpiringReport | null>(null)
  const [showReminders, setShowReminders] = useState(false)
  const [shellQueue, setShellQueue] = useState<ShellRiskReport | null>(null)
  const [showShell, setShowShell] = useState(false)

  // Maintenance banners (non-AI scans). Loaded once on mount; empty/absent
  // reports render nothing. Best-effort: a failure never blocks the list.
  useEffect(() => {
    api
      .expiringReminders()
      .then((rep) => {
        if (rep.count > 0) setReminders(rep)
      })
      .catch(() => {})
    api
      .shellRiskQueue()
      .then((rep) => {
        if (rep.count > 0) setShellQueue(rep)
      })
      .catch(() => {})
  }, [])

  const load = useCallback(
    async (p: ListParams, reset: boolean) => {
      setLoading(true)
      setError('')
      try {
        const page = await api.listSuppliers(p)
        setItems((prev) => (reset ? page.items : [...prev, ...page.items]))
        setCursor(page.next_cursor)
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e))
      } finally {
        setLoading(false)
      }
    },
    [],
  )

  // Reset and reload whenever a filter changes.
  useEffect(() => {
    load({ ...params, cursor: undefined }, true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    params.q,
    params.province,
    params.city,
    params.category,
    params.min_qual_level,
    params.min_rating,
    params.include_archived,
  ])

  const set = (patch: Partial<ListParams>) => setParams((p) => ({ ...p, ...patch }))

  // Export the CURRENT filter view: the server streams an attachment
  // (Content-Disposition), so a hidden anchor click downloads without
  // leaving the app. JSON = full-fidelity backup bundle; XLSX = exchange
  // workbook round-trippable through the Excel importer.
  const downloadExport = (format: 'json' | 'xlsx') => {
    const a = document.createElement('a')
    a.href = api.exportUrl(params, format)
    document.body.appendChild(a)
    a.click()
    a.remove()
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-slate-800">供应商</h1>
        <div className="flex gap-2">
          <button className="btn-ghost" onClick={() => go({ name: 'import' })}>
            ⬆ Excel 导入
          </button>
          <button className="btn-ghost" onClick={() => downloadExport('xlsx')} title="按当前筛选导出 Excel（可再导入）">
            ⬇ 导出 Excel
          </button>
          <button className="btn-ghost" onClick={() => downloadExport('json')} title="按当前筛选导出完整 JSON 备份（含变更记录/附件信息）">
            ⬇ JSON 备份
          </button>
          <button className="btn-primary" onClick={() => go({ name: 'new' })}>
            ＋ 新建供应商
          </button>
        </div>
      </div>

      {/* Search + filters */}
      <div className="rounded-lg border border-slate-200 bg-white p-3 shadow-sm">
        <input
          className="input mb-2"
          placeholder="搜索公司名 / 信用代码 / 法人 / 经营范围 / 品类关键词…"
          defaultValue={params.q}
          onKeyDown={(e) => {
            if (e.key === 'Enter') set({ q: (e.target as HTMLInputElement).value.trim() || undefined })
          }}
        />
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
          <input className="input" placeholder="省（如 浙江）" defaultValue={params.province}
            onKeyDown={(e) => e.key === 'Enter' && set({ province: (e.target as HTMLInputElement).value.trim() || undefined })} />
          <input className="input" placeholder="市（如 杭州）" defaultValue={params.city}
            onKeyDown={(e) => e.key === 'Enter' && set({ city: (e.target as HTMLInputElement).value.trim() || undefined })} />
          <input className="input" placeholder="品类（如 施工服务）" defaultValue={params.category}
            onKeyDown={(e) => e.key === 'Enter' && set({ category: (e.target as HTMLInputElement).value.trim() || undefined })} />
          <select
            className="input"
            defaultValue={params.min_qual_level ?? ''}
            onChange={(e) => set({ min_qual_level: e.target.value || undefined })}
          >
            <option value="">资质等级（不限）</option>
            {QUAL_LEVELS.map((l) => (
              <option key={l} value={l}>{l}及以上</option>
            ))}
          </select>
        </div>
        <div className="mt-2 flex items-center gap-4 text-sm text-slate-500">
          <label className="flex items-center gap-1">
            最低评分
            <select
              className="input !w-20 !py-1"
              defaultValue={params.min_rating ?? ''}
              onChange={(e) => set({ min_rating: e.target.value ? Number(e.target.value) : undefined })}
            >
              <option value="">不限</option>
              {[3, 3.5, 4, 4.5].map((r) => (
                <option key={r} value={r}>{r}+</option>
              ))}
            </select>
          </label>
          <label className="flex items-center gap-1">
            <input
              type="checkbox"
              checked={!!params.include_archived}
              onChange={(e) => set({ include_archived: e.target.checked || undefined })}
            />
            显示已归档
          </label>
        </div>
      </div>

      {reminders && (
        <div
          className={`rounded-lg border px-3 py-2 text-sm ${
            reminders.expired > 0
              ? 'border-red-200 bg-red-50 text-red-800'
              : 'border-amber-200 bg-amber-50 text-amber-800'
          }`}
        >
          <button
            className="flex w-full items-center gap-2 text-left font-medium"
            onClick={() => setShowReminders((v) => !v)}
          >
            <span>{reminders.expired > 0 ? '🚫' : '⏰'}</span>
            <span>
              {reminders.expired > 0
                ? `${reminders.expired} 项资质已过期`
                : `${reminders.count} 项资质即将到期`}
              （90 天内，含 7/30 天提醒窗口）
            </span>
            <span className="ml-auto text-xs opacity-70">{showReminders ? '收起 ▲' : '展开 ▼'}</span>
          </button>
          {showReminders && (
            <ul className="mt-2 divide-y divide-current/10 border-t border-current/10">
              {reminders.items.map((a) => (
                <li key={`${a.supplier_id}-${a.qual_type}-${a.expiry}`} className="py-1.5">
                  <button
                    className="flex w-full items-center gap-2 text-left"
                    onClick={() => go({ name: 'detail', id: a.supplier_id })}
                  >
                    <span
                      className={`rounded-full px-2 py-0.5 text-xs ${
                        a.bucket === 'expired'
                          ? 'bg-red-200 text-red-900'
                          : a.bucket === '7d'
                            ? 'bg-orange-200 text-orange-900'
                            : 'bg-amber-200 text-amber-900'
                      }`}
                    >
                      {expiryTag(a.bucket)}
                    </span>
                    <span className="font-medium">{a.supplier_name}</span>
                    <span className="text-xs opacity-80">
                      {a.qual_type}
                      {a.qual_level ? ` · ${a.qual_level}` : ''} · 到期 {a.expiry}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      {shellQueue && (
        <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-800">
          <button
            className="flex w-full items-center gap-2 text-left font-medium"
            onClick={() => setShowShell((v) => !v)}
          >
            <span>⚠️</span>
            <span>
              {shellQueue.count} 家供应商存在空壳风险，建议人工审核
              （本地规则自动检测，无需 AI）
            </span>
            <span className="ml-auto text-xs opacity-70">{showShell ? '收起 ▲' : '展开 ▼'}</span>
          </button>
          {showShell && (
            <ul className="mt-2 divide-y divide-current/10 border-t border-current/10">
              {shellQueue.items.map((it) => {
                const top =
                  it.signals.find((s) => s.severity === 'high') ??
                  it.signals.find((s) => s.severity === 'medium') ??
                  it.signals[0]
                const tag = top ? severityTag(top.severity) : null
                return (
                  <li key={it.supplier_id} className="py-1.5">
                    <button
                      className="flex w-full items-center gap-2 text-left"
                      onClick={() => go({ name: 'detail', id: it.supplier_id })}
                    >
                      {tag && (
                        <span className={`rounded-full px-2 py-0.5 text-xs ${tag.cls}`}>
                          {tag.label}风险
                        </span>
                      )}
                      <span className="font-medium">{it.supplier_name}</span>
                      <span className="truncate text-xs opacity-80">{top?.message}</span>
                    </button>
                  </li>
                )
              })}
            </ul>
          )}
        </div>
      )}

      {error && <div className="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{error}</div>}

      {/* Results */}
      <div className="grid gap-3">
        {items.map((s) => (
          <button
            key={s.id}
            onClick={() => go({ name: 'detail', id: s.id })}
            className="rounded-lg border border-slate-200 bg-white p-4 text-left shadow-sm transition hover:border-brand-500 hover:shadow"
          >
            <div className="flex items-center gap-2">
              <span className="font-medium text-slate-800">{s.name}</span>
              {s.shell_risk && (
                <span
                  className="rounded-full bg-red-100 px-2 py-0.5 text-xs text-red-700"
                  title="本地规则检测到空壳风险，点开详情查看逐条信号"
                >
                  ⚠ 空壳风险
                </span>
              )}
              {s.status === STATUS_ARCHIVED && (
                <span className="rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-500">已归档</span>
              )}
              <span className="ml-auto text-sm text-amber-500">
                {s.rating > 0 ? `★ ${s.rating.toFixed(1)}` : ''}
              </span>
            </div>
            <div className="mt-1 text-sm text-slate-500">
              {[s.province, s.city, s.district].filter(Boolean).join(' · ')}
              {s.top_qual ? ` · ${s.top_qual}资质` : ''}
            </div>
            {(s.categories?.length ?? 0) > 0 && (
              <div className="mt-2 flex flex-wrap gap-1">
                {s.categories!.map((c) => (
                  <span key={c} className="chip">{c}</span>
                ))}
              </div>
            )}
          </button>
        ))}
        {!loading && items.length === 0 && !error && (
          <div className="rounded-lg border border-dashed border-slate-300 bg-white p-10 text-center text-slate-400">
            没有匹配的供应商，试试减少筛选条件，或点击右上角“新建供应商”。
          </div>
        )}
      </div>

      <div className="text-center">
        {loading && <div className="py-3 text-sm text-slate-400">加载中…</div>}
        {!loading && cursor && (
          <button
            className="btn-ghost"
            onClick={() => load({ ...params, cursor }, false)}
          >
            加载更多
          </button>
        )}
      </div>
    </div>
  )
}
