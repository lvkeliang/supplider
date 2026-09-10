import { describe, expect, it } from 'vitest'
import { OTHER_TYPE, SUPPLIER_TYPES, composeSupplierType, splitSupplierType } from './supplierType'

describe('splitSupplierType', () => {
  it('maps empty values to an unset choice', () => {
    expect(splitSupplierType(undefined)).toEqual({ category: '', custom: '' })
    expect(splitSupplierType(null)).toEqual({ category: '', custom: '' })
    expect(splitSupplierType('  ')).toEqual({ category: '', custom: '' })
  })

  it('keeps a fixed category', () => {
    expect(splitSupplierType('施工承包')).toEqual({ category: '施工承包', custom: '' })
    expect(splitSupplierType('检测认证')).toEqual({ category: '检测认证', custom: '' })
  })

  it('folds legacy free-text aliases into the new taxonomy', () => {
    expect(splitSupplierType('施工商').category).toBe('施工承包')
    expect(splitSupplierType('建筑施工').category).toBe('施工承包')
    expect(splitSupplierType('贸易商').category).toBe('物资供应')
    expect(splitSupplierType('生产商').category).toBe('物资供应')
    expect(splitSupplierType('设备租赁商').category).toBe('设备租赁')
    expect(splitSupplierType('服务商').category).toBe('技术服务')
  })

  it('preserves unknown legacy values as an 其他 custom value', () => {
    expect(splitSupplierType('园林绿化专业分包')).toEqual({
      category: OTHER_TYPE,
      custom: '园林绿化专业分包',
    })
  })
})

describe('composeSupplierType', () => {
  it('stores the fixed category directly', () => {
    expect(composeSupplierType({ category: '劳务服务', custom: '' })).toBe('劳务服务')
  })

  it('stores the custom text for 其他', () => {
    expect(composeSupplierType({ category: OTHER_TYPE, custom: '  特种作业分包 ' })).toBe('特种作业分包')
  })

  it('is empty when unset', () => {
    expect(composeSupplierType({ category: '', custom: '' })).toBe('')
    expect(composeSupplierType({ category: OTHER_TYPE, custom: '  ' })).toBe('')
  })
})

describe('taxonomy', () => {
  it('provides exactly the 8 fixed categories plus 其他', () => {
    expect(SUPPLIER_TYPES).toHaveLength(8)
    expect([...SUPPLIER_TYPES, OTHER_TYPE].every((s) => s.length > 0)).toBe(true)
  })
})
