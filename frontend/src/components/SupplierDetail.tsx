import { useCallback, useEffect, useState } from 'react'
import { api, apiUrl } from '../api'
import type { Supplier } from '../types'
import { STATUS_ARCHIVED, VIS_LABELS } from '../types'
import { DocumentCard, Field } from './Card'
import type { Go } from '../App'

/**
 * Detail page — 文档卡片式档案: each facet is an independent collapsible
 * card. Fixed core fields render in labeled rows; free-form custom_fields
 * render generically (no schema assumed). Archived documents offer restore.
 */
export function SupplierDetail({ id, go }: { id: string; go: Go }) {
  const [doc, setDoc] = useState<Supplier | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [uploading, setUploading] = useState(false)

  const load = useCallback(() => {
    api
      .getSupplier(id)
      .then(setDoc)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [id])

  useEffect(load, [load])

  const archive = async () => {
    if (!confirm('确定归档该供应商？归档后列表默认隐藏，历史仍保留。')) return
    setBusy(true)
    try {
      await api.archiveSupplier(id)
      load()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const restore = async () => {
    setBusy(true)
    try {
      await api.restoreSupplier(id)
      load()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const MAX_ATTACH = 50 * 1024 * 1024
  const uploadFile = async (file: File) => {
    if (file.size > MAX_ATTACH) {
      setError(`附件 ${file.name} 超过 50MB 限制`)
      return
    }
    setUploading(true)
    setError('')
    try {
      await api.uploadAttachment(id, file)
      load() // refresh so the new attachment appears
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setUploading(false)
    }
  }

  if (error) return <div className="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{error}</div>
  if (!doc) return <div className="py-10 text-center text-slate-400">加载中…</div>

  const b = doc.basic_info
  const archived = doc.status === STATUS_ARCHIVED
  const customEntries = Object.entries(doc.custom_fields ?? {})

  return (
    <div className="mx-auto max-w-3xl space-y-3">
      {/* Header */}
      <div className="flex items-start gap-3">
        <div>
          <button className="text-sm text-slate-400 hover:text-slate-600" onClick={() => go({ name: 'list' })}>
            ← 返回列表
          </button>
          <h1 className="mt-1 text-xl font-semibold text-slate-800">
            {b.company_name}
            {archived && <span className="ml-2 rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-500">已归档</span>}
          </h1>
          <div className="mt-1 text-sm text-slate-500">
            {[b.region.province, b.region.city, b.region.district].filter(Boolean).join(' · ')}
            {doc.rating ? <span className="ml-3 text-amber-500">★ {doc.rating.toFixed(1)}</span> : null}
            <span className="ml-3">{VIS_LABELS[doc.visibility] ?? `等级${doc.visibility}`}</span>
          </div>
        </div>
        <div className="ml-auto flex gap-2">
          {!archived ? (
            <>
              <button className="btn-ghost" onClick={() => go({ name: 'edit', id })}>编辑</button>
              <button className="btn-danger" disabled={busy} onClick={archive}>归档</button>
            </>
          ) : (
            <button className="btn-primary" disabled={busy} onClick={restore}>恢复</button>
          )}
        </div>
      </div>

      {/* Risk flags banner (only when a flag is set) */}
      {doc.risk_flags && (doc.risk_flags.shell_risk || doc.risk_flags.executed_person || doc.risk_flags.admin_penalty) && (
        <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-2 text-sm text-red-700">
          风险标记：
          {doc.risk_flags.shell_risk && <span className="ml-1">空壳风险</span>}
          {doc.risk_flags.executed_person && <span className="ml-1">被执行人</span>}
          {doc.risk_flags.admin_penalty && <span className="ml-1">行政处罚</span>}
          {doc.risk_flags.notes && <span className="ml-1">（{doc.risk_flags.notes}）</span>}
        </div>
      )}

      {/* 基本信息 */}
      <DocumentCard title="基本信息">
        <dl className="grid grid-cols-2 gap-x-6">
          <Field label="统一社会信用代码" value={b.credit_code} />
          <Field label="法定代表人" value={b.legal_person} />
          <Field label="注册资本" value={b.registered_capital} />
          <Field label="成立日期" value={b.establishment_date} />
          <Field label="供应商类型" value={b.supplier_type} />
          <Field label="联系人" value={[b.contact_name, b.contact_phone].filter(Boolean).join(' ')} />
          <Field label="联系邮箱" value={b.contact_email} />
          <Field label="网址" value={b.website} />
          <Field label="地址" value={b.address} />
          <div className="col-span-2"><Field label="经营范围" value={b.business_scope} /></div>
        </dl>
      </DocumentCard>

      {/* 资质 */}
      <DocumentCard title="资质证书" count={doc.qualifications?.length ?? 0} defaultOpen={!!doc.qualifications?.length}>
        {(doc.qualifications?.length ?? 0) === 0 ? <Empty text="暂无资质记录" /> : (
          <div className="space-y-2">
            {doc.qualifications!.map((q, i) => (
              <div key={i} className="flex flex-wrap items-center gap-2 rounded-md border border-slate-100 px-3 py-2 text-sm">
                <span className="font-medium">{q.type}</span>
                {q.level && <span className="chip">{q.level}</span>}
                {q.verified && <span className="text-xs text-emerald-600">✓ 已核验</span>}
                <span className="ml-auto text-xs text-slate-400">
                  {[q.cert_no, q.issuer, q.expiry ? `到期 ${q.expiry}` : ''].filter(Boolean).join(' · ')}
                </span>
              </div>
            ))}
          </div>
        )}
      </DocumentCard>

      {/* 品类 */}
      <DocumentCard title="品类标签" count={doc.categories?.length ?? 0} defaultOpen={!!doc.categories?.length}>
        <div className="flex flex-wrap gap-1">
          {(doc.categories ?? []).map((c) => <span key={c} className="chip">{c}</span>)}
          {(doc.categories?.length ?? 0) === 0 && <Empty text="暂无品类" />}
        </div>
      </DocumentCard>

      {/* 产品/服务 */}
      <DocumentCard title="产品 / 服务" count={doc.products_services?.length ?? 0} defaultOpen={!!doc.products_services?.length}>
        {(doc.products_services?.length ?? 0) === 0 ? <Empty text="暂无产品/服务记录" /> : (
          <div className="space-y-2">
            {doc.products_services!.map((p, i) => (
              <div key={i} className="rounded-md border border-slate-100 px-3 py-2 text-sm">
                <div className="font-medium">{p.name}
                  {p.unit_price_range && <span className="ml-2 text-xs text-slate-400">{p.unit_price_range}</span>}
                </div>
                {p.desc && <div className="text-slate-500">{p.desc}</div>}
              </div>
            ))}
          </div>
        )}
      </DocumentCard>

      {/* 绩效 */}
      <DocumentCard title="合作 / 绩效记录" count={doc.performance_history?.length ?? 0} defaultOpen={!!doc.performance_history?.length}>
        {(doc.performance_history?.length ?? 0) === 0 ? <Empty text="暂无绩效记录" /> : (
          <div className="space-y-2">
            {doc.performance_history!.map((p, i) => (
              <div key={i} className="rounded-md border border-slate-100 px-3 py-2 text-sm">
                <div className="flex items-center gap-2">
                  <span className="font-medium">{p.project}</span>
                  <span className="text-amber-500">★ {p.score}</span>
                  <span className="ml-auto text-xs text-slate-400">{p.date}</span>
                </div>
                {p.feedback && <div className="text-slate-500">{p.feedback}</div>}
              </div>
            ))}
          </div>
        )}
      </DocumentCard>

      {/* 自定义字段 — 自由结构,泛化渲染 */}
      <DocumentCard title="自定义字段" count={customEntries.length} defaultOpen={customEntries.length > 0}>
        {customEntries.length === 0 ? <Empty text="暂无自定义字段" /> : (
          <dl className="grid grid-cols-2 gap-x-6">
            {customEntries.map(([k, v]) => (
              <Field key={k} label={k} value={formatCustom(v)} />
            ))}
          </dl>
        )}
      </DocumentCard>

      {/* 附件 */}
      <DocumentCard title="附件" count={doc.attachments?.length ?? 0} defaultOpen={false}>
        {!archived && (
          <div className="mb-3">
            <label className="btn-ghost cursor-pointer">
              {uploading ? '上传中…' : '＋ 上传附件（≤ 50MB）'}
              <input
                type="file"
                className="hidden"
                disabled={uploading}
                onChange={(e) => {
                  const f = e.target.files?.[0]
                  if (f) void uploadFile(f)
                  e.target.value = '' // allow re-selecting the same file
                }}
              />
            </label>
            <span className="ml-2 text-xs text-slate-400">合同 / 资质扫描件 / 营业执照等</span>
          </div>
        )}
        {(doc.attachments?.length ?? 0) === 0 ? (
          <Empty text="暂无附件" />
        ) : (
          <ul className="space-y-1 text-sm">
            {doc.attachments!.map((a, i) => (
              <li key={i} className="flex items-center gap-2">
                <a
                  className="text-brand-600 hover:underline"
                  href={apiUrl(a.url)}
                  target="_blank"
                  rel="noreferrer"
                >
                  {a.name}
                </a>
                <span className="text-xs text-slate-400">{(a.size / 1024).toFixed(0)} KB</span>
              </li>
            ))}
          </ul>
        )}
      </DocumentCard>

      {/* 变更记录 */}
      <DocumentCard title="变更记录" count={doc.change_log?.length ?? 0} defaultOpen={false}>
        {(doc.change_log?.length ?? 0) === 0 ? <Empty text="暂无变更记录" /> : (
          <ol className="space-y-1.5 text-sm">
            {[...(doc.change_log ?? [])].reverse().map((c, i) => (
              <li key={i} className="flex flex-wrap items-baseline gap-2">
                <span className="text-xs text-slate-400">{new Date(c.date).toLocaleString('zh-CN')}</span>
                <span className="font-mono text-xs text-brand-700">{c.field}</span>
                <span className="text-slate-500">
                  {c.old !== undefined && c.old !== null && c.old !== '' ? `${stringify(c.old)} → ` : ''}
                  {stringify(c.new)}
                </span>
                <span className="ml-auto text-xs text-slate-400">{c.source}</span>
              </li>
            ))}
          </ol>
        )}
      </DocumentCard>

      <div className="pb-8 text-center text-xs text-slate-300">ID: {doc.id}</div>
    </div>
  )
}

function Empty({ text }: { text: string }) {
  return <p className="text-sm text-slate-400">{text}</p>
}

function formatCustom(v: unknown): string {
  if (typeof v === 'boolean') return v ? '是' : '否'
  if (Array.isArray(v)) return v.map(stringify).join(', ')
  if (v && typeof v === 'object') return JSON.stringify(v)
  return String(v ?? '')
}

function stringify(v: unknown): string {
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}
