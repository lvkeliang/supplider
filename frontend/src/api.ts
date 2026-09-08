// Thin HTTP client for the Go sidecar (suppliderd). The app uses relative
// URLs: in `vite dev` they are proxied to 127.0.0.1:7612; in the Tauri
// build set VITE_API_BASE=http://127.0.0.1:7612. Business/UI code calls
// these functions and never touches fetch or URLs directly.
import type {
  DuplicateMatch,
  ExpiringReport,
  Features,
  ImportReport,
  Inspection,
  Page,
  RiskReport,
  ShellRiskReport,
  Supplier,
  SupplierSummary,
  VisibilityEnforceReport,
  VisibilityReport,
} from './types'

const BASE = import.meta.env.VITE_API_BASE ?? ''

/** List query parameters — mirrors datamodel.SupplierFilter + paging. */
export interface ListParams {
  q?: string
  province?: string
  city?: string
  district?: string
  category?: string // comma-separated for OR semantics
  min_qual_level?: string // e.g. "二级"
  min_rating?: number
  max_rating?: number
  owner?: string
  status?: string
  include_archived?: boolean
  sort?: string
  order?: 'asc' | 'desc'
  limit?: number
  cursor?: string
}

class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(BASE + path, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  })
  if (res.status === 204) return undefined as T
  const text = await res.text()
  const data = text ? JSON.parse(text) : undefined
  if (!res.ok) {
    const msg = data?.error ?? `HTTP ${res.status}`
    throw new ApiError(res.status, msg)
  }
  return data as T
}

function withQuery(path: string, params: Record<string, string | number | boolean | undefined>): string {
  const qs = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== '' && v !== false) qs.set(k, String(v))
  }
  const s = qs.toString()
  return s ? `${path}?${s}` : path
}

export const api = {
  features: () => request<Features>('GET', '/api/v1/features'),

  // Qualification-expiry maintenance scan (资质到期提醒, 提前 90/30/7 天).
  // Non-AI: pure local date math; always available.
  expiringReminders: (within = 90) =>
    request<ExpiringReport>('GET', withQuery('/api/v1/reminders/expiring', { within })),

  // Shell-company detection (空壳特征检测): pure local rules, no AI/network.
  // Live signal report for one supplier (explains WHY it was flagged).
  supplierRisk: (id: string) =>
    request<RiskReport>('GET', `/api/v1/suppliers/${encodeURIComponent(id)}/risk`),
  // Manual-review queue: active suppliers the local rules flag as shell-risk.
  shellRiskQueue: () => request<ShellRiskReport>('GET', '/api/v1/risk/shell'),

  // Human resolves a flagged supplier (人工审核闭环): 'verified' (papers
  // checked) or 'dismissed' (false positive). Clears it from the queue until
  // a risk-relevant edit reopens the review. Returns the updated document.
  reviewRisk: (id: string, outcome: 'verified' | 'dismissed', by = 'local', note = '') =>
    request<Supplier>('POST', `/api/v1/suppliers/${encodeURIComponent(id)}/risk-review`, {
      outcome,
      by,
      note,
    }),

  listSuppliers: (p: ListParams = {}) =>
    request<Page<SupplierSummary>>(
      'GET',
      withQuery('/api/v1/suppliers', {
        q: p.q,
        province: p.province,
        city: p.city,
        district: p.district,
        category: p.category,
        min_qual_level: p.min_qual_level,
        min_rating: p.min_rating,
        max_rating: p.max_rating,
        owner: p.owner,
        status: p.status,
        include_archived: p.include_archived,
        sort: p.sort,
        order: p.order,
        limit: p.limit,
        cursor: p.cursor,
      }),
    ),

  // Download URL for exporting the current filter view. `format` is 'json'
  // (full-fidelity backup bundle) or 'xlsx' (exchange workbook). The server
  // answers with Content-Disposition: attachment, so navigating to the URL
  // (or clicking a hidden anchor) starts a download without leaving the app.
  exportUrl: (p: ListParams, format: 'json' | 'xlsx') =>
    withQuery('/api/v1/export', {
      q: p.q,
      province: p.province,
      city: p.city,
      district: p.district,
      category: p.category,
      min_qual_level: p.min_qual_level,
      min_rating: p.min_rating,
      max_rating: p.max_rating,
      owner: p.owner,
      status: p.status,
      include_archived: p.include_archived,
      format,
    }),

  // Pre-entry duplicate check (录入去重): strong on credit code, probable on
  // normalized name; includes blacklisted/archived records. Non-blocking.
  checkDuplicates: (cand: { name?: string; credit_code?: string; province?: string; city?: string }) =>
    request<{ count: number; matches: DuplicateMatch[] }>(
      'GET',
      withQuery('/api/v1/suppliers/duplicates', {
        name: cand.name,
        credit_code: cand.credit_code,
        province: cand.province,
        city: cand.city,
      }),
    ),

  getSupplier: (id: string) => request<Supplier>('GET', `/api/v1/suppliers/${encodeURIComponent(id)}`),

  createSupplier: (body: Partial<Supplier>) =>
    request<Supplier>('POST', '/api/v1/suppliers', body),

  updateSupplier: (id: string, body: Record<string, unknown>) =>
    request<Supplier>('PATCH', `/api/v1/suppliers/${encodeURIComponent(id)}`, body),

  archiveSupplier: (id: string) =>
    request<void>('DELETE', `/api/v1/suppliers/${encodeURIComponent(id)}`),

  restoreSupplier: (id: string) =>
    request<Supplier>('POST', `/api/v1/suppliers/${encodeURIComponent(id)}/restore`),

  // 黑名单（淘汰/禁用）：列入后仍可搜到但醒目标记；移出恢复在库。
  blacklistSupplier: (id: string, reason: string) =>
    request<Supplier>('POST', `/api/v1/suppliers/${encodeURIComponent(id)}/blacklist`, { reason }),
  unblacklistSupplier: (id: string) =>
    request<Supplier>('POST', `/api/v1/suppliers/${encodeURIComponent(id)}/unblacklist`),

  // 可见性策略收紧处置（数据处置流程）。appeal 是录入者动作（暂停自动降级
  // 倒计时）；resolve 是管理员裁决；violations 只读扫描；enforce 执行处置。
  appealVisibility: (id: string, note: string) =>
    request<Supplier>('POST', `/api/v1/suppliers/${encodeURIComponent(id)}/appeal-visibility`, { note }),
  resolveVisibilityAppeal: (id: string, grant: boolean, maxLevel?: number) =>
    request<Supplier>('POST', `/api/v1/suppliers/${encodeURIComponent(id)}/resolve-visibility-appeal`,
      { grant, ...(maxLevel !== undefined ? { max_level: maxLevel } : {}) }),
  visibilityViolations: (maxLevel?: number, bufferDays?: number) =>
    request<VisibilityReport>('GET', withQuery('/api/v1/visibility/violations', {
      max_level: maxLevel,
      buffer_days: bufferDays,
    })),
  enforceVisibility: (maxLevel?: number, bufferDays?: number) =>
    request<VisibilityEnforceReport>('POST', '/api/v1/visibility/enforce',
      { ...(maxLevel !== undefined ? { max_level: maxLevel } : {}),
        ...(bufferDays !== undefined ? { buffer_days: bufferDays } : {}) }),

  // Upload an attachment (multipart). Returns the updated supplier document.
  // The hard limit is 50MB (enforced server-side; 413 on overflow).
  uploadAttachment: async (id: string, file: File): Promise<Supplier> => {
    const form = new FormData()
    form.append('file', file)
    const res = await fetch(BASE + `/api/v1/suppliers/${encodeURIComponent(id)}/attachments`, {
      method: 'POST',
      body: form,
    })
    const text = await res.text()
    const data = text ? JSON.parse(text) : undefined
    if (!res.ok) throw new ApiError(res.status, data?.error ?? `HTTP ${res.status}`)
    return data as Supplier
  },

  // ---- Excel import ----

  // Preview an uploaded workbook: headers + sample rows + suggested mapping.
  previewImport: async (file: File): Promise<Inspection> => {
    const form = new FormData()
    form.append('file', file)
    const res = await fetch(BASE + '/api/v1/import/preview', { method: 'POST', body: form })
    const data = await res.json().catch(() => undefined)
    if (!res.ok) throw new ApiError(res.status, data?.error ?? `HTTP ${res.status}`)
    return data as Inspection
  },

  // Commit an import with a confirmed column mapping (column index → field
  // key; "custom" keeps the column as a custom field, "" ignores it).
  commitImport: async (
    file: File,
    mapping: Record<string, string>,
    owner: string,
    visibility: number,
    skipDuplicates = false,
  ): Promise<ImportReport> => {
    const form = new FormData()
    form.append('file', file)
    form.append('mapping', JSON.stringify(mapping))
    form.append('owner', owner)
    form.append('visibility', String(visibility))
    form.append('skip_duplicates', skipDuplicates ? 'true' : 'false')
    const res = await fetch(BASE + '/api/v1/import/commit', { method: 'POST', body: form })
    const data = await res.json().catch(() => undefined)
    if (!res.ok) throw new ApiError(res.status, data?.error ?? `HTTP ${res.status}`)
    return data as ImportReport
  },
}

/** Prefix a stored relative URL (attachment/template path) with the API base. */
export function apiUrl(relPath: string): string {
  return BASE + relPath
}

export { ApiError }
