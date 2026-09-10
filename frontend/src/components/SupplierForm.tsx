import { useEffect, useRef, useState } from 'react'
import { api } from '../api'
import type { BasicInfo, DuplicateMatch, Qualification, ProductService, Performance, Supplier } from '../types'
import { QUAL_LEVELS, STATUS_BLACKLISTED, VIS_LABELS } from '../types'
import type { Go } from '../App'
import { useToast } from './Toast'
import {
  PROVINCES,
  canonicalizeCity,
  canonicalizeProvince,
  citiesOf,
  districtsOf,
  splitRegion,
} from '../regions'
import {
  OTHER_TYPE,
  SUPPLIER_TYPES,
  composeSupplierType,
  splitSupplierType,
  type SupplierTypeChoice,
} from '../supplierType'
import { normalizeEstablishmentDate } from '../date'

// 文档式录入:固定核心字段 + 可自由增删的自定义字段(不预定义字段名/类型)。
// 资质/产品/绩效也是可增删的行,贴合施工商记资质设备、贸易商记品牌规格的差异。

interface CustomRow {
  key: string
  kind: 'text' | 'number' | 'bool'
  value: string
}

interface FormState {
  owner: string
  basic: BasicInfo
  categoriesText: string
  quals: Qualification[]
  products: ProductService[]
  perfs: Performance[]
  custom: CustomRow[]
  visibility: number
  /** Supplier-type select state (basic.supplier_type is composed from it). */
  typeChoice: SupplierTypeChoice
  /** Raw text of the establishment-date box; normalised to ISO on submit. */
  estDateText: string
}

function emptyForm(visibilityLevels: number): FormState {
  return {
    owner: '',
    basic: {
      company_name: '',
      region: { province: '', city: '', district: '' },
    },
    categoriesText: '',
    quals: [],
    products: [],
    perfs: [],
    custom: [],
    visibility: Math.min(0, visibilityLevels - 1),
    typeChoice: { category: '', custom: '' },
    estDateText: '',
  }
}

function fromSupplier(s: Supplier): FormState {
  const custom: CustomRow[] = Object.entries(s.custom_fields ?? {}).map(([key, v]) => {
    if (typeof v === 'number') return { key, kind: 'number', value: String(v) }
    if (typeof v === 'boolean') return { key, kind: 'bool', value: v ? 'true' : 'false' }
    return { key, kind: 'text', value: String(v ?? '') }
  })
  return {
    owner: s.owner ?? '',
    basic: {
      ...s.basic_info,
      // Normalise legacy full-suffix values to the cascader's short names;
      // unknown values are preserved as-is and shown as an extra option.
      region: {
        province: canonicalizeProvince(s.basic_info.region.province),
        city: canonicalizeCity(
          s.basic_info.region.city,
          canonicalizeProvince(s.basic_info.region.province),
        ),
        district: s.basic_info.region.district ?? '',
      },
    },
    categoriesText: (s.categories ?? []).join(', '),
    quals: (s.qualifications ?? []).map((q) => ({ ...q })),
    products: (s.products_services ?? []).map((p) => ({ ...p })),
    perfs: (s.performance_history ?? []).map((p) => ({ ...p })),
    custom,
    visibility: s.visibility ?? 0,
    typeChoice: splitSupplierType(s.basic_info.supplier_type),
    // Canonicalise a parseable legacy date for display; keep unparseable raw.
    estDateText: (() => {
      const raw = s.basic_info.establishment_date ?? ''
      const n = normalizeEstablishmentDate(raw)
      return n === null ? raw : n
    })(),
  }
}

export function SupplierForm({ go, id, visibilityLevels }: { go: Go; id?: string; visibilityLevels: number }) {
  const toast = useToast()
  const editing = !!id
  const [form, setForm] = useState<FormState>(() => emptyForm(visibilityLevels))
  const [loading, setLoading] = useState(editing)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [dupes, setDupes] = useState<DuplicateMatch[]>([])
  const dupReq = useRef(0)

  useEffect(() => {
    if (!id) return
    api
      .getSupplier(id)
      .then((s) => setForm(fromSupplier(s)))
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false))
  }, [id])

  // Live duplicate check while ENTERING a new supplier (录入去重). Debounced;
  // skipped in edit mode (the supplier being edited would match itself).
  const name = form.basic.company_name.trim()
  const code = (form.basic.credit_code ?? '').trim()
  const prov = form.basic.region.province.trim()
  const city = form.basic.region.city.trim()
  useEffect(() => {
    if (editing) return
    if (!name && !code) {
      setDupes([])
      return
    }
    const seq = ++dupReq.current
    const t = setTimeout(() => {
      api
        .checkDuplicates({ name: form.basic.company_name, credit_code: form.basic.credit_code, province: prov, city })
        .then((r) => {
          if (seq === dupReq.current) setDupes(r.matches)
        })
        .catch(() => {
          /* best-effort hint; never block the form */
        })
    }, 400)
    return () => clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name, code, prov, city, editing])

  const setBasic = (patch: Partial<BasicInfo>) =>
    setForm((f) => ({ ...f, basic: { ...f.basic, ...patch } }))
  const setRegion = (patch: Partial<BasicInfo['region']>) =>
    setForm((f) => ({ ...f, basic: { ...f.basic, region: { ...f.basic.region, ...patch } } }))

  // 整段地址粘贴（企查查/天眼查复制）：按已知省/市/区拆分后联动填充。
  const [regionPaste, setRegionPaste] = useState('')
  const applyRegionPaste = (raw: string) => {
    const parts = splitRegion(raw)
    if (!parts.province) {
      toast.error('未识别出省/直辖市，请手动选择或检查地址文本')
      return
    }
    setRegion({
      province: parts.province,
      city: parts.city ?? (citiesOf(parts.province).length === 1 ? citiesOf(parts.province)[0] : ''),
      district: parts.district ?? '',
    })
    toast.success(
      `已识别：${[parts.province, parts.city, parts.district].filter(Boolean).join(' · ')}`,
    )
  }

  const buildCustomFields = (): Record<string, unknown> => {
    const out: Record<string, unknown> = {}
    for (const row of form.custom) {
      const key = row.key.trim()
      if (!key) continue
      if (row.kind === 'number') out[key] = Number(row.value) || 0
      else if (row.kind === 'bool') out[key] = row.value === 'true'
      else out[key] = row.value
    }
    return out
  }

  const submit = async () => {
    setError('')
    if (!form.basic.company_name.trim()) return setError('公司名称必填')
    if (!form.basic.region.province.trim() || !form.basic.region.city.trim())
      return setError('地域的省、市必填（本地化偏好依赖）')

    if (form.typeChoice.category === OTHER_TYPE && !form.typeChoice.custom.trim())
      return setError('选择「其他」类型时请填写具体类型')

    // Normalise pasted/typed dates; an unparseable value is passed through
    // unchanged (Excel imports can already contain such legacy strings, and
    // the backend tolerates free text — never block editing those records).
    const parsedEst = normalizeEstablishmentDate(form.estDateText)
    const estDate = parsedEst === null ? form.estDateText.trim() : parsedEst

    const categories = form.categoriesText.split(/[,，、\s]+/).map((s) => s.trim()).filter(Boolean)
    const body = {
      basic_info: {
        ...form.basic,
        supplier_type: composeSupplierType(form.typeChoice) || undefined,
        establishment_date: estDate || undefined,
      },
      categories,
      qualifications: form.quals.filter((q) => q.type.trim()),
      products_services: form.products.filter((p) => p.name.trim()),
      performance_history: form.perfs
        .filter((p) => p.project.trim())
        .map((p) => ({ ...p, score: Number(p.score) || 0 })),
      visibility: form.visibility,
      custom_fields: buildCustomFields(),
    }

    setSaving(true)
    try {
      if (editing && id) {
        await api.updateSupplier(id, body)
        toast.success('供应商信息已保存')
        go({ name: 'detail', id })
      } else {
        const created = await api.createSupplier({ owner: form.owner || undefined, ...body })
        toast.success(`已创建供应商「${form.basic.company_name.trim()}」`)
        go({ name: 'detail', id: created.id })
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  const cancel = () => go(editing && id ? { name: 'detail', id } : { name: 'list' })

  if (loading) return <div className="py-10 text-center text-slate-400">加载中…</div>

  return (
    <div className="mx-auto max-w-3xl space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-slate-800">{editing ? '编辑供应商' : '新建供应商'}</h1>
        <button className="btn-ghost" onClick={cancel}>取消</button>
      </div>

      {error && <div className="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{error}</div>}

      {/* 录入去重提示（仅新建时）：命中黑名单用红色，其余琥珀色 */}
      {!editing && dupes.length > 0 && (
        <div
          className={`rounded-lg border px-3 py-2 text-sm ${
            dupes.some((d) => d.status === STATUS_BLACKLISTED)
              ? 'border-red-300 bg-red-50 text-red-800'
              : 'border-amber-300 bg-amber-50 text-amber-800'
          }`}
        >
          <div className="font-medium">
            ⚠ 发现 {dupes.length} 家可能重复的供应商（按信用代码/公司名/名称相近），录入不会被阻断，请核对：
          </div>
          <ul className="mt-1 divide-y divide-current/10">
            {dupes.map((d) => (
              <li key={d.supplier_id} className="py-1">
                <button type="button" className="flex w-full items-center gap-2 text-left" onClick={() => go({ name: 'detail', id: d.supplier_id })}>
                  <span className={`rounded-full px-2 py-0.5 text-xs ${
                    d.level === 'strong' ? 'bg-red-200 text-red-900'
                      : d.level === 'possible' ? 'bg-slate-200 text-slate-700'
                        : 'bg-amber-200 text-amber-900'}`}>
                    {d.level === 'strong' ? '确凿·信用代码一致'
                      : d.level === 'possible' ? '近似·名称相近'
                        : '疑似·名称一致'}
                  </span>
                  {d.status === STATUS_BLACKLISTED && (
                    <span className="rounded-full bg-red-600 px-2 py-0.5 text-xs text-white">🚫 黑名单</span>
                  )}
                  <span className="font-medium">{d.name}</span>
                  <span className="text-xs opacity-75">
                    {[d.province, d.city].filter(Boolean).join(' ')} · {d.status}
                  </span>
                  <span className="ml-auto text-xs underline">查看</span>
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}

      {/* 基本信息 */}
      <section className="space-y-3 rounded-lg border border-slate-200 bg-white p-4 shadow-sm">
        <h2 className="text-sm font-semibold text-slate-700">基本信息</h2>
        <div className="grid grid-cols-2 gap-3">
          <div className="col-span-2">
            <label className="label">公司名称 *</label>
            <input className="input" value={form.basic.company_name}
              onChange={(e) => setBasic({ company_name: e.target.value })} />
          </div>
          <div>
            <label className="label">统一社会信用代码</label>
            <input className="input" value={form.basic.credit_code ?? ''}
              onChange={(e) => setBasic({ credit_code: e.target.value })} />
          </div>
          <div>
            <label className="label">法定代表人</label>
            <input className="input" value={form.basic.legal_person ?? ''}
              onChange={(e) => setBasic({ legal_person: e.target.value })} />
          </div>
          <div>
            <label className="label">注册资本</label>
            <input className="input" value={form.basic.registered_capital ?? ''}
              onChange={(e) => setBasic({ registered_capital: e.target.value })} />
          </div>
          <div>
            <label className="label">成立日期</label>
            <input
              className="input"
              type="text"
              inputMode="numeric"
              placeholder="2005-03-15（可直接粘贴企查查格式）"
              title="支持 2005-03-15 / 2005/3/15 / 2005.3.15 / 2005年3月15日"
              value={form.estDateText}
              onChange={(e) => setForm((f) => ({ ...f, estDateText: e.target.value }))}
              onPaste={(e) => {
                // Normalize 企查查/天眼查-style pastes in place immediately.
                const text = e.clipboardData.getData('text')
                e.preventDefault()
                const iso = normalizeEstablishmentDate(text)
                if (iso !== null) {
                  setForm((f) => ({ ...f, estDateText: iso }))
                } else {
                  setForm((f) => ({ ...f, estDateText: text }))
                  toast.error('未识别的日期格式，可手动改为 2005-03-15')
                }
              }}
              onBlur={(e) => {
                const iso = normalizeEstablishmentDate(e.target.value)
                if (iso) setForm((f) => ({ ...f, estDateText: iso }))
              }}
            />
          </div>
          <div>
            <label className="label">供应商类型</label>
            <select
              className="input"
              value={form.typeChoice.category}
              onChange={(e) =>
                setForm((f) => ({
                  ...f,
                  typeChoice: { category: e.target.value, custom: e.target.value === OTHER_TYPE ? f.typeChoice.custom : '' },
                }))
              }
            >
              <option value="">请选择（可选）</option>
              {SUPPLIER_TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
              <option value={OTHER_TYPE}>{OTHER_TYPE}（自定义）</option>
            </select>
            {form.typeChoice.category === OTHER_TYPE && (
              <input
                className="input mt-1"
                placeholder="填写具体类型，如 园林绿化专业分包"
                value={form.typeChoice.custom}
                onChange={(e) =>
                  setForm((f) => ({
                    ...f,
                    typeChoice: { ...f.typeChoice, custom: e.target.value },
                  }))
                }
              />
            )}
          </div>
          <div className="col-span-2">
            <label className="label">经营范围</label>
            <textarea className="input" rows={2} value={form.basic.business_scope ?? ''}
              onChange={(e) => setBasic({ business_scope: e.target.value })} />
          </div>
        </div>
      </section>

      {/* 联系方式 — 联系人/电话是合作前尽调与日常联系的核心字段，也用于
          空壳检测 R202（缺联系人/电话会被标记）。 */}
      <section className="space-y-3 rounded-lg border border-slate-200 bg-white p-4 shadow-sm">
        <h2 className="text-sm font-semibold text-slate-700">联系方式</h2>
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="label">联系人</label>
            <input className="input" value={form.basic.contact_name ?? ''}
              onChange={(e) => setBasic({ contact_name: e.target.value })} />
          </div>
          <div>
            <label className="label">联系电话</label>
            <input className="input" value={form.basic.contact_phone ?? ''}
              onChange={(e) => setBasic({ contact_phone: e.target.value })} />
          </div>
          <div>
            <label className="label">联系邮箱</label>
            <input className="input" type="email" value={form.basic.contact_email ?? ''}
              onChange={(e) => setBasic({ contact_email: e.target.value })} />
          </div>
          <div>
            <label className="label">网址 / 官网</label>
            <input className="input" value={form.basic.website ?? ''}
              onChange={(e) => setBasic({ website: e.target.value })} />
          </div>
          <div className="col-span-2">
            <label className="label">详细地址</label>
            <input className="input" value={form.basic.address ?? ''}
              onChange={(e) => setBasic({ address: e.target.value })} />
          </div>
        </div>
      </section>

      {/* 地域（省/市/区县级联，避免错字破坏本地优先排序；支持整段粘贴拆分） */}
      <section className="space-y-3 rounded-lg border border-slate-200 bg-white p-4 shadow-sm">
        <h2 className="text-sm font-semibold text-slate-700">地域 *（本地供应商偏好）</h2>
        <input
          className="input"
          placeholder="可粘贴整段地址自动拆分，如：浙江省杭州市西湖区文三路…"
          value={regionPaste}
          onChange={(e) => setRegionPaste(e.target.value)}
          onPaste={(e) => {
            const text = e.clipboardData.getData('text')
            e.preventDefault()
            setRegionPaste(text)
            applyRegionPaste(text)
          }}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              applyRegionPaste(regionPaste)
            }
          }}
        />
        <div className="grid grid-cols-3 gap-3">
          <select
            className="input"
            value={form.basic.region.province}
            onChange={(e) => {
              const p = e.target.value
              const cs = citiesOf(p)
              // Direct municipalities have a single self-named city level.
              setRegion({ province: p, city: cs.length === 1 ? cs[0] : '', district: '' })
            }}
          >
            <option value="">省/直辖市 *</option>
            {!PROVINCES.includes(form.basic.region.province) && form.basic.region.province && (
              <option value={form.basic.region.province}>{form.basic.region.province}（原值）</option>
            )}
            {PROVINCES.map((p) => <option key={p} value={p}>{p}</option>)}
          </select>
          <select
            className="input"
            value={form.basic.region.city}
            onChange={(e) => setRegion({ city: e.target.value, district: '' })}
            disabled={!form.basic.region.province}
          >
            <option value="">市 *</option>
            {!citiesOf(form.basic.region.province).includes(form.basic.region.city) &&
              form.basic.region.city && (
                <option value={form.basic.region.city}>{form.basic.region.city}（原值）</option>
              )}
            {citiesOf(form.basic.region.province).map((c) => <option key={c} value={c}>{c}</option>)}
          </select>
          <select
            className="input"
            value={form.basic.region.district ?? ''}
            onChange={(e) => setRegion({ district: e.target.value })}
            disabled={!form.basic.region.city}
          >
            <option value="">区/县（不限）</option>
            {!districtsOf(form.basic.region.province, form.basic.region.city).includes(
              form.basic.region.district ?? '',
            ) &&
              form.basic.region.district && (
                <option value={form.basic.region.district}>
                  {form.basic.region.district}（原值）
                </option>
              )}
            {districtsOf(form.basic.region.province, form.basic.region.city).map((d) => (
              <option key={d} value={d}>{d}</option>
            ))}
          </select>
        </div>
      </section>

      {/* 品类 */}
      <section className="space-y-2 rounded-lg border border-slate-200 bg-white p-4 shadow-sm">
        <h2 className="text-sm font-semibold text-slate-700">品类标签</h2>
        <input className="input" placeholder="多个品类用逗号分隔，如 施工服务, 市政工程"
          value={form.categoriesText} onChange={(e) => setForm((f) => ({ ...f, categoriesText: e.target.value }))} />
      </section>

      {/* 资质 */}
      <section className="space-y-2 rounded-lg border border-slate-200 bg-white p-4 shadow-sm">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold text-slate-700">资质证书</h2>
          <button className="btn-ghost !py-1 text-xs"
            onClick={() => setForm((f) => ({ ...f, quals: [...f.quals, { type: '' }] }))}>＋ 添加资质</button>
        </div>
        {form.quals.map((q, i) => (
          <div key={i} className="grid grid-cols-12 gap-2">
            <input className="input col-span-4" placeholder="资质类型（建筑工程施工总承包）" value={q.type}
              onChange={(e) => setForm((f) => updateArr(f, 'quals', i, { type: e.target.value }))} />
            <select className="input col-span-2" value={q.level ?? ''}
              onChange={(e) => setForm((f) => updateArr(f, 'quals', i, { level: e.target.value }))}>
              <option value="">等级</option>
              {QUAL_LEVELS.map((l) => <option key={l} value={l}>{l}</option>)}
            </select>
            <input className="input col-span-3" placeholder="证书编号" value={q.cert_no ?? ''}
              onChange={(e) => setForm((f) => updateArr(f, 'quals', i, { cert_no: e.target.value }))} />
            <input className="input col-span-2" type="date" title="到期日" value={q.expiry ?? ''}
              onChange={(e) => setForm((f) => updateArr(f, 'quals', i, { expiry: e.target.value }))} />
            <button className="btn-danger col-span-1 !px-2 text-xs"
              onClick={() => setForm((f) => ({ ...f, quals: f.quals.filter((_, j) => j !== i) }))}>删</button>
          </div>
        ))}
      </section>

      {/* 产品/服务 */}
      <section className="space-y-2 rounded-lg border border-slate-200 bg-white p-4 shadow-sm">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold text-slate-700">产品 / 服务</h2>
          <button className="btn-ghost !py-1 text-xs"
            onClick={() => setForm((f) => ({ ...f, products: [...f.products, { name: '' }] }))}>＋ 添加</button>
        </div>
        {form.products.map((p, i) => (
          <div key={i} className="grid grid-cols-12 gap-2">
            <input className="input col-span-4" placeholder="名称（品牌/规格/服务项）" value={p.name}
              onChange={(e) => setForm((f) => updateArr(f, 'products', i, { name: e.target.value }))} />
            <input className="input col-span-5" placeholder="说明" value={p.desc ?? ''}
              onChange={(e) => setForm((f) => updateArr(f, 'products', i, { desc: e.target.value }))} />
            <input className="input col-span-2" placeholder="价格区间" value={p.unit_price_range ?? ''}
              onChange={(e) => setForm((f) => updateArr(f, 'products', i, { unit_price_range: e.target.value }))} />
            <button className="btn-danger col-span-1 !px-2 text-xs"
              onClick={() => setForm((f) => ({ ...f, products: f.products.filter((_, j) => j !== i) }))}>删</button>
          </div>
        ))}
      </section>

      {/* 绩效 */}
      <section className="space-y-2 rounded-lg border border-slate-200 bg-white p-4 shadow-sm">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold text-slate-700">合作 / 绩效记录</h2>
          <button className="btn-ghost !py-1 text-xs"
            onClick={() => setForm((f) => ({ ...f, perfs: [...f.perfs, { project: '', score: 0 }] }))}>＋ 添加</button>
        </div>
        <p className="text-xs text-slate-400">
          总评分可直接填写（0–5）；也可只填交付/质量/配合度三维，系统按三维均值计入综合评分。
        </p>
        {form.perfs.map((p, i) => (
          <div key={i} className="space-y-2 rounded-md border border-slate-100 p-2">
            <div className="flex gap-2">
              <input className="input flex-1" placeholder="项目名称" value={p.project}
                onChange={(e) => setForm((f) => updateArr(f, 'perfs', i, { project: e.target.value }))} />
              <input className="input w-28" type="number" step="0.1" min="0" max="5" placeholder="总评 0-5"
                value={p.score || ''} title="总评分（0–5）；留空则按交付/质量/配合度均值"
                onChange={(e) => setForm((f) => updateArr(f, 'perfs', i, { score: Number(e.target.value) }))} />
              <button className="btn-danger !px-2 text-xs"
                onClick={() => setForm((f) => ({ ...f, perfs: f.perfs.filter((_, j) => j !== i) }))}>删</button>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              {([
                ['delivery', '交付'],
                ['quality', '质量'],
                ['cooperation', '配合度'],
              ] as const).map(([key, label]) => (
                <label key={key} className="flex items-center gap-1 text-xs text-slate-500">
                  {label}
                  <input className="input w-16" type="number" step="0.1" min="0" max="5" placeholder="0-5"
                    value={p[key] || ''}
                    onChange={(e) => setForm((f) => updateArr(f, 'perfs', i, { [key]: Number(e.target.value) }))} />
                </label>
              ))}
              <input className="input min-w-40 flex-1" placeholder="评价反馈" value={p.feedback ?? ''}
                onChange={(e) => setForm((f) => updateArr(f, 'perfs', i, { feedback: e.target.value }))} />
            </div>
          </div>
        ))}
      </section>

      {/* 自定义字段 — 自由扩展,不预定义 */}
      <section className="space-y-2 rounded-lg border border-slate-200 bg-white p-4 shadow-sm">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-sm font-semibold text-slate-700">自定义字段</h2>
            <p className="text-xs text-slate-400">按行业自由记录：施工商可记“垫资能力/设备”，贸易商可记“品牌/规格”。</p>
          </div>
          <button className="btn-ghost !py-1 text-xs"
            onClick={() => setForm((f) => ({ ...f, custom: [...f.custom, { key: '', kind: 'text', value: '' }] }))}>
            ＋ 添加字段
          </button>
        </div>
        {form.custom.map((c, i) => (
          <div key={i} className="grid grid-cols-12 gap-2">
            <input className="input col-span-4" placeholder="字段名（垫资能力）" value={c.key}
              onChange={(e) => setForm((f) => updateArr(f, 'custom', i, { key: e.target.value }))} />
            <select className="input col-span-2" value={c.kind}
              onChange={(e) => setForm((f) => updateArr(f, 'custom', i, { kind: e.target.value as CustomRow['kind'], value: '' }))}>
              <option value="text">文本</option>
              <option value="number">数字</option>
              <option value="bool">是/否</option>
            </select>
            {c.kind === 'bool' ? (
              <select className="input col-span-5" value={c.value}
                onChange={(e) => setForm((f) => updateArr(f, 'custom', i, { value: e.target.value }))}>
                <option value="true">是</option>
                <option value="false">否</option>
              </select>
            ) : (
              <input className="input col-span-5" type={c.kind === 'number' ? 'number' : 'text'}
                placeholder="值" value={c.value}
                onChange={(e) => setForm((f) => updateArr(f, 'custom', i, { value: e.target.value }))} />
            )}
            <button className="btn-danger col-span-1 !px-2 text-xs"
              onClick={() => setForm((f) => ({ ...f, custom: f.custom.filter((_, j) => j !== i) }))}>删</button>
          </div>
        ))}
      </section>

      {/* 可见性 */}
      <section className="space-y-2 rounded-lg border border-slate-200 bg-white p-4 shadow-sm">
        <h2 className="text-sm font-semibold text-slate-700">可见性</h2>
        <select className="input max-w-xs" value={form.visibility}
          onChange={(e) => setForm((f) => ({ ...f, visibility: Number(e.target.value) }))}>
          {Array.from({ length: visibilityLevels }, (_, i) => (
            <option key={i} value={i}>{VIS_LABELS[i] ?? `等级 ${i}`}</option>
          ))}
        </select>
        {!editing && (
          <p className="text-xs text-slate-400">
            录入者标识（个人版可留空，默认为本机用户）：
            <input className="input mt-1 max-w-xs" value={form.owner}
              onChange={(e) => setForm((f) => ({ ...f, owner: e.target.value }))} />
          </p>
        )}
      </section>

      <div className="flex justify-end gap-2 pb-8">
        <button className="btn-ghost" onClick={cancel}>取消</button>
        <button className="btn-primary" disabled={saving} onClick={submit}>
          {saving ? '保存中…' : editing ? '保存修改' : '创建供应商'}
        </button>
      </div>
    </div>
  )
}

// updateArr immutably patches one row of a form array section.
function updateArr<K extends 'quals' | 'products' | 'perfs' | 'custom'>(
  f: FormState,
  key: K,
  i: number,
  patch: Partial<FormState[K][number]>,
): FormState {
  const arr = (f[key] as unknown as Array<Record<string, unknown>>).map((row, j) =>
    j === i ? { ...row, ...patch } : row,
  )
  return { ...f, [key]: arr }
}
