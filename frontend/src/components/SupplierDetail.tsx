import { useCallback, useEffect, useState } from 'react'
import { api, apiUrl } from '../api'
import type { DuplicateMatch, RiskReport, RiskSignal, Supplier } from '../types'
import { STATUS_ARCHIVED, STATUS_BLACKLISTED, VIS_LABELS } from '../types'
import { DocumentCard, Field } from './Card'
import type { Go } from '../App'
import { useToast } from './Toast'
import { Icon } from './Icon'

/** Tailwind classes + Chinese label for one risk severity. */
function sevStyle(sev: string): { cls: string; label: string } {
  switch (sev) {
    case 'high':
      return { cls: 'bg-red-100 text-red-700', label: '高' }
    case 'medium':
      return { cls: 'bg-orange-100 text-orange-700', label: '中' }
    default:
      return { cls: 'bg-slate-100 text-slate-600', label: '低' }
  }
}

function SignalRow({ sig }: { sig: RiskSignal }) {
  const s = sevStyle(sig.severity)
  return (
    <li className="flex items-start gap-2 text-sm">
      <span className={`mt-0.5 shrink-0 rounded-full px-2 py-0.5 text-xs ${s.cls}`}>{s.label}</span>
      <span className="font-mono text-xs text-slate-400">{sig.code}</span>
      <span className="text-slate-700">{sig.message}</span>
    </li>
  )
}

/**
 * Detail page — 文档卡片式档案: each facet is an independent collapsible
 * card. Fixed core fields render in labeled rows; free-form custom_fields
 * render generically (no schema assumed). Archived documents offer restore.
 */
export function SupplierDetail({ id, go }: { id: string; go: Go }) {
  const toast = useToast()
  const [doc, setDoc] = useState<Supplier | null>(null)
  const [risk, setRisk] = useState<RiskReport | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [reviewing, setReviewing] = useState(false)
  // 合并重复档案选择器：从查重结果里直接挑选要并入的档案（手输 id 仅作兜底）。
  const [mergeOpen, setMergeOpen] = useState(false)
  const [mergeCandidates, setMergeCandidates] = useState<DuplicateMatch[] | null>(null)
  const [mergeManualId, setMergeManualId] = useState('')

  const load = useCallback(() => {
    api
      .getSupplier(id)
      .then(setDoc)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
    // Live local-rule report (signals explain the verdict). Best-effort.
    api
      .supplierRisk(id)
      .then(setRisk)
      .catch(() => setRisk(null))
  }, [id])

  useEffect(load, [load])

  const archive = async () => {
    if (!confirm('确定归档该供应商？归档后列表默认隐藏，历史仍保留。')) return
    setBusy(true)
    try {
      await api.archiveSupplier(id)
      toast.success('已归档（列表默认隐藏，历史保留）')
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
      toast.success('已恢复到在库')
      load()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  // 人工审核闭环：查验原件后标记"已核验"，或标记"误报忽略"。两种结果都把
  // 供应商清出审核队列；之后若资料再被改动（信用代码/资质/品类等），后端会
  // 自动重置审核状态、重新进入队列。
  const reviewRisk = async (outcome: 'verified' | 'dismissed') => {
    const ok =
      outcome === 'verified'
        ? confirm('确认已查验营业执照/资质原件，该供应商为正规主体？')
        : confirm('确认这些风险信号为误报，忽略并清出审核队列？')
    if (!ok) return
    setReviewing(true)
    setError('')
    try {
      await api.reviewRisk(id, outcome)
      toast.success(outcome === 'verified' ? '已标记为「已核验」' : '风险信号已忽略，清出审核队列')
      load()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setReviewing(false)
    }
  }

  // 黑名单（淘汰/禁用）：列入仍可搜到但醒目标记，防止误选；移出恢复在库。
  const blacklist = async () => {
    const reason = prompt('列入黑名单的原因（如：资质造假 / 严重违约 / 恶意失信）：')
    if (reason === null) return // cancelled
    setBusy(true)
    setError('')
    try {
      await api.blacklistSupplier(id, reason.trim())
      toast.success('已列入黑名单')
      load()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const unblacklist = async () => {
    if (!confirm('确定将该供应商移出黑名单、恢复在库？')) return
    setBusy(true)
    setError('')
    try {
      await api.unblacklistSupplier(id)
      toast.success('已移出黑名单，恢复在库')
      load()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  // 关注：关注后该供应商出现空壳风险/被列入黑名单/归档/合并/资质临期时，
  // 会进入顶部铃铛的变更通知。纯本地、不写 change_log、不影响列表排序。
  const toggleWatch = async () => {
    if (!doc) return
    const next = !doc.watched
    setBusy(true)
    setError('')
    try {
      await api.watchSupplier(id, next)
      toast.success(next ? '已关注，风险/变更将在铃铛通知' : '已取消关注')
      load()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  // 合并重复供应商：打开选择器并用当前档案的公司名/信用代码跑录入查重，把
  // 疑似同一主体的档案列出来直接挑选（当前档案保留身份，并入对方的绩效/
  // 附件/资质/品类/产品线/自定义字段，对方随后归档）。黑名单/归档记录服务端
  // 拒绝，故在列表中禁用并说明。用于清理历史重复录入。
  const openMerge = async () => {
    if (mergeOpen) {
      setMergeOpen(false)
      return
    }
    setMergeOpen(true)
    setError('')
    setMergeCandidates(null)
    setMergeManualId('')
    try {
      const res = await api.checkDuplicates({
        name: doc?.basic_info.company_name,
        credit_code: doc?.basic_info.credit_code,
        province: doc?.basic_info.region.province,
        city: doc?.basic_info.region.city,
      })
      // The scan matches the current record itself (same name/code); the
      // master cannot be merged into itself.
      setMergeCandidates(res.matches.filter((m) => m.supplier_id !== id))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setMergeCandidates([])
    }
  }

  const doMerge = async (dup: DuplicateMatch | { supplier_id: string; name: string }) => {
    const dupId = dup.supplier_id.trim()
    if (dupId === id) {
      setError('不能与自身合并')
      return
    }
    if (!confirm(
      `确认将「${dup.name}」（${dupId}）并入当前档案？\n` +
      '· 当前档案保留，吸收对方的绩效/附件/资质/品类/自定义字段（冲突时以当前档案为准）\n' +
      `· 「${dup.name}」将被归档（历史保留，列表默认隐藏）\n该操作不可撤销。`,
    )) {
      return
    }
    setBusy(true)
    setError('')
    try {
      const res = await api.mergeSuppliers(id, dupId)
      setMergeOpen(false)
      load()
      const m = res.merged
      toast.success(
        `合并完成：并入绩效 ${m.performance_added}、附件 ${m.attachments_added}、资质 ${m.qualifications_added}、品类 ${m.categories_added} 条；「${dup.name}」已归档。`,
      )
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  // 可见性策略收紧：对"待调整"标记提出申诉（暂停自动降级倒计时，等待管理员
  // 裁决）。录入者也可直接在编辑里把可见范围调到合规等级，标记会在下一次
  // 处置扫描时自动解除。
  const appealVisibility = async () => {
    const note = prompt('申诉理由（说明为何需要保留当前可见范围）：')
    if (note === null) return // cancelled
    setBusy(true)
    setError('')
    try {
      await api.appealVisibility(id, note.trim())
      toast.success('申诉已提交，自动降级已暂停，等待管理员裁决')
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
      toast.success(`附件「${file.name}」已上传`)
      load() // refresh so the new attachment appears
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setUploading(false)
    }
  }

  const deleteAttachment = async (url: string, name: string) => {
    if (!confirm(`确定删除附件「${name}」？文件将从档案与磁盘移除，不可恢复。`)) return
    setBusy(true)
    setError('')
    try {
      await api.deleteAttachment(id, url)
      toast.success('附件已删除')
      load()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (error) return <div className="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{error}</div>
  if (!doc) return <div className="py-10 text-center text-slate-400">加载中…</div>

  const b = doc.basic_info
  const archived = doc.status === STATUS_ARCHIVED
  const blacklisted = doc.status === STATUS_BLACKLISTED
  const customEntries = Object.entries(doc.custom_fields ?? {})

  return (
    <div className="mx-auto max-w-3xl space-y-3">
      {/* Sticky header: title + lifecycle actions stay under the global
          header while the document cards scroll (TR-10). */}
      <div className="sticky top-[57px] z-40 -mx-4 -mt-6 bg-slate-50 px-4 pb-2 pt-4">
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
            {doc.rating ? (
              <span className="ml-3 inline-flex items-center gap-0.5 text-amber-500">
                <Icon name="star" size={13} filled /> {doc.rating.toFixed(1)}
              </span>
            ) : null}
            <span className="ml-3">{VIS_LABELS[doc.visibility] ?? `等级${doc.visibility}`}</span>
          </div>
        </div>
        <div className="ml-auto flex flex-wrap gap-2">
          {/* 关注在归档/在库状态下都可用（归档后仍保留关注与通知历史）。 */}
          <button
            className={doc.watched ? 'btn-primary' : 'btn-ghost'}
            disabled={busy}
            onClick={toggleWatch}
            title={doc.watched ? '取消关注：该供应商的变更不再进通知' : '关注：风险/黑名单/归档/资质临期等变更会进顶部铃铛通知'}
          >
            <span className="inline-flex items-center gap-1">
              <Icon name="star" size={15} filled={doc.watched} />
              {doc.watched ? '已关注' : '关注'}
            </span>
          </button>
          {archived ? (
            <button className="btn-primary" disabled={busy} onClick={restore}>恢复</button>
          ) : (
            <>
              <button className="btn-ghost" onClick={() => go({ name: 'edit', id })}>编辑</button>
              {blacklisted ? (
                <button className="btn-primary" disabled={busy} onClick={unblacklist}>移出黑名单</button>
              ) : (
                <>
                  <button className="btn-ghost" disabled={busy} onClick={openMerge} title="把另一条重复档案并入当前档案（并入绩效/附件/资质等，对方归档），用于清理历史重复录入"><Icon name="shuffle" size={15} /> 合并重复</button>
                  <button className="btn-ghost" disabled={busy} onClick={blacklist} title="列入黑名单（淘汰/禁用）：仍可搜到但醒目标记"><Icon name="ban" size={15} /> 列入黑名单</button>
                  <button className="btn-danger" disabled={busy} onClick={archive}>归档</button>
                </>
              )}
            </>
          )}
        </div>
      </div>
      </div>

      {/* 合并重复档案选择器：查重候选 + 手动 id 兜底 */}
      {mergeOpen && (
        <div className="rounded-lg border border-slate-300 bg-white px-4 py-3">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-semibold text-slate-800">选择要并入的重复档案</h3>
            <button className="text-sm text-slate-400 hover:text-slate-600" onClick={() => setMergeOpen(false)}>
              取消
            </button>
          </div>
          <p className="mt-1 text-xs text-slate-500">
            按当前档案的公司名/信用代码查重；并入后对方的绩效/附件/资质/品类/自定义字段合并到本档案，对方归档（不可撤销）。
          </p>

          {mergeCandidates === null ? (
            <p className="py-3 text-center text-sm text-slate-400">正在查重…</p>
          ) : mergeCandidates.length === 0 ? (
            <p className="py-3 text-sm text-slate-500">
              未查到疑似重复档案。如确需合并，可在下方手动输入对方档案 id（sup_ 开头）。
            </p>
          ) : (
            <ul className="mt-2 divide-y divide-slate-100">
              {mergeCandidates.map((m) => {
                const blocked =
                  m.status === STATUS_BLACKLISTED
                    ? '黑名单记录不可合并（确认造假的档案不能并入其他档案），请先移出黑名单'
                    : m.status === STATUS_ARCHIVED
                      ? '该档案已归档，请先恢复后再合并'
                      : ''
                return (
                  <li key={m.supplier_id} className="flex items-center gap-3 py-2">
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <span className="truncate text-sm font-medium text-slate-800">{m.name}</span>
                        <span
                          className={`rounded-full px-2 py-0.5 text-xs ${
                            m.level === 'strong'
                              ? 'bg-red-50 text-red-700'
                              : m.level === 'possible'
                                ? 'bg-slate-100 text-slate-600'
                                : 'bg-amber-50 text-amber-700'
                          }`}
                        >
                          {m.level === 'strong'
                            ? '确凿·信用代码一致'
                            : m.level === 'possible'
                              ? '近似·名称相近'
                              : '疑似·同名'}
                        </span>
                        {m.status === STATUS_BLACKLISTED && (
                          <span className="inline-flex items-center gap-0.5 rounded-full bg-red-600 px-2 py-0.5 text-xs text-white"><Icon name="ban" size={11} /> 黑名单</span>
                        )}
                        {m.status === STATUS_ARCHIVED && (
                          <span className="rounded-full bg-slate-200 px-2 py-0.5 text-xs text-slate-600">已归档</span>
                        )}
                      </div>
                      <div className="mt-0.5 truncate text-xs text-slate-400">
                        <span className="font-mono">{m.supplier_id}</span>
                        {m.province || m.city ? (
                          <span> · {[m.province, m.city].filter(Boolean).join(' ')}</span>
                        ) : null}
                        {m.reason ? <span> · {m.reason}</span> : null}
                      </div>
                    </div>
                    {blocked ? (
                      <span className="shrink-0 text-xs text-slate-400" title={blocked}>
                        不可合并
                      </span>
                    ) : (
                      <button
                        className="btn-ghost shrink-0 px-2 py-1 text-xs"
                        disabled={busy}
                        onClick={() => doMerge(m)}
                        title="把该档案并入当前档案，然后归档该档案"
                      >
                        并入此档案
                      </button>
                    )}
                  </li>
                )
              })}
            </ul>
          )}

          {/* 兜底：查重未覆盖时手动输入 id */}
          <div className="mt-2 flex items-center gap-2 border-t border-slate-100 pt-2">
            <input
              className="min-w-0 flex-1 rounded-md border border-slate-300 px-2 py-1 text-sm"
              placeholder="手动输入重复档案 id（sup_ 开头）"
              value={mergeManualId}
              onChange={(e) => setMergeManualId(e.target.value)}
            />
            <button
              className="btn-ghost shrink-0 px-2 py-1 text-xs"
              disabled={busy || mergeManualId.trim() === ''}
              onClick={() => doMerge({ supplier_id: mergeManualId, name: mergeManualId.trim() })}
            >
              按 id 合并
            </button>
          </div>
        </div>
      )}

      {/* 黑名单横幅（淘汰/禁用） */}
      {blacklisted && (
        <div className="flex items-center gap-2 rounded-lg border border-red-600 bg-red-600 px-4 py-2 text-sm font-medium text-white">
          <Icon name="ban" size={16} /> 该供应商已列入黑名单（淘汰/禁用），请勿选用。
          {doc.blacklist_reason ? <span className="ml-1 font-normal">原因：{doc.blacklist_reason}</span> : null}
        </div>
      )}

      {/* 可见性策略收紧：待调整横幅（数据处置流程，非 AI） */}
      {doc.vis_enforcement?.pending_adjustment && (
        <div className="rounded-lg border border-amber-400 bg-amber-50 px-4 py-2 text-sm text-amber-900">
          <div className="font-medium">
            ⏳ 可见性待调整：当前可见范围（{VIS_LABELS[doc.visibility] ?? `等级${doc.visibility}`}）
            超出策略允许的最高等级。
            {doc.vis_enforcement.appealed
              ? ' 已提交申诉，自动降级倒计时暂停，等待管理员裁决。'
              : doc.vis_enforcement.deadline
                ? ` 请于 ${doc.vis_enforcement.deadline.slice(0, 10)} 前调整或申诉，超时将自动降级。`
                : ''}
          </div>
          {doc.vis_enforcement.reason && !doc.vis_enforcement.appealed && (
            <div className="mt-0.5 text-amber-700">{doc.vis_enforcement.reason}</div>
          )}
          {doc.vis_enforcement.appealed && doc.vis_enforcement.appeal_note && (
            <div className="mt-0.5 text-amber-700">申诉理由：{doc.vis_enforcement.appeal_note}</div>
          )}
          {!doc.vis_enforcement.appealed && (
            <div className="mt-2 flex gap-2">
              <button className="btn-ghost px-2 py-1 text-xs" disabled={busy} onClick={appealVisibility}>
                我要申诉
              </button>
              <button className="btn-ghost px-2 py-1 text-xs" disabled={busy} onClick={() => go({ name: 'edit', id })}>
                去调整可见范围
              </button>
            </div>
          )}
        </div>
      )}
      {/* 申诉成立：管理员批准的例外，可保留超出上限的可见等级 */}
      {doc.vis_exception && !doc.vis_enforcement?.pending_adjustment && (
        <div className="flex items-center gap-2 rounded-lg border border-emerald-300 bg-emerald-50 px-4 py-2 text-sm text-emerald-800">
          <Icon name="check" size={15} strokeWidth={3} /> 申诉成立：该供应商的可见范围已获管理员例外批准。再次修改可见范围后需重新申请。
        </div>
      )}

      {/* 风险检测：本地规则引擎（非 AI）+ 高版本外部信号 + 人工审核闭环。
          无信号则不显示。 */}
      {(() => {
        const ext = doc.risk_flags
        const external =
          (ext?.executed_person ?? false) || (ext?.admin_penalty ?? false)
        const signals = risk?.signals ?? []
        const shell = risk?.shell_risk ?? ext?.shell_risk ?? false
        const flagged = shell || external
        const reviewed = ext?.reviewed ?? false
        if (!flagged && signals.length === 0) return null
        return (
          <DocumentCard
            title={
              flagged ? (
                reviewed ? (
                  <span className="inline-flex items-center gap-1 text-emerald-700">
                    <Icon name="check" size={14} strokeWidth={3} /> 风险已审核{shell ? '（空壳信号）' : ''}
                  </span>
                ) : (
                  <span className="inline-flex items-center gap-1 text-red-700">
                    <Icon name="alert" size={14} /> 风险检测 · 待人工审核{shell ? '（空壳风险）' : ''}
                  </span>
                )
              ) : (
                '资料补全建议'
              )
            }
            defaultOpen={flagged && !reviewed}
          >
            {flagged && reviewed && (
              <div className="mb-3 rounded-md bg-emerald-50 px-3 py-2 text-sm text-emerald-800">
                {ext?.review_outcome === 'dismissed' ? '已标记为误报忽略' : '已人工核验为正规供应商'}
                {ext?.reviewed_by ? `（审核人：${ext.reviewed_by}` : '（'}
                {ext?.reviewed_at ? ` · ${new Date(ext.reviewed_at).toLocaleString('zh-CN')}` : ''}）
                {ext?.review_note ? <div className="mt-0.5 text-emerald-700">备注：{ext.review_note}</div> : null}
                <div className="mt-0.5 text-xs text-emerald-600">资料再变更（信用代码/资质/品类等）会自动重新进入审核队列。</div>
              </div>
            )}
            <ul className="space-y-1.5">
              {ext?.executed_person && (
                <SignalRow sig={{ code: 'EXT', severity: 'high', message: '被执行人（外部工商/司法数据）' }} />
              )}
              {ext?.admin_penalty && (
                <SignalRow sig={{ code: 'EXT', severity: 'high', message: '行政处罚（外部工商数据）' }} />
              )}
              {signals.map((sig, i) => (
                <SignalRow key={`${sig.code}-${i}`} sig={sig} />
              ))}
            </ul>
            <p className="mt-2 text-xs text-slate-400">
              以上为本地规则自动检测结果（信用代码校验位 / 成立时长 / 资料完整度 / 资质 / 注册资本等），仅供人工审核参考，不会阻断录入。
            </p>
            {flagged && !reviewed && !archived && (
              <div className="mt-3 flex gap-2 border-t border-slate-100 pt-3">
                <button className="btn-primary" disabled={reviewing} onClick={() => reviewRisk('verified')}>
                  {reviewing ? '处理中…' : <span className="inline-flex items-center gap-1"><Icon name="check" size={15} strokeWidth={3} /> 已核验（查验原件，正规）</span>}
                </button>
                <button className="btn-ghost" disabled={reviewing} onClick={() => reviewRisk('dismissed')}>
                  误报忽略
                </button>
              </div>
            )}
          </DocumentCard>
        )
      })()}

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
                {q.verified && (
                  <span className="inline-flex items-center gap-0.5 text-xs text-emerald-600">
                    <Icon name="check" size={12} strokeWidth={3} /> 已核验
                  </span>
                )}
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
                  {p.score ? (
                    <span className="inline-flex items-center gap-0.5 text-amber-500">
                      <Icon name="star" size={13} filled /> {p.score}
                    </span>
                  ) : null}
                  <span className="ml-auto text-xs text-slate-400">{p.date}</span>
                </div>
                {(p.delivery || p.quality || p.cooperation) && (
                  <div className="mt-1 flex flex-wrap gap-3 text-xs text-slate-600">
                    {p.delivery ? <span>交付 <b className="text-amber-600">{p.delivery}</b></span> : null}
                    {p.quality ? <span>质量 <b className="text-amber-600">{p.quality}</b></span> : null}
                    {p.cooperation ? <span>配合度 <b className="text-amber-600">{p.cooperation}</b></span> : null}
                  </div>
                )}
                {p.feedback && <div className="mt-0.5 text-slate-500">{p.feedback}</div>}
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
                {!archived && (
                  <button
                    className="ml-auto text-xs text-red-500 hover:text-red-700"
                    disabled={busy}
                    title="删除附件（档案记录与文件一并移除）"
                    onClick={() => deleteAttachment(a.url, a.name)}
                  >
                    删除
                  </button>
                )}
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
