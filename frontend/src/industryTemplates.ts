/**
 * 行业模板 (PRD「行业模板扩展」): pre-built field presets for common
 * construction sub-industries, so an 录入员 picks a template instead of hand
 * typing categories / supplier type / custom fields each time. Each template
 * carries a suggested supplier-type category (one of SUPPLIER_TYPES, or
 * 其他), a category set and custom-field placeholders.
 */
import { SUPPLIER_TYPES } from './supplierType'

export interface TemplateCustomField {
  key: string
  kind: 'text' | 'number' | 'bool'
  hint: string
}

export interface IndustryTemplate {
  id: string
  label: string
  /** Suggested supplier-type category (valid SUPPLIER_TYPES, or 其他). */
  supplierType: string
  categories: string[]
  customFields: TemplateCustomField[]
}

export const INDUSTRY_TEMPLATES: IndustryTemplate[] = [
  {
    id: 'municipal',
    label: '市政工程',
    supplierType: '施工承包',
    categories: ['市政工程', '道路桥梁', '管网'],
    customFields: [
      { key: '垫资能力', kind: 'text', hint: '如 500 万以内' },
      { key: '主要施工设备', kind: 'text', hint: '压路机 / 挖掘机 / 摊铺机…' },
    ],
  },
  {
    id: 'decoration',
    label: '装饰装修',
    supplierType: '施工承包',
    categories: ['装饰装修', '幕墙', '精装修'],
    customFields: [
      { key: '设计施工一体化', kind: 'bool', hint: '是否有设计与施工一体能力' },
      { key: '样板工程', kind: 'text', hint: '近三年代表项目' },
    ],
  },
  {
    id: 'landscape',
    label: '园林绿化',
    supplierType: '施工承包',
    categories: ['园林绿化', '景观工程', '绿化养护'],
    customFields: [
      { key: '苗圃基地', kind: 'text', hint: '自有苗圃地点/规模' },
      { key: '养护服务', kind: 'bool', hint: '是否提供养护' },
    ],
  },
  {
    id: 'weakCurrent',
    label: '弱电智能化',
    supplierType: '施工承包',
    categories: ['弱电工程', '智能化', '安防监控'],
    customFields: [
      { key: '系统集成范围', kind: 'text', hint: '监控 / 门禁 / 楼宇自控…' },
      { key: '软硬件代理品牌', kind: 'text', hint: '海康 / 大华 / 华为…' },
    ],
  },
  {
    id: 'materials',
    label: '建材物资供应',
    supplierType: '物资供应',
    categories: ['建筑材料', '钢材', '水泥混凝土'],
    customFields: [
      { key: '代理品牌', kind: 'text', hint: '所代理建材品牌' },
      { key: '仓储配送', kind: 'text', hint: '仓储能力 / 配送范围' },
    ],
  },
]

/** True when every template is internally-consistent (test safety net). */
export function templatesValid(): boolean {
  const ids = new Set<string>()
  for (const t of INDUSTRY_TEMPLATES) {
    if (ids.has(t.id) || !t.label || !t.supplierType) return false
    ids.add(t.id)
    if (!(SUPPLIER_TYPES as readonly string[]).includes(t.supplierType)) return false
    if (t.categories.length === 0 || t.customFields.length === 0) return false
    if (new Set(t.categories).size !== t.categories.length) return false
  }
  return true
}