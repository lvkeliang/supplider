import { useCallback, useEffect, useRef, useState } from 'react'
import { api, type ListParams } from '../api'
import type { ExpiringReport, LocalPreference, ShellRiskReport, SupplierSummary } from '../types'
import { QUAL_LEVELS, STATUS_ARCHIVED, STATUS_BLACKLISTED } from '../types'
import type { Go } from '../App'
import { useToast } from './Toast'
import { Icon } from './Icon'
import { Debouncer, normalizeQuery } from '../debounce'
import { FOCUS_SEARCH_EVENT } from '../hotkeys'

/** Live-search debounce (TR-03): type → search 300ms after the last key. */
const SEARCH_DEBOUNCE_MS = 300

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
export function SupplierList({
  go,
  aiNLSearch = false,
  aiDocSearch = false,
}: {
  go: Go
  aiNLSearch?: boolean
  aiDocSearch?: boolean
}) {
  const toast = useToast()
  const [params, setParams] = useState<ListParams>({ limit: 20 })
  // 自然语言搜索模式 (TR-19-D)：开启后关键词框当作整句需求，提交时经
  // LLM 解析成结构化筛选并套用；手动关键词+筛选仍是默认/降级路径。
  const [aiMode, setAiMode] = useState(false)
  const [aiBusy, setAiBusy] = useState(false)
  // Controlled keyword box (TR-03): the text field updates instantly while
  // the list query commits 300ms after typing stops (Enter or the search
  // icon commits immediately; the clear icon clears and commits at once).
  const [qInput, setQInput] = useState(params.q ?? '')
  const debouncerRef = useRef<Debouncer | null>(null)
  if (!debouncerRef.current) debouncerRef.current = new Debouncer(SEARCH_DEBOUNCE_MS)
  const debouncer = debouncerRef.current
  useEffect(() => () => debouncer.cancel(), [debouncer])
  // Global shortcut "/" or Ctrl/⌘+K focuses this box (TR-18).
  const searchInputRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    const focus = () => searchInputRef.current?.focus()
    window.addEventListener(FOCUS_SEARCH_EVENT, focus)
    return () => window.removeEventListener(FOCUS_SEARCH_EVENT, focus)
  }, [])
  const [items, setItems] = useState<SupplierSummary[]>([])
  const [cursor, setCursor] = useState<string | undefined>(undefined)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [reminders, setReminders] = useState<ExpiringReport | null>(null)
  const [showReminders, setShowReminders] = useState(false)
  const [shellQueue, setShellQueue] = useState<ShellRiskReport | null>(null)
  const [showShell, setShowShell] = useState(false)
  // 比价/对比：勾选的供应商 id（跨分页保留，打开对比页时一并提交）。
  const [selected, setSelected] = useState<string[]>([])
  // 本地供应商偏好（设置页配置）：开启时本地供应商排最前（仅排序）。
  const [pref, setPref] = useState<LocalPreference | null>(null)
  const [localOn, setLocalOn] = useState(true)

  const isLocal = (s: SupplierSummary) =>
    !!pref?.configured &&
    s.province === pref.province &&
    (!pref.city || s.city === pref.city)

  const toggleSelected = (id: string, on: boolean) =>
    setSelected((prev) => (on ? (prev.includes(id) ? prev : [...prev, id]) : prev.filter((x) => x !== id)))

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
    api
      .getLocalPreference()
      .then((p) => {
        if (p.configured) setPref(p)
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
    params.watched,
    params.localFirst,
  ])

  const set = (patch: Partial<ListParams>) => setParams((p) => ({ ...p, ...patch }))

  // Commit the keyword box to the query. Functional equality guard means
  // Enter or the search icon on an unchanged (or already-empty) term never refetches.
  const applyQuery = useCallback((raw: string) => {
    const q = normalizeQuery(raw)
    setParams((p) => ((p.q ?? '') === (q ?? '') ? p : { ...p, q }))
  }, [])

  // AI 自然语言搜索 (TR-19-D)：把整句需求解析成结构化筛选并套用。
  const runNLSearch = async (raw: string) => {
    const q = raw.trim()
    if (!q) {
      applyQuery('')
      return
    }
    setAiBusy(true)
    try {
      const r = await api.aiNLSearch(q)
      setParams((p) => ({
        ...p,
        q: r.keyword || undefined,
        province: r.province || undefined,
        city: r.city || undefined,
        district: r.district || undefined,
        category: r.category || undefined,
        min_qual_level: r.min_qual_level || undefined,
        min_rating: r.min_rating > 0 ? r.min_rating : undefined,
      }))
      const parts = [
        r.province, r.city, r.district,
        r.category, r.min_qual_level,
        r.min_rating > 0 ? `${r.min_rating} 分以上` : '',
      ].filter(Boolean)
      toast.success(
        parts.length ? `已解析为筛选：${parts.join(' · ')}` : '已解析为关键词搜索',
      )
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setAiBusy(false)
    }
  }

  // Export the CURRENT filter view. Fetch as a Blob (instead of a
  // fire-and-forget anchor) so the transfer is tracked: preparing → done
  // with the real filename → failure, all via toast (TR-02). JSON =
  // full-fidelity backup bundle; XLSX = exchange workbook round-trippable
  // through the Excel importer.
  const downloadExport = async (format: 'json' | 'xlsx') => {
    const label = format === 'xlsx' ? 'Excel' : 'JSON'
    toast.info(`正在准备${label}导出…`)
    try {
      const name = await api.download(
        api.exportUrl(params, format),
        format === 'xlsx' ? 'suppliers.xlsx' : 'suppliers.json',
      )
      toast.success(`已下载「${name}」`)
    } catch (e) {
      toast.error(`${label}导出失败：${e instanceof Error ? e.message : String(e)}`)
    }
  }

  return (
    <div className="space-y-4">
      {/* Sticky band: title + search/filter stay under the global header
          while the long supplier list scrolls (TR-09). Page-bg fill hides
          rows passing underneath. */}
      <div className="sticky top-[57px] z-40 -mx-4 -mt-6 space-y-3 bg-slate-50 px-4 pb-3 pt-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-slate-800">供应商</h1>
        <div className="flex gap-2">
          {aiDocSearch && (
            <button className="btn-ghost" onClick={() => go({ name: 'docsearch' })} title="上传或粘贴需求文档，AI 语义匹配推荐供应商">
              <Icon name="scales" size={15} /> AI 文档搜索
            </button>
          )}
          <button className="btn-ghost" onClick={() => go({ name: 'import' })}>
            <Icon name="upload" size={15} /> Excel 导入
          </button>
          <button className="btn-ghost" onClick={() => downloadExport('xlsx')} title="按当前筛选导出 Excel（可再导入）">
            <Icon name="download" size={15} /> 导出 Excel
          </button>
          <button className="btn-ghost" onClick={() => downloadExport('json')} title="按当前筛选导出完整 JSON 备份（含变更记录/附件信息）">
            <Icon name="download" size={15} /> JSON 备份
          </button>
          <button className="btn-primary" onClick={() => go({ name: 'new' })}>
            <Icon name="plus" size={15} /> 新建供应商
          </button>
        </div>
      </div>

      {/* Search + filters */}
      <div className="rounded-lg border border-slate-200 bg-white p-3 shadow-sm">
        <div className="relative mb-2">
          <input
            ref={searchInputRef}
            className="input pr-24"
            placeholder={
              aiMode
                ? '用一句话描述需求，如：杭州本地能做市政工程的二级资质以上供应商'
                : '搜索公司名 / 信用代码 / 法人 / 品类…（输入即搜索，按 / 聚焦）'
            }
            title={aiMode ? '回车或点搜索解析成筛选' : '按 / 或 Ctrl+K 快速聚焦搜索'}
            value={qInput}
            onChange={(e) => {
              const v = e.target.value
              setQInput(v)
              if (!aiMode) debouncer.schedule(() => applyQuery(v))
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                if (aiMode) void runNLSearch(qInput)
                else debouncer.flush(() => applyQuery(qInput))
              }
            }}
          />
          {aiNLSearch && (
            <button
              type="button"
              aria-label={aiMode ? '切回普通搜索' : 'AI 自然语言搜索'}
              title={aiMode ? '切回普通关键词搜索' : '用一句话描述需求，AI 解析成筛选'}
              className={`absolute right-10 top-1/2 -translate-y-1/2 rounded px-1.5 text-xs font-semibold leading-5 ${
                aiMode ? 'bg-brand-600 text-white' : 'text-slate-400 hover:text-brand-600'
              }`}
              onClick={() => setAiMode((m) => !m)}
            >
              AI
            </button>
          )}
          {qInput !== '' && (
            <button
              type="button"
              aria-label="清除搜索"
              title="清除"
              className="absolute right-9 top-1/2 -translate-y-1/2 rounded px-1 leading-none text-slate-400 hover:text-slate-700"
              onClick={() => {
                setQInput('')
                if (aiMode) void runNLSearch('')
                else debouncer.flush(() => applyQuery(''))
              }}
            >
              <Icon name="x" size={14} />
            </button>
          )}
          <button
            type="button"
            aria-label="搜索"
            title="搜索"
            className="absolute right-2 top-1/2 -translate-y-1/2 rounded px-1 text-slate-500 hover:text-brand-600"
            onClick={() => {
              if (aiMode) void runNLSearch(qInput)
              else debouncer.flush(() => applyQuery(qInput))
            }}
          >
            {aiBusy ? <span className="text-xs">…</span> : <Icon name="search" size={16} />}
          </button>
        </div>
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
          <label className="flex items-center gap-1" title="只看我关注的供应商（风险/黑名单/资质临期会进铃铛通知）">
            <input
              type="checkbox"
              checked={!!params.watched}
              onChange={(e) => set({ watched: e.target.checked || undefined })}
            />
            <Icon name="star" size={13} filled className="text-amber-500" /> 仅看关注
          </label>
          {pref?.configured && (
            <label className="flex items-center gap-1" title={`本地供应商（${[pref.province, pref.city].filter(Boolean).join(' · ')}）排最前，仅排序不筛选`}>
              <input
                type="checkbox"
                checked={localOn}
                onChange={(e) => {
                  const on = e.target.checked
                  setLocalOn(on)
                  set({ localFirst: on ? undefined : false })
                }}
              />
              本地优先（{[pref.province, pref.city].filter(Boolean).join('·')}）
            </label>
          )}
        </div>
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
            <Icon
              name={reminders.expired > 0 ? 'ban' : 'clock'}
              size={15}
              className={reminders.expired > 0 ? 'text-red-600' : 'text-amber-500'}
            />
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
            <Icon name="alert" size={15} className="text-red-600" />
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
          <div
            key={s.id}
            onClick={() => go({ name: 'detail', id: s.id })}
            className="cursor-pointer rounded-lg border border-slate-200 bg-white p-4 text-left shadow-sm transition hover:border-brand-500 hover:shadow"
          >
            <div className="flex items-center gap-2">
              <input
                type="checkbox"
                className="h-4 w-4 shrink-0"
                checked={selected.includes(s.id)}
                title="加入对比"
                onClick={(e) => e.stopPropagation()}
                onChange={(e) => toggleSelected(s.id, e.target.checked)}
              />
              <span className="font-medium text-slate-800">{s.name}</span>
              {s.watched && (
                <span
                  className="inline-flex items-center gap-0.5 rounded-full bg-amber-50 px-2 py-0.5 text-xs text-amber-700"
                  title="我关注的供应商：风险/黑名单/归档/资质临期会进铃铛通知"
                >
                  <Icon name="star" size={11} filled /> 已关注
                </span>
              )}
              {localOn && isLocal(s) && (
                <span
                  className="inline-flex items-center gap-0.5 rounded-full bg-sky-100 px-2 py-0.5 text-xs text-sky-700"
                  title="本地供应商（符合本地偏好地域），已优先排序"
                >
                  <Icon name="pin" size={11} /> 本地
                </span>
              )}
              {s.status === STATUS_BLACKLISTED && (
                <span className="inline-flex items-center gap-0.5 rounded-full bg-red-600 px-2 py-0.5 text-xs font-medium text-white" title="黑名单（淘汰/禁用），请勿选用">
                  <Icon name="ban" size={11} /> 黑名单
                </span>
              )}
              {s.shell_risk && !s.risk_reviewed && (
                <span
                  className="inline-flex items-center gap-0.5 rounded-full bg-red-100 px-2 py-0.5 text-xs text-red-700"
                  title="本地规则检测到空壳风险，待人工审核（点开详情查看信号/标记已核验）"
                >
                  <Icon name="alert" size={11} /> 待审核
                </span>
              )}
              {s.shell_risk && s.risk_reviewed && (
                <span className="inline-flex items-center gap-0.5 rounded-full bg-emerald-100 px-2 py-0.5 text-xs text-emerald-700" title="曾检测到风险，已人工核验/处理">
                  <Icon name="check" size={11} strokeWidth={3} /> 已核验
                </span>
              )}
              {s.vis_pending && (
                <span
                  className="inline-flex items-center gap-0.5 rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-800"
                  title="可见性策略收紧：可见范围超出最高允许等级，缓冲期内请调整或申诉，超时将自动降级"
                >
                  <Icon name="clock" size={11} /> 待调整
                </span>
              )}
              {s.status === STATUS_ARCHIVED && (
                <span className="rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-500">已归档</span>
              )}
              {s.rating > 0 && (
                <span className="ml-auto inline-flex items-center gap-0.5 text-sm text-amber-500">
                  <Icon name="star" size={13} filled /> {s.rating.toFixed(1)}
                </span>
              )}
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
          </div>
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

      {/* 对比/比价浮动条：勾选供应商后出现，≥2 家可并排对比。 */}
      {selected.length > 0 && (
        <div className="sticky bottom-4 z-10 flex justify-center">
          <div className="flex items-center gap-3 rounded-full border border-slate-200 bg-white px-4 py-2 shadow-lg">
            <span className="text-sm text-slate-600">已选 {selected.length} 家</span>
            <button
              className="btn-primary !rounded-full !py-1 text-sm"
              disabled={selected.length < 2}
              title={selected.length < 2 ? '请勾选至少 2 家供应商' : '并排对比评分/资质/价格/风险'}
              onClick={() => go({ name: 'compare', ids: selected })}
            >
              <Icon name="scales" size={15} /> 开始对比
            </button>
            <button className="text-sm text-slate-400 hover:text-slate-600" onClick={() => setSelected([])}>
              清空
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
