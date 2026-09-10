import { describe, expect, it } from 'vitest'
import {
  PROVINCES,
  canonicalizeCity,
  canonicalizeProvince,
  citiesOf,
  districtsOf,
  splitRegion,
} from './regions'

describe('region tables', () => {
  it('covers the 31 provincial divisions with short names', () => {
    expect(PROVINCES.length).toBe(31)
    expect(PROVINCES).toContain('浙江')
    expect(PROVINCES).toContain('内蒙古')
    expect(PROVINCES).toContain('广西')
  })

  it('lists cities and districts', () => {
    expect(citiesOf('浙江')).toContain('杭州')
    expect(districtsOf('浙江', '杭州')).toContain('西湖区')
    expect(citiesOf('不存在的省')).toEqual([])
  })
})

describe('canonicalizeProvince / canonicalizeCity', () => {
  it('strips official suffixes back to the table name', () => {
    expect(canonicalizeProvince('浙江省')).toBe('浙江')
    expect(canonicalizeProvince('北京市')).toBe('北京')
    expect(canonicalizeProvince('广西壮族自治区')).toBe('广西')
    expect(canonicalizeProvince('内蒙古自治区')).toBe('内蒙古')
  })

  it('keeps valid short names and preserves unknown legacy values', () => {
    expect(canonicalizeProvince('浙江')).toBe('浙江')
    expect(canonicalizeProvince('火星')).toBe('火星')
    expect(canonicalizeProvince('')).toBe('')
    expect(canonicalizeCity('杭州市', '浙江')).toBe('杭州')
    expect(canonicalizeCity('杭州', '浙江')).toBe('杭州')
    expect(canonicalizeCity('杭县', '浙江')).toBe('杭县')
  })
})

describe('splitRegion (paste a full 企查查-style address)', () => {
  it('splits full-suffix address and ignores the street remainder', () => {
    expect(splitRegion('浙江省杭州市西湖区文三路 100 号')).toEqual({
      province: '浙江',
      city: '杭州',
      district: '西湖区',
    })
  })

  it('works without suffixes', () => {
    expect(splitRegion('浙江杭州西湖文三路')).toMatchObject({
      province: '浙江',
      city: '杭州',
      district: '西湖区',
    })
  })

  it('handles autonomous regions and a direct municipality', () => {
    expect(splitRegion('内蒙古自治区呼和浩特市新城区成吉思汗大街')).toMatchObject({
      province: '内蒙古',
      city: '呼和浩特',
      district: '新城区',
    })
    expect(splitRegion('北京市朝阳区建国路 88 号')).toEqual({
      province: '北京',
      city: '北京',
      district: '朝阳区',
    })
  })

  it('resolves province and city when the district is unknown', () => {
    expect(splitRegion('广东省东莞市南城街道某园区')).toMatchObject({
      province: '广东',
      city: '东莞',
    })
  })

  it('returns an empty result for unrecognised text', () => {
    expect(splitRegion('一段没有任何行政区划的文字')).toEqual({})
    expect(splitRegion('')).toEqual({})
  })
})
