import { useCallback, useEffect, useState } from 'react'
import { api, type ListParams } from '../api'
import type { SupplierSummary } from '../types'
import { QUAL_LEVELS, STATUS_ARCHIVED } from '../types'
import type { Go } from '../App'

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

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-slate-800">供应商</h1>
        <button className="btn-primary" onClick={() => go({ name: 'new' })}>
          ＋ 新建供应商
        </button>
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
