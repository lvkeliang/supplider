import { useEffect, useState, type ReactNode } from 'react'
import { api } from '../api'
import type { Performance, Supplier } from '../types'
import { STATUS_BLACKLISTED } from '../types'
import type { Go } from '../App'
import { Icon } from './Icon'

// 比价/选型对比：把搜索结果中勾选的多家供应商并排比较（评分、三维均分、
// 资质、价格区间、风险等），与 srm-cli compare / MCP compare_suppliers
// 同源同数据。只读，不做任何处置动作。

/** Mean of one performance sub-score over all records that set it. */
function dimAvg(d: Supplier, pick: (p: Performance) => number): number | null {
  const ps = d.performance_history ?? []
  let sum = 0
  let n = 0
  for (const p of ps) {
    const v = pick(p)
    if (v > 0) {
      sum += v
      n++
    }
  }
  return n > 0 ? sum / n : null
}

function fmt(v: number | null): string {
  return v === null ? '—' : v.toFixed(2)
}

export function CompareView({ ids, go }: { ids: string[]; go: Go }) {
  const [docs, setDocs] = useState<Supplier[] | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    Promise.all(ids.map((id) => api.getSupplier(id)))
      .then(setDocs)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [ids.join('|')]) // eslint-disable-line react-hooks/exhaustive-deps

  if (error) {
    return (
      <div className="space-y-3">
        <Back go={go} />
        <div className="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{error}</div>
      </div>
    )
  }
  if (!docs) return <div className="py-10 text-center text-slate-400">加载中…</div>

  const rows: { label: string; render: (d: Supplier) => ReactNode; best?: (d: Supplier) => number }[] = [
    {
      label: '地域',
      render: (d) => [d.basic_info.region.province, d.basic_info.region.city, d.basic_info.region.district].filter(Boolean).join(' '),
    },
    { label: '品类', render: (d) => (d.categories ?? []).join(' / ') || '—' },
    {
      label: '联系人',
      render: (d) => [d.basic_info.contact_name, d.basic_info.contact_phone].filter(Boolean).join(' ') || '—',
    },
    { label: '最高资质', render: (d) => d.qualifications?.map((q) => [q.level, q.type].filter(Boolean).join(' ')).join('；') || '—' },
    {
      label: '综合评分',
      render: (d) =>
        (d.rating ?? 0) > 0 ? (
          <b className="inline-flex items-center gap-0.5 text-amber-600">
            <Icon name="star" size={12} filled /> {d.rating!.toFixed(2)}
          </b>
        ) : (
          '—'
        ),
      best: (d) => d.rating ?? 0,
    },
    { label: '交付均分', render: (d) => fmt(dimAvg(d, (p) => p.delivery ?? 0)) },
    { label: '质量均分', render: (d) => fmt(dimAvg(d, (p) => p.quality ?? 0)) },
    { label: '配合度均分', render: (d) => fmt(dimAvg(d, (p) => p.cooperation ?? 0)) },
    { label: '合作项目数', render: (d) => String(d.performance_history?.length ?? 0) },
    {
      label: '价格区间',
      render: (d) =>
        (d.products_services ?? [])
          .filter((p) => p.unit_price_range)
          .map((p) => `${p.name}:${p.unit_price_range}`)
          .join('；') || '—',
    },
    {
      label: '风险 / 状态',
      render: (d) => (
        <span>
          {d.status === STATUS_BLACKLISTED && (
            <span className="mr-1 inline-flex items-center gap-0.5 rounded-full bg-red-600 px-2 py-0.5 text-xs text-white">
              <Icon name="ban" size={10} /> 黑名单
            </span>
          )}
          {d.risk_flags?.shell_risk && !d.risk_flags?.reviewed && (
            <span className="mr-1 inline-flex items-center gap-0.5 rounded-full bg-red-100 px-2 py-0.5 text-xs text-red-700">
              <Icon name="alert" size={10} /> 空壳风险
            </span>
          )}
          {d.risk_flags?.shell_risk && d.risk_flags?.reviewed && (
            <span className="mr-1 inline-flex items-center gap-0.5 rounded-full bg-emerald-100 px-2 py-0.5 text-xs text-emerald-700">
              <Icon name="check" size={10} strokeWidth={3} /> 已核验
            </span>
          )}
        </span>
      ),
    },
  ]

  // Highlight the highest value for numeric rows (rating).
  const bestOf = (pick: (d: Supplier) => number): number =>
    docs.reduce((m, d) => Math.max(m, pick(d)), 0)

  return (
    <div className="space-y-3">
      <Back go={go} />
      <h2 className="text-lg font-semibold text-slate-800">供应商对比（{docs.length} 家）</h2>

      <div className="overflow-x-auto rounded-lg border border-slate-200 bg-white shadow-sm">
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr>
              <th className="w-28 border-b border-slate-200 bg-slate-50 p-2 text-left text-xs font-medium text-slate-500">维度</th>
              {docs.map((d) => (
                <th key={d.id} className="border-b border-l border-slate-200 p-2 text-left align-top">
                  <button
                    className="font-semibold text-brand-700 hover:underline"
                    onClick={() => go({ name: 'detail', id: d.id })}
                    title="查看完整档案"
                  >
                    {d.basic_info.company_name}
                  </button>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => {
              const best = r.best ? bestOf(r.best) : 0
              return (
                <tr key={r.label} className="align-top">
                  <td className="border-b border-slate-100 bg-slate-50/60 p-2 text-xs text-slate-500">{r.label}</td>
                  {docs.map((d) => {
                    const isBest = r.best && best > 0 && r.best(d) === best
                    return (
                      <td key={d.id} className={`border-b border-l border-slate-100 p-2 ${isBest ? 'bg-amber-50' : ''}`}>
                        {r.render(d)}
                      </td>
                    )
                  })}
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      <p className="text-xs text-slate-400">
        点击公司名可进入完整档案（资质明细、附件、绩效记录、风险信号逐条说明）。评分来自绩效记录：总评优先，否则取交付/质量/配合度均值。
      </p>
    </div>
  )
}

function Back({ go }: { go: Go }) {
  return (
    <button className="text-sm text-slate-400 hover:text-slate-600" onClick={() => go({ name: 'list' })}>
      ← 返回列表
    </button>
  )
}
