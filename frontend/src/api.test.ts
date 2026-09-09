import { describe, expect, it } from 'vitest'
import { filterQuery, listQuery, type ListParams } from './api'

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

  it('omits empty/zero/false values', () => {
    const q = listQuery({ province: '', min_rating: 0, watched: false, include_archived: false })
    expect(q).not.toHaveProperty('province')
    expect(q).not.toHaveProperty('min_rating')
    expect(q).not.toHaveProperty('watched')
    expect(q).not.toHaveProperty('include_archived')
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
