import { useRef, useState } from 'react'
import { api } from '../api'
import type { Go } from '../App'
import type { DocSearchResponse } from '../types'
import { Icon } from './Icon'
import { useToast } from './Toast'

/**
 * 文档分析搜索 (TR-19-E): paste a requirement document (or upload a .txt/.md
 * file), the LLM extracts the requirement, and the semantic index returns the
 * most-similar suppliers — ranked, with a match reason. Keyword + structured
 * filter search stay the non-AI path (no embedding model → this view is hidden).
 */
export function DocSearch({ go }: { go: Go }) {
  const toast = useToast()
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [indexing, setIndexing] = useState(false)
  const [result, setResult] = useState<DocSearchResponse | null>(null)
  const fileRef = useRef<HTMLInputElement>(null)

  const run = async () => {
    if (!text.trim()) {
      toast.error('请先粘贴需求文档内容')
      return
    }
    setBusy(true)
    try {
      setResult(await api.aiDocSearch(text))
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const upload = async (file: File) => {
    setBusy(true)
    try {
      setResult(await api.aiDocSearchFile(file))
      toast.success(`已分析「${file.name}」`)
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const rebuild = async () => {
    setIndexing(true)
    try {
      const r = await api.aiIndex()
      toast.success(`已重建向量索引：${r.indexed} 家供应商`)
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e))
    } finally {
      setIndexing(false)
    }
  }

  const req = result?.requirement
  const reqTags = req
    ? [req.province, req.city, req.district, req.category, req.min_qual_level].filter(Boolean)
    : []

  return (
    <div className="space-y-4">
      {/* Sticky title bar (TR-11 pattern) */}
      <div className="sticky top-[57px] z-40 -mx-4 -mt-6 bg-slate-50 px-4 py-2">
        <div className="flex items-center justify-between">
          <h1 className="text-xl font-semibold text-slate-800">AI 文档搜索</h1>
          <div className="flex gap-2">
            <button className="btn-ghost" onClick={() => go({ name: 'list' })}>返回列表</button>
            <button
              className="btn-ghost"
              disabled={indexing}
              title="将现有供应商向量化（内存索引，重启后需重建）"
              onClick={rebuild}
            >
              {indexing ? '重建中…' : '重建索引'}
            </button>
          </div>
        </div>
      </div>

      {/* Input card */}
      <div className="rounded-lg border border-slate-200 bg-white p-4 shadow-sm">
        <label className="mb-1 block text-sm font-medium text-slate-700">
          粘贴需求文档内容
        </label>
        <textarea
          className="input min-h-[160px] w-full resize-y"
          placeholder={'粘贴方案/需求清单/报价单等文本，例如：\n\n杭州市政工程需要 C30 商品混凝土约 5000m³，供应商需具备预拌混凝土专业承包二级以上资质，30 天内供货…'}
          value={text}
          onChange={(e) => setText(e.target.value)}
        />
        <div className="mt-3 flex flex-wrap items-center gap-2">
          <button className="btn-primary" disabled={busy} onClick={run}>
            <Icon name="search" size={15} /> {busy ? '分析中…' : '分析并推荐'}
          </button>
          <button
            className="btn-ghost"
            disabled={busy}
            onClick={() => fileRef.current?.click()}
          >
            <Icon name="upload" size={15} /> 上传文档
          </button>
          <input
            ref={fileRef}
            type="file"
            accept=".txt,.md,.csv,text/plain,text/markdown,text/csv"
            className="hidden"
            onChange={(e) => {
              const f = e.target.files?.[0]
              if (f) void upload(f)
              e.target.value = ''
            }}
          />
          <span className="text-xs text-slate-400">
            支持纯文本（.txt/.md/.csv）；PDF/Word/Excel/图片请先粘贴文本。首次使用请先「重建索引」。
          </span>
        </div>
      </div>

      {/* Extracted requirement + recommendations */}
      {result && req && (
        <div className="space-y-3">
          <div className="rounded-lg border border-brand-200 bg-brand-50 px-4 py-3">
            <div className="text-sm font-medium text-brand-800">提取的需求</div>
            <p className="mt-1 text-sm text-slate-700">{req.requirement}</p>
            {reqTags.length > 0 && (
              <div className="mt-2 flex flex-wrap gap-1">
                {reqTags.map((t) => (
                  <span key={t} className="chip">{t}</span>
                ))}
              </div>
            )}
          </div>

          <div className="grid gap-3">
            {result.results.map((s) => {
              const pct = Math.round(Math.max(0, Math.min(1, s.score)) * 100)
              return (
                <div
                  key={s.id}
                  onClick={() => go({ name: 'detail', id: s.id })}
                  className="cursor-pointer rounded-lg border border-slate-200 bg-white p-4 text-left shadow-sm transition hover:border-brand-500 hover:shadow"
                >
                  <div className="flex items-center gap-2">
                    <span className="font-medium text-slate-800">{s.name}</span>
                    <span className="ml-auto inline-flex items-center gap-0.5 text-sm text-amber-500">
                      <Icon name="star" size={13} filled /> {s.rating.toFixed(1)}
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
                  <div className="mt-2 flex items-center gap-2 border-t border-slate-100 pt-2 text-xs">
                    <span className="rounded-full bg-brand-100 px-2 py-0.5 font-medium text-brand-700">
                      {s.reason}
                    </span>
                    <span className="text-slate-400">相似度 {pct}%</span>
                  </div>
                </div>
              )
            })}
            {result.results.length === 0 && (
              <div className="rounded-lg border border-dashed border-slate-300 bg-white p-8 text-center text-slate-400">
                未匹配到供应商。可能是索引为空——请点击右上角「重建索引」后再试，或放宽需求中的地域/品类/资质条件。
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
