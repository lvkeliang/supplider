import { describe, it, expect } from 'vitest'
import { INDUSTRY_TEMPLATES, templatesValid } from './industryTemplates'

describe('industryTemplates', () => {
  it('every template is internally consistent', () => {
    expect(templatesValid()).toBe(true)
  })

  it('covers the four planned industries + a materials preset', () => {
    const labels = INDUSTRY_TEMPLATES.map((t) => t.label)
    expect(labels).toContain('市政工程')
    expect(labels).toContain('装饰装修')
    expect(labels).toContain('园林绿化')
    expect(labels).toContain('弱电智能化')
    expect(labels.length).toBeGreaterThanOrEqual(4)
  })

  it('templates carry distinct categories and a non-empty custom-field set', () => {
    for (const t of INDUSTRY_TEMPLATES) {
      expect(new Set(t.categories).size).toBe(t.categories.length)
      expect(t.customFields.length).toBeGreaterThan(0)
      expect(t.customFields.every((cf) => cf.key && ['text', 'number', 'bool'].includes(cf.kind))).toBe(true)
    }
  })
})