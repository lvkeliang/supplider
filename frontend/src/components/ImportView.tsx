import { useMemo, useState } from 'react'
import { api } from '../api'
import type { ImportReport, Inspection } from '../types'
import { VIS_LABELS, STATUS_BLACKLISTED, STATUS_ARCHIVED } from '../types'
import type { Go } from '../App'
import { useToast } from './Toast'
import { Icon } from './Icon'

/**
 * Excel 批量导入 — manual column mapping (AI smart-mapping is a later,
 * degradable enhancement hidden behind the ai_excel_mapping flag).
 *
 * Flow: download template → pick a workbook → preview headers + suggested
 * mapping → adjust each column (fixed field / 自定义字段 / 忽略) → commit →
 * per-row report. Invalid rows are reported without aborting valid ones.
 */
export function ImportView({ go, visibilityLevels, aiExcelMapping = false }: { go: Go; visibilityLevels: number; aiExcelMapping?: boolean }) {
  const toast = useToast()
  const [file, setFile] = useState<File | null>(null)
  const [insp, setInsp] = useState<Inspection | null>(null)
  const [mapping, setMapping] = useState<Record<string, string>>({})
  const [owner, setOwner] = useState('')
  const [visibility, setVisibility] = useState(0)
  // 录入去重：勾选后命中既有供应商（含黑名单/归档）的行直接跳过；默认只警告、仍导入。
  const [skipDuplicates, setSkipDuplicates] = useState(true)
  const [busy, setBusy] = useState(false)
  const [aiMappingBusy, setAiMappingBusy] = useState(false)
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
      setReport(await api.commitImport(file, mapping, owner, visibility, skipDuplicates))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  // Fetch the template as a Blob (TR-02) so a broken/missing file is shown
  // as a toast instead of a silent or navigated-away anchor download.
  const downloadTemplate = async () => {
    toast.info('正在准备导入模板…')
    try {
      const name = await api.download('/api/v1/import/template', '供应商导入模板.xlsx')
      toast.success(`已下载「${name}」`)
    } catch (e) {
      toast.error(`模板下载失败：${e instanceof Error ? e.message : String(e)}`)
    }
  }

  const setMap = (col: number, key: string) =>
    setMapping((m) => ({ ...m, [String(col)]: key }))

  // AI 智能列映射 (TR-19-C)：让 LLM 按表头+示例值建议映射，应用后可再手动
  // 微调。无 AI 配置时按钮由 aiExcelMapping 门控隐藏。
  const runAIMapping = async () => {
    if (!insp) return
    setAiMappingBusy(true)
    try {
      const res = await api.aiExcelMap(insp.headers, insp.sample)
      const suggested = res.mapping ?? {}
      if (Object.keys(suggested).length === 0) {
        toast.info('AI 未给出可用的映射建议，请手动映射')
        return
      }
      setMapping((m) => ({ ...m, ...suggested }))
      toast.success(`已应用 AI 建议的 ${Object.keys(suggested).length} 列映射，请核对后导入`)
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setAiMappingBusy(false)
    }
  }

  // Required fixed fields that are not mapped to any column yet.
  const missingRequired = useMemo(() => {
    if (!insp) return []
    const used = new Set(Object.values(mapping))
    return insp.fields.filter((f) => f.required && !used.has(f.key))
  }, [insp, mapping])

  return (
    <div className="mx-auto max-w-3xl space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-slate-800 dark:text-slate-100">Excel 批量导入</h1>
        <button className="btn-ghost" onClick={() => go({ name: 'list' })}>返回列表</button>
      </div>

      {/* Step 1: template + file */}
      <section className="space-y-3 rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 p-4 shadow-sm">
        <div className="flex flex-wrap items-center gap-3">
          <button
            type="button"
            className="btn-ghost"
            onClick={() => void downloadTemplate()}
          >
            <Icon name="download" size={15} /> 下载导入模板（.xlsx）
          </button>
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
          {file && <span className="text-sm text-slate-500 dark:text-slate-400">{file.name}</span>}
          {busy && <span className="text-sm text-slate-400 dark:text-slate-500">处理中…</span>}
        </div>
        <p className="text-xs text-slate-400 dark:text-slate-500">
          模板首行为表头（带 * 为必填）。未匹配的列会默认作为「自定义字段」保留，可在下方调整为忽略。
        </p>
      </section>

      {error && <div className="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{error}</div>}

      {/* Step 2: column mapping */}
      {insp && (
        <section className="space-y-3 rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 p-4 shadow-sm">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-semibold text-slate-700 dark:text-slate-200">
              列映射（识别到 {insp.total_data_rows} 行数据）
            </h2>
            {aiExcelMapping && (
              <button
                type="button"
                className="btn-ghost !py-1 text-xs"
                disabled={aiMappingBusy}
                onClick={() => void runAIMapping()}
                title="让 AI 按表头与示例值建议列映射，可再手动调整"
              >
                <Icon name="shuffle" size={14} /> {aiMappingBusy ? '建议中…' : 'AI 建议映射'}
              </button>
            )}
          </div>
          {missingRequired.length > 0 && (
            <div className="rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-700">
              必填字段未映射：{missingRequired.map((f) => f.label).join('、')}（对应行将导入失败）
            </div>
          )}
          <div className="overflow-hidden rounded-md border border-slate-100 dark:border-slate-800">
            <table className="w-full text-sm">
              <thead className="bg-slate-50 dark:bg-slate-800 text-xs text-slate-500 dark:text-slate-400">
                <tr>
                  <th className="px-3 py-2 text-left">Excel 列</th>
                  <th className="px-3 py-2 text-left">示例值</th>
                  <th className="px-3 py-2 text-left">映射到</th>
                </tr>
              </thead>
              <tbody>
                {insp.headers.map((h, i) => (
                  <tr key={i} className="border-t border-slate-100 dark:border-slate-800">
                    <td className="px-3 py-1.5 font-medium text-slate-700 dark:text-slate-200">{h || `(空列 ${i + 1})`}</td>
                    <td className="max-w-[12rem] truncate px-3 py-1.5 text-slate-400 dark:text-slate-500">
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

          {/* 录入去重选项 */}
          <label className="flex items-start gap-2 rounded-md border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 px-3 py-2 text-xs text-slate-600 dark:text-slate-300">
            <input
              type="checkbox"
              className="mt-0.5"
              checked={skipDuplicates}
              onChange={(e) => setSkipDuplicates(e.target.checked)}
            />
            <span>
              <b>跳过重复供应商</b>：按统一社会信用代码（确凿）/ 公司名（疑似）比对命中则跳过该行；名称相近（近似：简称全称、同音字、一字之差，同省才提示）置信度低，<b>仅警告不跳过</b>
              （含黑名单、归档）以及本批次前面的行；命中的行不导入。取消勾选则仍导入，仅在结果中警告。
            </span>
          </label>

          <div className="flex justify-end">
            <button className="btn-primary" disabled={busy} onClick={commit}>
              {busy ? '导入中…' : `开始导入 ${insp.total_data_rows} 行`}
            </button>
          </div>
        </section>
      )}

      {/* Step 3: report */}
      {report && (
        <section className="space-y-2 rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 p-4 shadow-sm">
          <h2 className="text-sm font-semibold text-slate-700 dark:text-slate-200">导入结果</h2>
          <div className="flex gap-4 text-sm">
            <span className="text-emerald-600">成功 {report.created} 条</span>
            {(report.skipped ?? 0) > 0 && (
              <span className="text-amber-600">跳过重复 {report.skipped} 条</span>
            )}
            <span className={report.failed > 0 ? 'text-red-600' : 'text-slate-400 dark:text-slate-500'}>
              失败 {report.failed} 条
            </span>
          </div>

          {/* Duplicate warnings (命中既有供应商；跳过模式下这些行未导入） */}
          {report.duplicates && report.duplicates.length > 0 && (
            <div className="max-h-56 overflow-auto rounded-md border border-amber-100">
              <table className="w-full text-xs">
                <thead className="bg-amber-50 text-amber-800">
                  <tr>
                    <th className="px-3 py-1.5 text-left">行号</th>
                    <th className="px-3 py-1.5 text-left">本行公司名</th>
                    <th className="px-3 py-1.5 text-left">命中已有供应商</th>
                    <th className="px-3 py-1.5 text-left">强度 / 状态</th>
                  </tr>
                </thead>
                <tbody>
                  {report.duplicates.map((d, i) => (
                    <tr key={i} className="border-t border-amber-50">
                      <td className="px-3 py-1.5">第 {d.row} 行</td>
                      <td className="px-3 py-1.5 text-slate-700 dark:text-slate-200">{d.name}</td>
                      <td className="px-3 py-1.5 text-slate-600 dark:text-slate-300">
                        {d.matches.slice(0, 2).map((m, j) => (
                          <div key={j}>
                            {m.name}
                            {m.status === STATUS_BLACKLISTED && (
                              <span className="ml-1 inline-flex items-center gap-0.5 text-red-600">
                                <Icon name="ban" size={11} /> 黑名单
                              </span>
                            )}
                            {m.status === STATUS_ARCHIVED && <span className="ml-1 text-slate-400 dark:text-slate-500">已归档</span>}
                          </div>
                        ))}
                      </td>
                      <td className="px-3 py-1.5">
                        {d.matches[0]?.level === 'strong' ? (
                          <span className="text-red-600">确凿（信用代码）</span>
                        ) : d.matches[0]?.level === 'possible' ? (
                          <span className="text-slate-500 dark:text-slate-400">近似（名称相近）</span>
                        ) : (
                          <span className="text-amber-700">疑似（同名）</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
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
                      <td className="px-3 py-1.5 text-slate-600 dark:text-slate-300">{e.message}</td>
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
