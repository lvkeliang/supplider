// Thin HTTP client for the Go sidecar (suppliderd). The app uses relative
// URLs: in `vite dev` they are proxied to 127.0.0.1:7612; in the Tauri
// build set VITE_API_BASE=http://127.0.0.1:7612. Business/UI code calls
// these functions and never touches fetch or URLs directly.
import type {
  Features,
  Page,
  Supplier,
  SupplierSummary,
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

  getSupplier: (id: string) => request<Supplier>('GET', `/api/v1/suppliers/${encodeURIComponent(id)}`),

  createSupplier: (body: Partial<Supplier>) =>
    request<Supplier>('POST', '/api/v1/suppliers', body),

  updateSupplier: (id: string, body: Record<string, unknown>) =>
    request<Supplier>('PATCH', `/api/v1/suppliers/${encodeURIComponent(id)}`, body),

  archiveSupplier: (id: string) =>
    request<void>('DELETE', `/api/v1/suppliers/${encodeURIComponent(id)}`),

  restoreSupplier: (id: string) =>
    request<Supplier>('POST', `/api/v1/suppliers/${encodeURIComponent(id)}/restore`),

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
}

/** Prefix a stored relative URL (attachment path) with the API base. */
export function apiUrl(relPath: string): string {
  return BASE + relPath
}

export { ApiError }
