import { describe, expect, it } from 'vitest'
import { filenameFromDisposition, filterQuery, listQuery, type ListParams } from './api'

// These serializers build EVERY list/export URL. A wrong/missing key would
// silently disable a filter on the backend (which now strictly rejects bad
// min_qual_level values and treats unknown inputs as errors), so the
// contract is pinned here without a DOM or a running sidecar.

describe('listQuery', () => {
  it('returns an empty object when no filter is set', () => {
    expect(listQuery({})).toEqual({})
  })

  it('passes filters through verbatim, including the strict qual level', () => {
    const p: ListParams = {
      q: '杭州混凝土',
      province: '浙江',
      city: '杭州',
      category: '施工服务,市政',
      min_qual_level: '二级',
      min_rating: 4,
      limit: 50,
      cursor: 'abc',
    }
    expect(listQuery(p)).toEqual({
      q: '杭州混凝土',
      province: '浙江',
      city: '杭州',
      category: '施工服务,市政',
      min_qual_level: '二级',
      min_rating: '4',
      limit: '50',
      cursor: 'abc',
    })
  })

  it('omits empty/false values but keeps numeric 0 (an explicit bound)', () => {
    const q = listQuery({ province: '', watched: false, include_archived: false })
    expect(q).not.toHaveProperty('province')
    expect(q).not.toHaveProperty('watched')
    expect(q).not.toHaveProperty('include_archived')
  })

  it('serializes rating bound 0 (min=0 matches all; max=0 is unrated-only)', () => {
    expect(listQuery({ min_rating: 0 }).min_rating).toBe('0')
    expect(filterQuery({ max_rating: 0 }).max_rating).toBe('0')
  })

  it('sends prefer=0 only when local-first is explicitly disabled', () => {
    expect(listQuery({ localFirst: false })).toEqual({ prefer: '0' })
    // default (undefined) → no key, backend applies saved preference
    expect(listQuery({})).not.toHaveProperty('prefer')
    // explicit true also sends nothing (prefer is opt-OUT)
    expect(listQuery({ localFirst: true })).not.toHaveProperty('prefer')
  })

  it('serializes booleans/numbers as strings', () => {
    const q = listQuery({ watched: true, include_archived: true, limit: 20 })
    expect(q.watched).toBe('true')
    expect(q.include_archived).toBe('true')
    expect(q.limit).toBe('20')
  })
})

describe('filterQuery (export)', () => {
  it('keeps filters but drops paging/sort/prefer (export walks all rows)', () => {
    const q = filterQuery({
      province: '浙江',
      limit: 100,
      cursor: 'xyz',
      sort: 'rating',
      localFirst: false,
    })
    expect(q).toEqual({ province: '浙江' })
  })

  it('preserves the qual level for hard-filtered exports', () => {
    expect(filterQuery({ min_qual_level: '一级' })).toEqual({ min_qual_level: '一级' })
  })
})

// The fetch()+Blob download (TR-02) reads the filename from the sidecar's
// Content-Disposition; cross-origin Tauri needs the CORS expose header and
// the parser must handle the RFC 5987 Chinese-name form the Go handlers emit.
describe('filenameFromDisposition', () => {
  it('decodes the RFC 5987 filename* form (Chinese names)', () => {
    const cd = `attachment; filename="template.xlsx"; filename*=UTF-8''${encodeURIComponent('供应商导入模板.xlsx')}`
    expect(filenameFromDisposition(cd, 'fallback.xlsx')).toBe('供应商导入模板.xlsx')
  })

  it('prefers filename* even when a legacy filename precedes it', () => {
    const cd = `attachment; filename="download"; filename*=UTF-8''back%20up.zip`
    expect(filenameFromDisposition(cd, 'fb.zip')).toBe('back up.zip')
  })

  it('falls back to the quoted legacy filename', () => {
    expect(filenameFromDisposition('attachment; filename="data.json";', 'fb.json')).toBe('data.json')
  })

  it('uses the fallback when the header is missing or malformed', () => {
    expect(filenameFromDisposition(null, 'fb.bin')).toBe('fb.bin')
    expect(filenameFromDisposition(undefined, 'fb.bin')).toBe('fb.bin')
    expect(filenameFromDisposition('', 'fb.bin')).toBe('fb.bin')
    // malformed percent-encoding in filename* falls through to fallback
    expect(filenameFromDisposition("filename*=UTF-8''a%0ZZ", 'fb.bin')).toBe('fb.bin')
  })
})
