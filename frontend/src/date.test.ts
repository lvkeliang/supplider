import { describe, expect, it } from 'vitest'
import { normalizeEstablishmentDate } from './date'

describe('normalizeEstablishmentDate', () => {
  it('canonicalises every accepted layout to ISO', () => {
    const cases: Array<[string, string]> = [
      ['2005-03-15', '2005-03-15'],
      ['2005-3-1', '2005-03-01'],
      ['2005/3/15', '2005-03-15'],
      ['2005.03.15', '2005-03-15'],
      ['2005年3月15日', '2005-03-15'],
      ['2005年03月15号', '2005-03-15'],
      ['20050315', '2005-03-15'],
      ['2005-03-15 00:00:00', '2005-03-15'],
      ['2005-03-15 00:00:00.0', '2005-03-15'],
      ['2005-03-15T00:00:00Z', '2005-03-15'],
    ]
    for (const [input, want] of cases) {
      expect(normalizeEstablishmentDate(input), input).toBe(want)
    }
  })

  it('maps blank input to empty string (not provided)', () => {
    expect(normalizeEstablishmentDate('')).toBe('')
    expect(normalizeEstablishmentDate('   ')).toBe('')
  })

  it('rejects unparseable text', () => {
    expect(normalizeEstablishmentDate('很久以前')).toBeNull()
    expect(normalizeEstablishmentDate('abc2005')).toBeNull()
  })

  it('rejects impossible calendar values (including leap-year rules)', () => {
    expect(normalizeEstablishmentDate('2005-13-01')).toBeNull()
    expect(normalizeEstablishmentDate('2005/2/29')).toBeNull() // 2005 平年
    expect(normalizeEstablishmentDate('2004-02-29')).toBe('2004-02-29') // 闰年
    expect(normalizeEstablishmentDate('2005-04-31')).toBeNull()
  })
})
