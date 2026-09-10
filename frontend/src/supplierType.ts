/**
 * Supplier type taxonomy (TR-05). Previously a 6-item free-text datalist
 * (施工商/贸易商/…) that allowed inconsistent values; the form now uses a
 * fixed 9-category select with an 其他 escape hatch for a custom value.
 *
 * Storage is unchanged: basic_info.supplier_type stays one free-form Chinese
 * string (the backend and Excel importer treat it as such), so records,
 * exports and existing imports remain compatible.
 */

export const SUPPLIER_TYPES = [
  '施工承包',
  '设计咨询',
  '物资供应',
  '设备租赁',
  '物流运输',
  '检测认证',
  '技术服务',
  '劳务服务',
] as const

export const OTHER_TYPE = '其他'

/** Legacy/free-form values folded into the nearest fixed category on edit. */
const LEGACY_ALIASES: Record<string, string> = {
  施工商: '施工承包',
  建筑施工: '施工承包',
  施工: '施工承包',
  工程施工: '施工承包',
  总承包: '施工承包',
  贸易商: '物资供应',
  生产商: '物资供应',
  制造商: '物资供应',
  供应商: '物资供应',
  设备租赁商: '设备租赁',
  物流商: '物流运输',
  服务商: '技术服务',
  咨询: '设计咨询',
}

export interface SupplierTypeChoice {
  /** Select value: one of SUPPLIER_TYPES, OTHER_TYPE, or '' (unset). */
  category: string
  /** Free text when category === OTHER_TYPE. */
  custom: string
}

/**
 * Split a stored supplier_type into select + custom-text state. Fixed
 * categories map to themselves; known legacy aliases fold to their category;
 * anything else non-empty becomes an 其他 custom value (never discarded).
 */
export function splitSupplierType(stored: string | undefined | null): SupplierTypeChoice {
  const v = (stored ?? '').trim()
  if (!v) return { category: '', custom: '' }
  if ((SUPPLIER_TYPES as readonly string[]).includes(v)) return { category: v, custom: '' }
  if (v === OTHER_TYPE) return { category: OTHER_TYPE, custom: '' }
  const aliased = LEGACY_ALIASES[v]
  if (aliased) return { category: aliased, custom: '' }
  return { category: OTHER_TYPE, custom: v }
}

/** Compose the stored value from the select state; '' means unset. */
export function composeSupplierType(choice: SupplierTypeChoice): string {
  if (choice.category === OTHER_TYPE) return choice.custom.trim()
  return choice.category
}
