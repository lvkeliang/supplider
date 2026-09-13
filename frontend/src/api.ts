// Thin HTTP client for the Go sidecar (suppliderd). The app uses relative
// URLs: in `vite dev` they are proxied to 127.0.0.1:7612; in the Tauri
// build set VITE_API_BASE=http://127.0.0.1:7612. Business/UI code calls
// these functions and never touches fetch or URLs directly.
import type {
  AIConfigResponse,
  AITestResult,
  NLSearchResult,
  OCRResult,
  DocSearchResponse,
  SemanticIndexResult,
  DuplicateMatch,
  ExpiringReport,
  Features,
  ImportReport,
  Inspection,
  LocalPreference,
  MergeResult,
  NotificationsResponse,
  Page,
  RestoreStatus,
  RiskReport,
  ShellRiskReport,
  Supplier,
  SupplierSummary,
  VisibilityEnforceReport,
  VisibilityPolicyResponse,
  VisibilityPolicySaveResponse,
  VisibilityReport,
} from './types'

/**
 * Resolve the API base.
 * - Explicit VITE_API_BASE wins (used by the embedded-web build via
 *   scripts/build-frontend.sh).
 * - Inside the Tauri desktop shell the frontend is served from
 *   tauri://localhost (not the sidecar), so relative URLs would miss the
 *   Go daemon — point straight at the loopback sidecar it spawns
 *   (src-tauri SIDECAR_ADDR constant). `tauri build` runs `npm run build`
 *   WITHOUT the env var, so this runtime detection is what makes the
 *   packaged app connect.
 * - In a plain browser / `vite dev`, relative URLs work: dev proxies to
 *   the sidecar and the embedded UI is served from the same origin.
 */
function resolveBase(): string {
  if (import.meta.env.VITE_API_BASE) return import.meta.env.VITE_API_BASE
  if (typeof window !== 'undefined') {
    const loc = window.location
    const inTauri = '__TAURI_INTERNALS__' in window || '__TAURI__' in window
    // Packaged Tauri serves from tauri://localhost (macOS/Linux) or
    // http://tauri.localhost (Windows); `tauri dev` instead uses the
    // vite server (localhost:5173) where relative URLs already proxy.
    const tauriProdOrigin =
      inTauri && (loc.protocol === 'tauri:' || loc.hostname === 'tauri.localhost')
    if (tauriProdOrigin) {
      return 'http://127.0.0.1:7612'
    }
  }
  return ''
}

const BASE = resolveBase()

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
  /** Only followed (关注) suppliers. */
  watched?: boolean
  sort?: string
  order?: 'asc' | 'desc'
  limit?: number
  cursor?: string
  /**
   * Local-first switch (本地供应商偏好): undefined/true uses the saved home
   * region automatically; false sends prefer=0 to disable ranking once.
   */
  localFirst?: boolean
}

class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

/**
 * Network-health events for the App-level connection gate.
 *
 * fetch() only REJECTS when no response came back at all (sidecar not
 * running, port closed) — a 4xx/5xx still resolves and means the backend is
 * alive, so it is reported as 'success' and must never trip the gate.
 * Rejections are the runtime "sidecar died" signal; the gate confirms with
 * a /readyz probe before flipping state (guards against one-off aborts).
 */
export type NetworkEvent = 'success' | 'failure'
let networkListener: ((event: NetworkEvent) => void) | null = null

export function setNetworkEventListener(listener: ((event: NetworkEvent) => void) | null) {
  networkListener = listener
}

/** Every outbound HTTP call goes through here so health events fire once. */
async function guardedFetch(path: string, init?: RequestInit): Promise<Response> {
  try {
    const res = await fetch(BASE + path, init)
    networkListener?.('success')
    return res
  } catch (err) {
    networkListener?.('failure')
    throw err
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await guardedFetch(path, {
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

/** Compact filter params, shared by the list and export endpoints. */
function compact(
  params: Record<string, string | number | boolean | undefined>,
): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(params)) {
    // Only undefined / empty string / false mean "unset". A numeric 0 is a
    // real value: max_rating=0 is "unrated only" (the backend distinguishes
    // an explicit 0 from an absent bound via a presence flag). min_rating=0
    // simply matches everything. Dropping 0 here silently broke those.
    if (v === undefined || v === '' || v === false) continue
    out[k] = String(v)
  }
  return out
}

/** Filter-only query (export walks the whole library; no paging/prefer). */
export function filterQuery(p: ListParams): Record<string, string> {
  return compact({
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
    watched: p.watched,
  })
}

/**
 * Serializes list filters + paging into query parameters (pure; unit-tested).
 * The backend strictly rejects unrecognized min_qual_level values, so the
 * controlled value passes through verbatim.
 */
export function listQuery(p: ListParams): Record<string, string> {
  return compact({
    ...filterQuery(p),
    sort: p.sort,
    order: p.order,
    limit: p.limit,
    cursor: p.cursor,
    // The list endpoint ranks home-region suppliers first by default via
    // the saved preference; prefer=0 opts out for one request.
    prefer: p.localFirst === false ? '0' : undefined,
  })
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

  // Liveness probe (the Tauri shell polls the same endpoint). Never errors
  // with a JSON-parse expectation: /readyz answers {"status":"ok"}.
  readyz: () => request<{ status?: string }>('GET', '/readyz'),

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
    request<Page<SupplierSummary>>('GET', withQuery('/api/v1/suppliers', listQuery(p))),

  // Follow / unfollow a supplier (关注). Watching writes no change_log and
  // does not reorder lists; watched suppliers raise change notifications.
  watchSupplier: (id: string, watched: boolean) =>
    request<Supplier>('POST', `/api/v1/suppliers/${encodeURIComponent(id)}/watch`, { watched }),

  // Change-notification feed (变更推送通知关注者).
  notifications: (unreadOnly = false) =>
    request<NotificationsResponse>(
      'GET',
      withQuery('/api/v1/notifications', {
        unread: unreadOnly ? true : undefined,
        limit: 50,
      }),
    ),
  markNotificationRead: (id: string) =>
    request<{ ok: boolean }>('POST', `/api/v1/notifications/${encodeURIComponent(id)}/read`),
  markAllNotificationsRead: () =>
    request<{ ok: boolean }>('POST', '/api/v1/notifications/read-all'),

  // Download URL for exporting the current filter view. `format` is 'json'
  // (full-fidelity backup bundle) or 'xlsx' (exchange workbook). Feed the
  // result to api.download() so the fetch is tracked (toast start/done/fail).
  exportUrl: (p: ListParams, format: 'json' | 'xlsx') =>
    withQuery('/api/v1/export', { ...filterQuery(p), format }),

  // Streamed downloads (import template / filtered export / full backup):
  // fetch as a Blob and trigger the browser save, so callers know when the
  // transfer actually finished (a fire-and-forget anchor click does not).
  // Resolves with the effective filename; rejects on HTTP errors like any
  // other call so the UI can toast the failure.
  download: async (path: string, fallbackName: string): Promise<string> => {
    const res = await guardedFetch(path)
    if (!res.ok) {
      const data = await res.json().catch(() => undefined)
      throw new ApiError(res.status, data?.error ?? `HTTP ${res.status}`)
    }
    const blob = await res.blob()
    const name = filenameFromDisposition(res.headers.get('Content-Disposition'), fallbackName)
    saveBlob(blob, name)
    return name
  },

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

  // 合并重复供应商：把 duplicateId 的绩效/附件/资质/品类/产品线/自定义字段并入
  // id（保留 id 的身份），duplicateId 随后归档。黑名单/归档记录服务端拒绝。
  mergeSuppliers: (masterId: string, duplicateId: string) =>
    request<{ supplier: Supplier; merged: MergeResult }>(
      'POST',
      `/api/v1/suppliers/${encodeURIComponent(masterId)}/merge`,
      { duplicate_id: duplicateId },
    ),

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
  // 持久化可见性策略：GET 返回当前生效策略（管理员配置或版本默认）；
  // PUT 保存策略并立即执行一次处置扫描（sidecar 开机/每日 24h 也会自动跑）。
  getVisibilityPolicy: () =>
    request<VisibilityPolicyResponse>('GET', '/api/v1/visibility/policy'),
  saveVisibilityPolicy: (maxLevel: number, bufferDays?: number) =>
    request<VisibilityPolicySaveResponse>('PUT', '/api/v1/visibility/policy',
      { max_level: maxLevel, ...(bufferDays !== undefined ? { buffer_days: bufferDays } : {}) }),

  // 本地供应商偏好：设置常驻地域后，列表/搜索中本地供应商排最前（仅排序，
  // 不筛选）。清除后恢复默认排序。
  getLocalPreference: () =>
    request<LocalPreference>('GET', '/api/v1/preferences/local'),
  saveLocalPreference: (province: string, city = '') =>
    request<LocalPreference>('PUT', '/api/v1/preferences/local', { province, city }),
  clearLocalPreference: () =>
    request<LocalPreference>('PUT', '/api/v1/preferences/local?clear=1'),

  // AI provider config (TR-19-A): read/write the self-configured model and
  // run a connectivity test. api_key is never returned in full.
  getAIConfig: () => request<AIConfigResponse>('GET', '/api/v1/ai/config'),
  saveAIConfig: (cfg: {
    format: string
    base_url: string
    api_key: string
    model: string
    max_tokens?: number
  }) => request<AIConfigResponse>('PUT', '/api/v1/ai/config', cfg),
  testAI: () => request<AITestResult>('GET', '/api/v1/ai/test'),
  // OCR 辅助录入 (TR-19-B): upload a license photo, get extracted fields.
  ocrUpload: async (file: File): Promise<OCRResult> => {
    const form = new FormData()
    form.append('file', file)
    const res = await guardedFetch('/api/v1/ai/ocr', { method: 'POST', body: form })
    const data = await res.json().catch(() => undefined)
    if (!res.ok) throw new ApiError(res.status, data?.error ?? `HTTP ${res.status}`)
    return data as OCRResult
  },
  // Excel 智能列映射 (TR-19-C): LLM suggests a column→field mapping for the
  // previewed workbook (user still reviews/adjusts it).
  aiExcelMap: (headers: string[], sample: string[][]) =>
    request<{ mapping: Record<string, string> }>('POST', '/api/v1/ai/excel-map', { headers, sample }),
  // 自然语言搜索 (TR-19-D): turn a free-form sentence into a structured filter.
  aiNLSearch: (query: string) =>
    request<NLSearchResult>('POST', '/api/v1/ai/nl-search', { query }),
  // 文档分析搜索 (TR-19-E): rebuild the vector index, then analyze a pasted
  // requirement document (or an uploaded .txt/.md file) → ranked recommendations.
  aiIndex: () => request<SemanticIndexResult>('POST', '/api/v1/ai/index'),
  aiDocSearch: (text: string, topK = 10) =>
    request<DocSearchResponse>('POST', '/api/v1/ai/doc-search', { text, top_k: topK }),
  aiDocSearchFile: async (file: File): Promise<DocSearchResponse> => {
    const form = new FormData()
    form.append('file', file)
    const res = await guardedFetch('/api/v1/ai/doc-search', { method: 'POST', body: form })
    const data = await res.json().catch(() => undefined)
    if (!res.ok) throw new ApiError(res.status, data?.error ?? `HTTP ${res.status}`)
    return data as DocSearchResponse
  },

  // In-app restore/migration: validate and stage a backup zip; the swap
  // takes effect at the next application restart (current data is kept in
  // a restore.rollback-* directory). 202 Accepted carries the manifest.
  getRestoreStatus: () =>
    request<RestoreStatus>('GET', '/api/v1/backup/restore'),
  uploadRestore: async (file: File): Promise<RestoreStatus> => {
    const res = await guardedFetch('/api/v1/backup/restore', {
      method: 'POST',
      headers: { 'Content-Type': 'application/zip' },
      body: file,
    })
    const text = await res.text()
    const data = text ? JSON.parse(text) : undefined
    if (!res.ok) throw new ApiError(res.status, data?.error ?? `HTTP ${res.status}`)
    return data as RestoreStatus
  },
  cancelRestore: () =>
    request<RestoreStatus>('DELETE', '/api/v1/backup/restore'),

  // Upload an attachment (multipart). Returns the updated supplier document.
  // The hard limit is 50MB (enforced server-side; 413 on overflow).
  uploadAttachment: async (id: string, file: File): Promise<Supplier> => {
    const form = new FormData()
    form.append('file', file)
    const res = await guardedFetch(`/api/v1/suppliers/${encodeURIComponent(id)}/attachments`, {
      method: 'POST',
      body: form,
    })
    const text = await res.text()
    const data = text ? JSON.parse(text) : undefined
    if (!res.ok) throw new ApiError(res.status, data?.error ?? `HTTP ${res.status}`)
    return data as Supplier
  },

  // Remove an attachment: deletes the document record and the backing file.
  // url is the stored attachment URL (apiUrl-relative path).
  deleteAttachment: (id: string, url: string) =>
    request<Supplier>('DELETE',
      withQuery(`/api/v1/suppliers/${encodeURIComponent(id)}/attachments`, { url })),

  // ---- Excel import ----

  // Preview an uploaded workbook: headers + sample rows + suggested mapping.
  previewImport: async (file: File): Promise<Inspection> => {
    const form = new FormData()
    form.append('file', file)
    const res = await guardedFetch('/api/v1/import/preview', { method: 'POST', body: form })
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
    const res = await guardedFetch('/api/v1/import/commit', { method: 'POST', body: form })
    const data = await res.json().catch(() => undefined)
    if (!res.ok) throw new ApiError(res.status, data?.error ?? `HTTP ${res.status}`)
    return data as ImportReport
  },
}

/** Prefix a stored relative URL (attachment/template path) with the API base. */
export function apiUrl(relPath: string): string {
  return BASE + relPath
}

/**
 * Extract a download filename from Content-Disposition, preferring the RFC
 * 5987 `filename*=UTF-8''…` form (the sidecar uses it for Chinese names),
 * then the legacy quoted `filename="…"`, then the caller's fallback.
 * Pure so the parsing contract is unit-testable.
 */
export function filenameFromDisposition(
  disposition: string | null | undefined,
  fallback: string,
): string {
  if (disposition) {
    const star = /filename\*\s*=\s*([^']+)'([^']*)'([^;]+)/i.exec(disposition)
    if (star?.[3]) {
      try {
        return decodeURIComponent(star[3].trim())
      } catch {
        // Malformed percent-encoding — fall through to legacy/fallback.
      }
    }
    const plain = /filename\s*=\s*"?([^";]+?)"?(?:;|$)/i.exec(disposition)
    if (plain?.[1]) return plain[1].trim()
  }
  return fallback
}

/** Trigger a browser save for an in-memory Blob, then release its object URL. */
function saveBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  // The browser claims the blob synchronously on click; revoke shortly after
  // so WebKit-based webviews have time to pick it up.
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

export { ApiError }
