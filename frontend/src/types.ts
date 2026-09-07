// TypeScript mirrors of the Go domain model (backend/internal/domain) and
// the feature matrix (backend/internal/featureflag). These MUST stay in
// sync with the JSON tags the HTTP API emits — the backend is the source of
// truth; this file is the typed projection the UI uses.

export interface Region {
  province: string
  city: string
  district?: string
}

export interface BasicInfo {
  company_name: string
  credit_code?: string
  legal_person?: string
  registered_capital?: string
  establishment_date?: string
  business_scope?: string
  supplier_type?: string
  contact_name?: string
  contact_phone?: string
  contact_email?: string
  region: Region
  address?: string
  website?: string
}

export interface Qualification {
  type: string
  level?: string
  cert_no?: string
  issuer?: string
  issue_date?: string
  expiry?: string
  verified?: boolean
}

export interface ProductService {
  name: string
  desc?: string
  unit_price_range?: string
}

export interface Performance {
  project: string
  date?: string
  score: number
  delivery?: number
  quality?: number
  cooperation?: number
  feedback?: string
}

export interface RiskFlags {
  shell_risk: boolean
  executed_person: boolean
  admin_penalty: boolean
  notes?: string
  /** Human has resolved the local-rule verdict (人工审核闭环). */
  reviewed?: boolean
  reviewed_at?: string
  reviewed_by?: string
  /** 'verified' (已核验) | 'dismissed' (误报忽略) */
  review_outcome?: 'verified' | 'dismissed' | string
  review_note?: string
}

export interface ChangeEntry {
  field: string
  old?: unknown
  new?: unknown
  date: string
  source: string
}

export interface Attachment {
  name: string
  url: string
  size: number
  mime_type?: string
  uploaded_at: string
}

/** Full supplier document (GET /api/v1/suppliers/{id}). */
export interface Supplier {
  id: string
  owner: string
  status: string
  basic_info: BasicInfo
  qualifications?: Qualification[]
  categories?: string[]
  products_services?: ProductService[]
  performance_history?: Performance[]
  risk_flags?: RiskFlags
  visibility: number
  shared_with?: string[]
  custom_fields?: Record<string, unknown>
  change_log?: ChangeEntry[]
  attachments?: Attachment[]
  rating?: number
  blacklist_reason?: string
  created_at: string
  updated_at: string
  archived_at?: string
}

/** List-page projection (summary fields only — performance red line). */
export interface SupplierSummary {
  id: string
  name: string
  province: string
  city: string
  district?: string
  categories?: string[]
  top_qual?: string
  rating: number
  status: string
  /** Denormalized local-rule shell-company flag (空壳风险), set on write. */
  shell_risk?: boolean
  /** Human has cleared the flag; list badges only UN-reviewed risk. */
  risk_reviewed?: boolean
  updated_at: string
}

export interface Page<T> {
  items: T[]
  next_cursor?: string
}

// ---- Maintenance reminders (资质到期提醒, GET /api/v1/reminders/expiring) ----

/** One certificate already expired or inside the 90/30/7-day windows. */
export interface ExpiryAlert {
  supplier_id: string
  supplier_name: string
  province?: string
  city?: string
  qual_type: string
  qual_level?: string
  cert_no?: string
  expiry: string
  /** Negative = expired that many days ago; 0 = expires today. */
  days_left: number
  bucket: 'expired' | '7d' | '30d' | '90d'
}

export interface ExpiringReport {
  generated_at: string
  within_days: number
  count: number
  expired: number
  items: ExpiryAlert[]
}

// ---- Shell-company risk detection (空壳特征检测, non-AI rule engine) ----

/** One fired local rule. Codes are stable (R1xx identity, R2xx profile, R3xx financial). */
export interface RiskSignal {
  code: string
  severity: 'high' | 'medium' | 'low'
  message: string
}

/** Live rule-engine report for one supplier (GET /api/v1/suppliers/{id}/risk). */
export interface RiskReport {
  shell_risk: boolean
  signals: RiskSignal[]
  checked_at: string
}

/** One supplier in the manual-review queue, with its fired signals. */
export interface ShellRiskItem {
  supplier_id: string
  supplier_name: string
  province?: string
  city?: string
  high_count: number
  medium_count: number
  signals: RiskSignal[]
}

export interface ShellRiskReport {
  generated_at: string
  count: number
  items: ShellRiskItem[]
}

export interface Features {
  tier: string
  ai_enabled: boolean
  ai_ocr_entry: boolean
  ai_doc_search: boolean
  ai_nl_search: boolean
  ai_excel_mapping: boolean
  visibility_levels: number
  rbac: boolean
  audit_log: boolean
  approval_flow: boolean
  storage: string
  search_engine: string
  vector_store: string
  object_storage: string
  queue: string
}

// ---- Excel import ----

export interface FieldOption {
  key: string
  label: string
  required: boolean
}

export interface Inspection {
  headers: string[]
  sample: string[][]
  /** Column index (as string) → field key; "custom" = custom_field, "" = ignore. */
  suggested: Record<string, string>
  fields: FieldOption[]
  total_data_rows: number
}

export interface ImportError {
  row: number
  message: string
}

export interface ImportReport {
  created: number
  failed: number
  ids?: string[]
  errors?: ImportError[]
}

// Lifecycle / visibility constants mirrored from the Go domain package.
export const STATUS_ACTIVE = 'active'
export const STATUS_PENDING = 'pending'
export const STATUS_ARCHIVED = 'archived'
export const STATUS_BLACKLISTED = 'blacklisted'

export const VIS_LABELS: Record<number, string> = {
  0: '仅自己可见',
  1: '指定人可见',
  2: '本部门可见',
  3: '指定部门可见',
  4: '全公司可见',
}

/** Chinese construction qualification ranks, ordered strongest first. */
export const QUAL_LEVELS = ['特级', '一级', '二级', '三级', '甲级', '乙级', '丙级'] as const
