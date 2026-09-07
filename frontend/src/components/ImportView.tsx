import { useMemo, useState } from 'react'
import { api, apiUrl } from '../api'
import type { ImportReport, Inspection } from '../types'
import { VIS_LABELS } from '../types'
import type { Go } from '../App'

/**
 * Excel 批量导入 — manual column mapping (AI smart-mapping is a later,
 * degradable enhancement hidden behind the ai_excel_mapping flag).
 *
 * Flow: download template → pick a workbook → preview headers + suggested
 * mapping → adjust each column (fixed field / 自定义字段 / 忽略) → commit →
 * per-row report. Invalid rows are reported without aborting valid ones.
 */
export function ImportView({ go, visibilityLevels }: { go: Go; visibilityLevels: number }) {
  const [file, setFile] = useState<File | null>(null)
  const [insp, setInsp] = useState<Inspection | null>(null)
  const [mapping, setMapping] = useState<Record<string, string>>({})
  const [owner, setOwner] = useState('')
  const [visibility, setVisibility] = useState(0)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [report, setReport] = useState<ImportReport | null>(null)

  const onPick = async (f: File) => {
    setFile(f)
    setReport(null)
    setError('')
    setBusy(true)
    try {
      const res = await api.previewImport(f)
      setInsp(res)
      setMapping({ ...res.suggested })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setInsp(null)
    } finally {
      setBusy(false)
    }
  }

  const commit = async () => {
    if (!file) return
    setBusy(true)
    setError('')
    try {
      setReport(await api.commitImport(file, mapping, owner, visibility))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const setMap = (col: number, key: string) =>
    setMapping((m) => ({ ...m, [String(col)]: key }))

  // Required fixed fields that are not mapped to any column yet.
  const missingRequired = useMemo(() => {
    if (!insp) return []
    const used = new Set(Object.values(mapping))
    return insp.fields.filter((f) => f.required && !used.has(f.key))
  }, [insp, mapping])

  return (
    <div className="mx-auto max-w-3xl space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-slate-800">Excel 批量导入</h1>
        <button className="btn-ghost" onClick={() => go({ name: 'list' })}>返回列表</button>
      </div>

      {/* Step 1: template + file */}
      <section className="space-y-3 rounded-lg border border-slate-200 bg-white p-4 shadow-sm">
        <div className="flex flex-wrap items-center gap-3">
          <a className="btn-ghost" href={apiUrl('/api/v1/import/template')}>
            ⬇ 下载导入模板（.xlsx）
          </a>
          <label className="btn-primary cursor-pointer">
            选择 Excel 文件
            <input
              type="file"
              accept=".xlsx"
              className="hidden"
              onChange={(e) => {
                const f = e.target.files?.[0]
                if (f) void onPick(f)
                e.target.value = ''
              }}
            />
          </label>
          {file && <span className="text-sm text-slate-500">{file.name}</span>}
          {busy && <span className="text-sm text-slate-400">处理中…</span>}
        </div>
        <p className="text-xs text-slate-400">
          模板首行为表头（带 * 为必填）。未匹配的列会默认作为「自定义字段」保留，可在下方调整为忽略。
        </p>
      </section>

      {error && <div className="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{error}</div>}

      {/* Step 2: column mapping */}
      {insp && (
        <section className="space-y-3 rounded-lg border border-slate-200 bg-white p-4 shadow-sm">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-semibold text-slate-700">
              列映射（识别到 {insp.total_data_rows} 行数据）
            </h2>
          </div>
          {missingRequired.length > 0 && (
            <div className="rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-700">
              必填字段未映射：{missingRequired.map((f) => f.label).join('、')}（对应行将导入失败）
            </div>
          )}
          <div className="overflow-hidden rounded-md border border-slate-100">
            <table className="w-full text-sm">
              <thead className="bg-slate-50 text-xs text-slate-500">
                <tr>
                  <th className="px-3 py-2 text-left">Excel 列</th>
                  <th className="px-3 py-2 text-left">示例值</th>
                  <th className="px-3 py-2 text-left">映射到</th>
                </tr>
              </thead>
              <tbody>
                {insp.headers.map((h, i) => (
                  <tr key={i} className="border-t border-slate-100">
                    <td className="px-3 py-1.5 font-medium text-slate-700">{h || `(空列 ${i + 1})`}</td>
                    <td className="max-w-[12rem] truncate px-3 py-1.5 text-slate-400">
                      {insp.sample.find((r) => r[i])?.[i] ?? ''}
                    </td>
                    <td className="px-3 py-1.5">
                      <select
                        className="input !py-1"
                        value={mapping[String(i)] ?? ''}
                        onChange={(e) => setMap(i, e.target.value)}
                      >
                        <option value="">忽略此列</option>
                        <option value="custom">自定义字段</option>
                        {insp.fields.map((f) => (
                          <option key={f.key} value={f.key}>
                            {f.label}{f.required ? ' *' : ''}
                          </option>
                        ))}
                      </select>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {/* Defaults */}
          <div className="grid grid-cols-2 gap-3 pt-1">
            <div>
              <label className="label">录入者标识（可留空）</label>
              <input className="input" value={owner} onChange={(e) => setOwner(e.target.value)} />
            </div>
            <div>
              <label className="label">可见性</label>
              <select className="input" value={visibility} onChange={(e) => setVisibility(Number(e.target.value))}>
                {Array.from({ length: visibilityLevels }, (_, i) => (
                  <option key={i} value={i}>{VIS_LABELS[i] ?? `等级 ${i}`}</option>
                ))}
              </select>
            </div>
          </div>

          <div className="flex justify-end">
            <button className="btn-primary" disabled={busy} onClick={commit}>
              {busy ? '导入中…' : `开始导入 ${insp.total_data_rows} 行`}
            </button>
          </div>
        </section>
      )}

      {/* Step 3: report */}
      {report && (
        <section className="space-y-2 rounded-lg border border-slate-200 bg-white p-4 shadow-sm">
          <h2 className="text-sm font-semibold text-slate-700">导入结果</h2>
          <div className="flex gap-4 text-sm">
            <span className="text-emerald-600">成功 {report.created} 条</span>
            <span className={report.failed > 0 ? 'text-red-600' : 'text-slate-400'}>
              失败 {report.failed} 条
            </span>
          </div>
          {report.errors && report.errors.length > 0 && (
            <div className="max-h-56 overflow-auto rounded-md border border-red-100">
              <table className="w-full text-xs">
                <thead className="bg-red-50 text-red-700">
                  <tr><th className="px-3 py-1.5 text-left">行号</th><th className="px-3 py-1.5 text-left">原因</th></tr>
                </thead>
                <tbody>
                  {report.errors.map((e, i) => (
                    <tr key={i} className="border-t border-red-50">
                      <td className="px-3 py-1.5">第 {e.row} 行</td>
                      <td className="px-3 py-1.5 text-slate-600">{e.message}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <div className="flex justify-end gap-2 pt-1">
            {report.created > 0 && (
              <button className="btn-primary" onClick={() => go({ name: 'list' })}>查看供应商列表</button>
            )}
          </div>
        </section>
      )}
    </div>
  )
}
